package taskqueue

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/1344011985/MyselfAI/internal/claude"
	"github.com/1344011985/MyselfAI/internal/codex"
	"github.com/1344011985/MyselfAI/internal/imageutil"
	"github.com/1344011985/MyselfAI/internal/kiro"
	"github.com/1344011985/MyselfAI/internal/memory"
	"github.com/1344011985/MyselfAI/internal/prompt"
)

type Logger interface {
	Error(msg string, args ...any)
	Info(msg string, args ...any)
}

// SkillsAugmenter is satisfied by *skills.Hub; defined here to avoid import cycles.
type SkillsAugmenter interface {
	Augment(systemPrompt, input string) string
}

type Queue interface {
	Submit(ctx context.Context, req SubmitRequest) (*Task, error)
	Get(taskID string) (*Task, error)
	ListByUser(userID string, limit int) ([]*Task, error)
	Cancel(taskID string) error
	UpdateVerifyStatus(taskID, status string) error
}

type sqliteQueue struct {
	db             *sql.DB
	store          memory.Store
	runner         *claude.Runner
	codexRunner    *codex.Runner
	kiroRunner     *kiro.Runner
	downloader     *imageutil.Downloader
	selector       *claude.ModelSelector
	systemPrompt   string
	promptBuilder  prompt.Builder
	logger         Logger
	skillsHub      SkillsAugmenter
	jobs           chan queueJob
	cancelMu       sync.Mutex
	cancelMap      map[string]context.CancelFunc
	completionMu   sync.Mutex
	completionMap  map[string]func(CompletionResult)
	completionDone map[string]struct{}
}

// SetSkillsHub wires in the skills hub for per-task prompt augmentation.
// Not part of the Queue interface — call after New() in main.
func (q *sqliteQueue) SetSkillsHub(hub SkillsAugmenter) {
	q.skillsHub = hub
}

// SetKiroRunner wires in the Kiro runner. Not part of the Queue interface.
func (q *sqliteQueue) SetKiroRunner(r *kiro.Runner) {
	q.kiroRunner = r
}

type queueJob struct {
	taskID       string
	progressFn   func(string)
	completionFn func(CompletionResult)
}

func New(db *sql.DB, store memory.Store, runner *claude.Runner, codexRunner *codex.Runner, downloader *imageutil.Downloader, selector *claude.ModelSelector, systemPrompt string, logger Logger, workers int) (Queue, error) {
	if workers <= 0 {
		workers = 2
	}
	q := &sqliteQueue{
		db:             db,
		store:          store,
		runner:         runner,
		codexRunner:    codexRunner,
		downloader:     downloader,
		selector:       selector,
		systemPrompt:   systemPrompt,
		promptBuilder:  prompt.New(systemPrompt),
		logger:         logger,
		jobs:           make(chan queueJob, workers*8),
		cancelMap:      make(map[string]context.CancelFunc),
		completionMap:  make(map[string]func(CompletionResult)),
		completionDone: make(map[string]struct{}),
	}
	if err := q.initSchema(); err != nil {
		return nil, err
	}
	for i := 0; i < workers; i++ {
		go q.worker()
	}
	return q, nil
}

func (q *sqliteQueue) Submit(ctx context.Context, req SubmitRequest) (*Task, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, fmt.Errorf("content is required")
	}
	id, err := newTaskID()
	if err != nil {
		return nil, err
	}
	taskType := string(req.Type)
	if taskType == "" {
		taskType = string(TaskTypeChat)
	}
	if _, err := q.db.ExecContext(ctx, `
		INSERT INTO tasks(id, user_id, group_id, content, status, result, error, session_id, continue_session,
			type, title, goal, project_key, executor, verify_status, parent_task_id, summary, meta_json, executor_session_id)
		VALUES(?, ?, ?, ?, ?, '', '', '', ?,
			?, ?, ?, ?, ?, 'none', '', '', '', '')
	`, id, req.UserID, req.GroupID, content, string(StatusPending), boolToInt(req.ContinueSession),
		taskType, req.Title, req.Goal, req.ProjectKey, req.Executor); err != nil {
		return nil, err
	}
	task, err := q.Get(id)
	if err != nil {
		return nil, err
	}
	if req.CompletionFn != nil {
		q.completionMu.Lock()
		q.completionMap[id] = req.CompletionFn
		q.completionMu.Unlock()
	}
	q.jobs <- queueJob{taskID: id, progressFn: req.ProgressFn, completionFn: req.CompletionFn}
	return task, nil
}

func (q *sqliteQueue) Get(taskID string) (*Task, error) {
	row := q.db.QueryRow(`
		SELECT id, user_id, group_id, content, status, result, error, session_id, continue_session, created_at, started_at, done_at,
			type, title, goal, project_key, executor, verify_status, parent_task_id, summary, meta_json, executor_session_id
		FROM tasks WHERE id = ?
	`, taskID)
	return scanTask(row)
}

func (q *sqliteQueue) ListByUser(userID string, limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := q.db.Query(`
		SELECT id, user_id, group_id, content, status, result, error, session_id, continue_session, created_at, started_at, done_at,
			type, title, goal, project_key, executor, verify_status, parent_task_id, summary, meta_json, executor_session_id
		FROM tasks WHERE user_id = ? ORDER BY created_at DESC LIMIT ?
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (q *sqliteQueue) Cancel(taskID string) error {
	task, err := q.Get(taskID)
	if err != nil {
		return err
	}
	if task.Status == StatusDone || task.Status == StatusFailed || task.Status == StatusCancelled {
		return nil
	}
	q.cancelMu.Lock()
	cancel := q.cancelMap[taskID]
	q.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	res, err := q.db.Exec(`UPDATE tasks SET status = ?, error = CASE WHEN error = '' THEN 'cancelled by user' ELSE error END, done_at = CURRENT_TIMESTAMP WHERE id = ? AND status IN (?, ?)`, string(StatusCancelled), taskID, string(StatusPending), string(StatusRunning))
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected > 0 {
		updated, _ := q.Get(taskID)
		if updated == nil {
			updated = task
			updated.Status = StatusCancelled
			if strings.TrimSpace(updated.Error) == "" {
				updated.Error = "cancelled by user"
			}
		}
		q.callCompletionOnce(taskID, nil, CompletionResult{Task: updated, Error: taskError(updated, "cancelled by user")})
	}
	return err
}

func (q *sqliteQueue) UpdateVerifyStatus(taskID, status string) error {
	_, err := q.db.Exec(`UPDATE tasks SET verify_status = ? WHERE id = ?`, status, taskID)
	return err
}

func (q *sqliteQueue) worker() {
	for job := range q.jobs {
		q.runTask(job)
	}
}

func formatRecentHistory(entries []memory.HistoryEntry, maxTurns int) string {
	if len(entries) == 0 || maxTurns <= 0 {
		return ""
	}
	if len(entries) > maxTurns {
		entries = entries[:maxTurns]
	}

	var lines []string
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		input := strings.TrimSpace(e.Input)
		response := strings.TrimSpace(e.Response)
		if input == "" && response == "" {
			continue
		}
		if input != "" {
			lines = append(lines, "用户："+truncateRunes(input, 600))
		}
		if response != "" {
			lines = append(lines, "助手："+truncateRunes(response, 800))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "## 最近对话历史（短期记忆）\n" + strings.Join(lines, "\n\n")
}

func truncateRunes(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if max <= 0 || len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}

func (q *sqliteQueue) runTask(job queueJob) {
	task, err := q.getTaskWithRetry(job.taskID)
	if err != nil {
		q.logger.Error("taskqueue get task failed", "task_id", job.taskID, "err", err)
		_, _ = q.db.Exec(`UPDATE tasks SET status = ?, error = ?, done_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?`,
			string(StatusFailed), "taskqueue get task failed: "+err.Error(), job.taskID, string(StatusPending))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.cancelMu.Lock()
	q.cancelMap[job.taskID] = cancel
	q.cancelMu.Unlock()
	defer func() {
		q.cancelMu.Lock()
		delete(q.cancelMap, job.taskID)
		q.cancelMu.Unlock()
		cancel()
	}()

	res, err := q.db.Exec(`UPDATE tasks SET status = ?, started_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?`, string(StatusRunning), task.ID, string(StatusPending))
	if err != nil {
		q.logger.Error("taskqueue mark running failed", "task_id", task.ID, "err", err)
		return
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		updated, _ := q.Get(task.ID)
		if updated != nil && updated.Status == StatusCancelled {
			q.callCompletionOnce(task.ID, job.completionFn, CompletionResult{Task: updated, Error: taskError(updated, "cancelled by user")})
		}
		return
	}

	q.logger.Info("task started", "task_id", task.ID, "type", string(task.Type), "title", task.Title, "user_id", task.UserID)

	runnerName := strings.ToLower(strings.TrimSpace(task.Executor))
	if runnerName == "" {
		runnerName = "claude"
	}

	sessionID := ""
	if task.ContinueSession && runnerName != "codex" {
		sessionID, _ = q.store.GetSession(task.UserID)
	}

	history, _ := q.store.GetHistory(task.UserID, 12)

	// 组装 system prompt：优先用 PromptBuilder，fallback 到老逻辑
	memories, _ := q.store.GetMemories(task.UserID)
	var systemPrompt string
	if q.promptBuilder != nil {
		input := prompt.BuildInput{
			Task: prompt.TaskInfo{
				ID:         task.ID,
				Type:       string(task.Type),
				Title:      task.Title,
				Goal:       task.Goal,
				Content:    task.Content,
				ProjectKey: task.ProjectKey,
				Executor:   task.Executor,
			},
			IsResume: sessionID != "",
		}
		var scratchpad []string
		if len(memories) > 0 {
			scratchpad = append(scratchpad, "## 用户个人记忆\n"+strings.Join(memories, "\n"))
		}
		if recent := formatRecentHistory(history, 8); recent != "" {
			scratchpad = append(scratchpad, recent)
		}
		if len(scratchpad) > 0 {
			input.Scratchpad = strings.Join(scratchpad, "\n\n")
		}
		systemPrompt = q.promptBuilder.Build(input)
		q.logger.Info("prompt built via PromptBuilder", "task_id", task.ID, "is_resume", sessionID != "", "prompt_len", len(systemPrompt))
	} else {
		// fallback: 老逻辑
		var promptParts []string
		if q.systemPrompt != "" {
			promptParts = append(promptParts, q.systemPrompt)
		}
		if len(memories) > 0 {
			promptParts = append(promptParts, "## 用户个人记忆\n"+strings.Join(memories, "\n"))
		}
		if recent := formatRecentHistory(history, 8); recent != "" {
			promptParts = append(promptParts, recent)
		}
		systemPrompt = strings.Join(promptParts, "\n\n")
	}

	// Apply skills (keyword-triggered prompt snippets) to system prompt.
	if q.skillsHub != nil {
		systemPrompt = q.skillsHub.Augment(systemPrompt, task.Content)
	}

	userPref, _ := q.store.GetModelPreference(task.UserID)
	modelKey := q.selector.SelectModel(userPref, task.Content, 0, len(history))
	modelName := q.selector.GetModelName(modelKey)

	var result *claude.RunResult
	var runErr error
	usedRunner := runnerName
	logModel := modelKey

	switch runnerName {
	case "kiro":
		if q.kiroRunner != nil {
			effectiveModel := q.kiroRunner.EffectiveModel(modelName)
			logModel = effectiveModel
			q.logger.Info("dispatching task to kiro", "task_id", task.ID, "model", effectiveModel, "selected_model", modelName)
			result, runErr = q.kiroRunner.RunWithModel(ctx, task.Content, sessionID, systemPrompt, nil, modelName, job.progressFn)
		} else {
			runErr = fmt.Errorf("kiro runner is not configured")
		}
		if runErr != nil && ctx.Err() == nil && q.runner != nil {
			q.logger.Error("kiro failed, fallback to claude", "task_id", task.ID, "err", runErr)
			if job.progressFn != nil {
				job.progressFn("Kiro 执行超时或失败，已自动降级到 Claude 继续处理…")
			}
			usedRunner = "claude_code"
			modelKey = q.selector.SelectModel(userPref, task.Content, 0, len(history))
			modelName = q.selector.GetModelName(modelKey)
			logModel = modelKey
			result, runErr = q.runner.RunWithModel(ctx, task.Content, sessionID, systemPrompt, nil, modelName, job.progressFn)
		}
	case "codex":
		if q.codexRunner != nil {
			if modelName == "" || strings.HasPrefix(modelName, "claude-") {
				modelName = "gpt-5.5"
			}
			q.logger.Info("dispatching task to codex", "task_id", task.ID, "model", modelName)
			result, runErr = q.codexRunner.RunWithModel(ctx, task.Content, sessionID, systemPrompt, nil, modelName, job.progressFn)
		} else {
			runErr = fmt.Errorf("codex runner is not configured")
		}
		if runErr != nil && ctx.Err() == nil && q.runner != nil {
			q.logger.Error("codex failed, fallback to claude", "task_id", task.ID, "err", runErr)
			if job.progressFn != nil {
				job.progressFn("Codex 执行失败，已自动降级到 Claude 继续处理…")
			}
			usedRunner = "claude_code"
			modelKey = q.selector.SelectModel(userPref, task.Content, 0, len(history))
			modelName = q.selector.GetModelName(modelKey)
			logModel = modelKey
			result, runErr = q.runner.RunWithModel(ctx, task.Content, sessionID, systemPrompt, nil, modelName, job.progressFn)
		}
	default:
		usedRunner = "claude_code"
		q.logger.Info("dispatching task to claude", "task_id", task.ID, "model", modelName)
		result, runErr = q.runner.RunWithModel(ctx, task.Content, sessionID, systemPrompt, nil, modelName, job.progressFn)
	}
	if runErr != nil {
		status := StatusFailed
		errText := runErr.Error()
		if ctx.Err() == context.Canceled {
			status = StatusCancelled
			if errText == "" {
				errText = "cancelled by user"
			}
		}
		_, _ = q.db.Exec(`UPDATE tasks SET status = ?, error = ?, done_at = CURRENT_TIMESTAMP WHERE id = ?`, string(status), errText, task.ID)
		q.logger.Info("task finished", "task_id", task.ID, "status", string(status), "error", errText)
		// 失败/取消也要回调
		if job.completionFn != nil {
			updated, _ := q.Get(task.ID)
			if updated == nil {
				updated = task
			}
			q.callCompletionOnce(task.ID, job.completionFn, CompletionResult{Task: updated, Error: runErr})
		}
		return
	}

	if updated, _ := q.Get(task.ID); updated != nil && updated.Status == StatusCancelled {
		q.callCompletionOnce(task.ID, job.completionFn, CompletionResult{Task: updated, Error: taskError(updated, "cancelled by user")})
		return
	}

	if task.ContinueSession && result.SessionID != "" && usedRunner != "codex" {
		if err := q.store.SaveSession(task.UserID, result.SessionID); err != nil {
			q.logger.Error("taskqueue save session failed", "task_id", task.ID, "err", err)
		}
	}
	if err := q.store.SaveHistory(task.UserID, task.Content, result.Text); err != nil {
		q.logger.Error("taskqueue save history failed", "task_id", task.ID, "err", err)
	}
	if result.Usage != nil {
		cost := q.selector.CalculateCost(modelKey, result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheCreationTokens, result.Usage.CacheReadTokens)
		_ = q.store.RecordUsage(&memory.UsageRecord{
			UserID:              task.UserID,
			SessionID:           result.SessionID,
			Model:               modelKey,
			InputTokens:         result.Usage.InputTokens,
			OutputTokens:        result.Usage.OutputTokens,
			CacheCreationTokens: result.Usage.CacheCreationTokens,
			CacheReadTokens:     result.Usage.CacheReadTokens,
			TotalCostUSD:        cost,
			CreatedAt:           time.Now(),
		})
	}

	// 更新 executor_session_id
	execSessionID := result.SessionID
	if _, err := q.db.Exec(`UPDATE tasks SET status = ?, result = ?, session_id = ?, executor_session_id = ?, done_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(StatusDone), result.Text, result.SessionID, execSessionID, task.ID); err != nil {
		q.logger.Error("taskqueue mark done failed", "task_id", task.ID, "err", err)
		if _, retryErr := q.db.Exec(`UPDATE tasks SET status = ?, error = ?, done_at = CURRENT_TIMESTAMP WHERE id = ?`,
			string(StatusFailed), "mark done failed: "+err.Error(), task.ID); retryErr != nil {
			q.logger.Error("taskqueue mark failed after done-update failure failed", "task_id", task.ID, "err", retryErr)
		}
		return
	}

	q.logger.Info("task finished", "task_id", task.ID, "status", "done", "result_len", len(result.Text), "model", logModel, "executor", usedRunner)

	// 成功回调
	if job.completionFn != nil {
		updated, _ := q.Get(task.ID)
		if updated == nil {
			updated = task
		}
		q.callCompletionOnce(task.ID, job.completionFn, CompletionResult{Task: updated, Result: result.Text})
	}
}

func (q *sqliteQueue) callCompletionOnce(taskID string, fallback func(CompletionResult), result CompletionResult) {
	q.completionMu.Lock()
	if _, done := q.completionDone[taskID]; done {
		q.completionMu.Unlock()
		return
	}
	q.completionDone[taskID] = struct{}{}
	fn := q.completionMap[taskID]
	delete(q.completionMap, taskID)
	q.completionMu.Unlock()
	if fn == nil {
		fn = fallback
	}
	if fn != nil {
		safeCallCompletion(fn, result)
	}
}

func taskError(task *Task, fallback string) error {
	if task != nil {
		if errText := strings.TrimSpace(task.Error); errText != "" {
			return errors.New(errText)
		}
	}
	return errors.New(fallback)
}

func (q *sqliteQueue) getTaskWithRetry(taskID string) (*Task, error) {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		task, err := q.Get(taskID)
		if err == nil {
			return task, nil
		}
		lastErr = err
		if !isSQLiteBusy(err) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 150 * time.Millisecond)
	}
	return nil, lastErr
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "database is locked") || strings.Contains(text, "sqlite_busy")
}

// safeCallCompletion calls the completion callback with panic recovery.
func safeCallCompletion(fn func(CompletionResult), result CompletionResult) {
	defer func() {
		if r := recover(); r != nil {
			_ = r
		}
	}()
	fn(result)
}

func (q *sqliteQueue) initSchema() error {
	_, err := q.db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			group_id TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			result TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			continue_session INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			done_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_tasks_user_created ON tasks(user_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_tasks_user_status ON tasks(user_id, status);
	`)
	if err != nil {
		return err
	}
	// ALTER TABLE 兼容加列（忽略 duplicate column 错误）
	newColumns := []string{
		"ALTER TABLE tasks ADD COLUMN type TEXT NOT NULL DEFAULT 'chat'",
		"ALTER TABLE tasks ADD COLUMN title TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN goal TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN project_key TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN executor TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN verify_status TEXT NOT NULL DEFAULT 'none'",
		"ALTER TABLE tasks ADD COLUMN parent_task_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN summary TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN meta_json TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE tasks ADD COLUMN executor_session_id TEXT NOT NULL DEFAULT ''",
	}
	for _, stmt := range newColumns {
		if _, err := q.db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("alter table: %w", err)
		}
	}
	return nil
}

func scanTask(scanner interface{ Scan(dest ...any) error }) (*Task, error) {
	var t Task
	var continueSession int
	var createdAt string
	var startedAt sql.NullString
	var doneAt sql.NullString
	var taskType sql.NullString
	var title, goal, projectKey, executor, verifyStatus, parentTaskID, summary, metaJSON, executorSessionID sql.NullString
	if err := scanner.Scan(
		&t.ID, &t.UserID, &t.GroupID, &t.Content, &t.Status, &t.Result, &t.Error, &t.SessionID, &continueSession,
		&createdAt, &startedAt, &doneAt,
		&taskType, &title, &goal, &projectKey, &executor, &verifyStatus, &parentTaskID, &summary, &metaJSON, &executorSessionID,
	); err != nil {
		return nil, err
	}
	t.ContinueSession = continueSession == 1
	t.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	if startedAt.Valid {
		if ts, err := time.Parse("2006-01-02 15:04:05", startedAt.String); err == nil {
			t.StartedAt = &ts
		}
	}
	if doneAt.Valid {
		if ts, err := time.Parse("2006-01-02 15:04:05", doneAt.String); err == nil {
			t.DoneAt = &ts
		}
	}
	if taskType.Valid {
		t.Type = TaskType(taskType.String)
	}
	if title.Valid {
		t.Title = title.String
	}
	if goal.Valid {
		t.Goal = goal.String
	}
	if projectKey.Valid {
		t.ProjectKey = projectKey.String
	}
	if executor.Valid {
		t.Executor = executor.String
	}
	if verifyStatus.Valid {
		t.VerifyStatus = verifyStatus.String
	}
	if parentTaskID.Valid {
		t.ParentTaskID = parentTaskID.String
	}
	if summary.Valid {
		t.Summary = summary.String
	}
	if metaJSON.Valid {
		t.MetaJSON = metaJSON.String
	}
	if executorSessionID.Valid {
		t.ExecutorSessionID = executorSessionID.String
	}
	return &t, nil
}

func newTaskID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "task_" + hex.EncodeToString(buf), nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
