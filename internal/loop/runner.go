package loop

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/1344011985/MyselfAI/internal/taskqueue"
)

type Runner struct {
	store    Store
	queue    taskqueue.Queue
	brain    *BrainStore
	notifier Notifier
}

type RunnerOption func(*Runner)

func WithBrainStore(brain *BrainStore) RunnerOption {
	return func(r *Runner) {
		r.brain = brain
	}
}

func WithNotifier(notifier Notifier) RunnerOption {
	return func(r *Runner) {
		r.notifier = notifier
	}
}

func NewRunner(store Store, queue taskqueue.Queue, opts ...RunnerOption) *Runner {
	r := &Runner{store: store, queue: queue}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

func loopExecutor(executor string) string {
	executor = strings.TrimSpace(executor)
	if executor == "" {
		return "kiro"
	}
	return executor
}

func (r *Runner) RunManual(ctx context.Context, scheduleID, userID string) (*LoopRun, *taskqueue.Task, error) {
	if r == nil || r.store == nil {
		return nil, nil, fmt.Errorf("loop store is not configured")
	}
	if r.queue == nil {
		return nil, nil, fmt.Errorf("task queue is not configured")
	}
	schedule, err := r.store.GetSchedule(ctx, scheduleID)
	if err != nil {
		return nil, nil, err
	}
	if schedule.UserID != userID {
		return nil, nil, fmt.Errorf("permission denied")
	}
	return r.run(ctx, schedule, TriggerTypeManual, "manual loop run created")
}

func (r *Runner) RunScheduled(ctx context.Context, scheduleID string) (*LoopRun, *taskqueue.Task, error) {
	if r == nil || r.store == nil {
		return nil, nil, fmt.Errorf("loop store is not configured")
	}
	if r.queue == nil {
		return nil, nil, fmt.Errorf("task queue is not configured")
	}
	schedule, err := r.store.GetSchedule(ctx, scheduleID)
	if err != nil {
		return nil, nil, err
	}
	return r.run(ctx, schedule, TriggerTypeScheduled, "scheduled loop run created")
}

func (r *Runner) run(ctx context.Context, schedule *LoopSchedule, triggerType TriggerType, createdMessage string) (*LoopRun, *taskqueue.Task, error) {
	if r.hasActiveRun(ctx, schedule.ID, "") {
		return nil, nil, fmt.Errorf("%w: %s", ErrActiveRun, schedule.ID)
	}
	brainContent := ""
	brainPath := ""
	if r.brain != nil {
		if content, path, err := r.brain.Read(schedule); err == nil {
			brainContent = content
			brainPath = path
		}
	}
	run := &LoopRun{
		ScheduleID:  schedule.ID,
		TriggerType: triggerType,
		Status:      RunStatusPending,
	}
	if err := r.store.CreateRun(ctx, run); err != nil {
		return nil, nil, err
	}
	_ = r.store.AppendEvent(ctx, &LoopEvent{
		RunID:      run.ID,
		ScheduleID: schedule.ID,
		EventType:  EventTypeCreated,
		Message:    createdMessage,
	})
	if brainPath != "" {
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      run.ID,
			ScheduleID: schedule.ID,
			EventType:  EventTypeProgress,
			Message:    "brain loaded: " + brainPath,
		})
	}

	task, err := r.queue.Submit(ctx, taskqueue.SubmitRequest{
		UserID:          schedule.UserID,
		GroupID:         schedule.GroupID,
		Content:         BuildParentTaskPrompt(schedule, brainContent),
		ContinueSession: false,
		Type:            taskqueue.TaskTypeProject,
		Title:           "Loop Run: " + schedule.Title,
		Goal:            schedule.Goal,
		ProjectKey:      schedule.ProjectKey,
		Executor:        loopExecutor(schedule.Executor),
		ProgressFn:      r.progressFn(schedule, run.ID),
		CompletionFn:    r.completionFn(schedule.ID, run.ID),
	})
	if err != nil {
		r.failRun(ctx, schedule, run.ID, "submit", err)
		return run, nil, err
	}

	if err := r.store.UpdateRunParentTask(ctx, run.ID, task.ID); err != nil {
		return run, task, err
	}
	if err := r.store.UpdateRunStatus(ctx, run.ID, RunStatusRunning, "", "", ""); err != nil {
		return run, task, err
	}
	_ = r.store.AppendEvent(ctx, &LoopEvent{
		RunID:      run.ID,
		ScheduleID: schedule.ID,
		TaskID:     task.ID,
		EventType:  EventTypeParentTaskSubmitted,
		Message:    "parent task submitted",
	})
	r.notify(schedule, run.ID, fmt.Sprintf("Loop run 已启动\nloop：%s\nrun：%s\nparent task：%s\n目标：%s",
		schedule.ID, run.ID, task.ID, truncate(schedule.Title, 80)))
	updated, err := r.store.GetRun(ctx, run.ID)
	if err == nil {
		run = updated
	}
	return run, task, nil
}

func (r *Runner) completionFn(scheduleID, runID string) func(taskqueue.CompletionResult) {
	return func(res taskqueue.CompletionResult) {
		ctx := context.Background()
		if run, err := r.store.GetRun(ctx, runID); err == nil && isTerminalRunStatus(run.Status) {
			return
		}
		if res.Error != nil {
			schedule, err := r.store.GetSchedule(ctx, scheduleID)
			if err != nil {
				_ = r.store.UpdateRunStatus(ctx, runID, RunStatusFailed, "", "", res.Error.Error())
				return
			}
			r.failRun(ctx, schedule, runID, "execute", res.Error)
			return
		}
		if resultErr := ErrorFromResult(res.Result); resultErr != nil {
			schedule, err := r.store.GetSchedule(ctx, scheduleID)
			if err != nil {
				_ = r.store.UpdateRunStatus(ctx, runID, RunStatusFailed, "", "", resultErr.Error())
				return
			}
			r.failRun(ctx, schedule, runID, "execute", resultErr)
			return
		}
		summary := truncate(res.Result, 1200)
		control := ParseLoopControl(res.Result)
		report := ExtractRunReport(res.Result)
		decision := DecisionFromResult(res.Result)
		risk := DetectRunRisk(decision, report)
		_ = r.store.UpdateRunStatus(ctx, runID, RunStatusDone, summary, control.Action, "")
		_ = r.store.UpdateRunReport(ctx, runID, report)
		if r.brain != nil {
			r.writeBrainBlock(ctx, scheduleID, runID, res.Result)
		}
		if strings.Contains(decision, "approval_required") {
			_ = r.store.AppendEvent(ctx, &LoopEvent{
				RunID:      runID,
				ScheduleID: scheduleID,
				EventType:  EventTypeApprovalRequired,
				Level:      EventLevelWarn,
				Message:    "parent task requested manual approval",
			})
		}
		if risk.Pause {
			_ = r.store.UpdateScheduleStatus(ctx, scheduleID, ScheduleStatusPaused)
			_ = r.store.AppendEvent(ctx, &LoopEvent{
				RunID:      runID,
				ScheduleID: scheduleID,
				EventType:  risk.EventType,
				Level:      EventLevelWarn,
				Message:    risk.Message,
			})
		}
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      runID,
			ScheduleID: scheduleID,
			EventType:  EventTypeCompleted,
			Message:    "parent task completed; loop control=" + control.Action,
		})
		if schedule, err := r.store.GetSchedule(ctx, scheduleID); err == nil {
			r.notify(schedule, runID, buildCompletionNotice(schedule, runID, control, summary, report))
			if risk.Pause {
				r.notify(schedule, runID, buildRiskPauseNotice(schedule, runID, risk))
			}
		}
		r.handleTaskDoneControl(ctx, scheduleID, runID, control)
	}
}

func (r *Runner) progressFn(schedule *LoopSchedule, runID string) func(string) {
	if r == nil || r.notifier == nil || schedule == nil {
		return nil
	}
	var mu sync.Mutex
	var last time.Time
	return func(partial string) {
		partial = strings.TrimSpace(partial)
		if partial == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if !last.IsZero() && time.Since(last) < 90*time.Second {
			return
		}
		last = time.Now()
		r.notify(schedule, runID, fmt.Sprintf("Loop run 进度\nloop：%s\nrun：%s\n%s",
			schedule.ID, runID, truncate(partial, 700)))
	}
}

func (r *Runner) failRun(ctx context.Context, schedule *LoopSchedule, runID, stage string, err error) {
	if schedule == nil {
		return
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	diag := DiagnoseLoopError(stage, err)
	diag.RetryCount = r.nextRetryCount(ctx, schedule.ID, runID)
	if diag.Retryable && diag.RetryCount <= maxLoopAutoRetries && !r.maxRunsReached(ctx, schedule) {
		next := time.Now().In(loadScheduleLocation(schedule.Timezone)).Add(RetryBackoff(diag.Kind, diag.RetryCount))
		diag.NextRetry = &next
		_ = r.store.UpdateScheduleNextRun(ctx, schedule.ID, diag.NextRetry)
	}
	status := RunStatusFailed
	if diag.Kind == "cancelled" {
		status = RunStatusCancelled
	}
	_ = r.store.UpdateRunFailure(ctx, runID, status, errText, diag)

	message := fmt.Sprintf("parent task failed: stage=%s kind=%s retryable=%t retry_count=%d",
		diag.Stage, diag.Kind, diag.Retryable, diag.RetryCount)
	if diag.NextRetry != nil {
		message += " next_retry_at=" + formatDiagnosticTime(*diag.NextRetry, schedule.Timezone)
	}
	if errText != "" {
		message += " error=" + truncate(errText, 400)
	}
	_ = r.store.AppendEvent(ctx, &LoopEvent{
		RunID:      runID,
		ScheduleID: schedule.ID,
		EventType:  EventTypeFailed,
		Level:      EventLevelError,
		Message:    message,
	})
	if diag.NextRetry != nil {
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      runID,
			ScheduleID: schedule.ID,
			EventType:  EventTypeProgress,
			Message:    "auto retry scheduled after backoff",
		})
	}
	r.notify(schedule, runID, buildFailureNotice(schedule, runID, status, diag, errText))
}

func buildCompletionNotice(schedule *LoopSchedule, runID string, control LoopControl, summary string, report RunReport) string {
	var sb strings.Builder
	sb.WriteString("Loop run 结束\n")
	sb.WriteString("状态：done\n")
	fmt.Fprintf(&sb, "loop：%s\nrun：%s\naction：%s\n", schedule.ID, runID, control.Action)
	appendNoticeLine(&sb, "摘要", summary, 700)
	appendNoticeLine(&sb, "产物", report.ArtifactSummary, 320)
	appendNoticeLine(&sb, "Diff", report.DiffSummary, 320)
	appendNoticeLine(&sb, "日志", report.LogSummary, 320)
	fmt.Fprintf(&sb, "详情：/loop runlog %s", runID)
	return sb.String()
}

func buildFailureNotice(schedule *LoopSchedule, runID string, status RunStatus, diag ErrorDiagnosis, errText string) string {
	var sb strings.Builder
	sb.WriteString("Loop run 结束\n")
	fmt.Fprintf(&sb, "状态：%s\nloop：%s\nrun：%s\nstage：%s\nkind：%s\nretryable：%t\n", status, schedule.ID, runID, diag.Stage, diag.Kind, diag.Retryable)
	if diag.NextRetry != nil {
		fmt.Fprintf(&sb, "next_retry_at：%s\n", formatDiagnosticTime(*diag.NextRetry, schedule.Timezone))
	}
	appendNoticeLine(&sb, "error", errText, 700)
	fmt.Fprintf(&sb, "详情：/loop runlog %s", runID)
	return sb.String()
}

func buildRiskPauseNotice(schedule *LoopSchedule, runID string, risk RunRisk) string {
	var sb strings.Builder
	sb.WriteString("Loop 已自动暂停\n")
	fmt.Fprintf(&sb, "loop：%s\nrun：%s\n原因：%s\n", schedule.ID, runID, risk.Message)
	fmt.Fprintf(&sb, "处理后可用 /loop resume %s 恢复。", schedule.ID)
	return sb.String()
}

func appendNoticeLine(sb *strings.Builder, label, value string, max int) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") {
		return
	}
	fmt.Fprintf(sb, "%s：%s\n", label, truncate(value, max))
}

func (r *Runner) notify(schedule *LoopSchedule, runID, content string) {
	if r == nil || r.notifier == nil || schedule == nil {
		return
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	target := NotifyTarget{UserID: schedule.UserID, GroupID: schedule.GroupID}
	go func() {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := r.notifier.Notify(notifyCtx, target, content); err != nil {
			_ = r.store.AppendEvent(context.Background(), &LoopEvent{
				RunID:      runID,
				ScheduleID: schedule.ID,
				EventType:  EventTypeProgress,
				Level:      EventLevelWarn,
				Message:    "loop notify failed: " + err.Error(),
			})
		}
	}()
}

func (r *Runner) nextRetryCount(ctx context.Context, scheduleID, currentRunID string) int {
	runs, err := r.store.ListRuns(ctx, scheduleID, 10)
	if err != nil {
		return 1
	}
	count := 1
	for _, run := range runs {
		if run.ID == currentRunID {
			continue
		}
		if run.Status != RunStatusFailed {
			break
		}
		count++
	}
	return count
}

func formatDiagnosticTime(t time.Time, timezone string) string {
	return t.In(loadScheduleLocation(timezone)).Format("2006-01-02 15:04:05")
}

type LoopControl struct {
	Action    string
	Delay     time.Duration
	NextRunAt *time.Time
}

var (
	loopControlSectionPattern = regexp.MustCompile(`(?is)##\s*Loop Control\s*\n+(.*?)(?:\n##\s|\z)`)
	loopNextRunPattern        = regexp.MustCompile(`(?im)^\s*next_run_at\s*:\s*(.+?)\s*$`)
	loopWaitPattern           = regexp.MustCompile(`(?im)^\s*(?:wait|delay)\s*:\s*([0-9]+(?:\.[0-9]+)?\s*(?:[a-zA-Z]+|\p{Han}+))\s*$`)
)

func ParseLoopControl(result string) LoopControl {
	control := LoopControl{Action: "wait"}
	if matches := loopControlSectionPattern.FindStringSubmatch(result); len(matches) >= 2 {
		body := strings.TrimSpace(matches[1])
		line := firstNonEmptyLine(body)
		control.Action, control.Delay = parseControlAction(line)
		if control.Delay == 0 {
			control.Delay = parseWaitDuration(body)
		}
		control.NextRunAt = parseNextRunAt(body)
	}
	return control
}

func parseControlAction(value string) (string, time.Duration) {
	value = strings.ToLower(strings.TrimSpace(value))
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '|' || r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	if len(fields) > 0 {
		value = strings.TrimSpace(fields[0])
	}
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return "wait", 0
	}
	action := normalizeLoopControlAction(parts[0])
	if len(parts) >= 2 && action == "wait" {
		if duration, err := parseHumanDuration(parts[1]); err == nil {
			return action, duration
		}
	}
	return action, 0
}

func normalizeLoopControlAction(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "continue", "wait", "done", "pause":
		return value
	default:
		return "wait"
	}
}

func (r *Runner) handleTaskDoneControl(ctx context.Context, scheduleID, completedRunID string, control LoopControl) {
	schedule, err := r.store.GetSchedule(ctx, scheduleID)
	if err != nil || schedule.Status != ScheduleStatusActive {
		return
	}
	switch control.Action {
	case "done", "pause":
		_ = r.store.UpdateScheduleStatus(ctx, schedule.ID, ScheduleStatusPaused)
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      completedRunID,
			ScheduleID: schedule.ID,
			EventType:  EventTypePaused,
			Message:    "loop paused by task_done control: " + control.Action,
		})
	case "continue":
		r.continueAfterTaskDone(ctx, schedule, completedRunID)
	case "wait":
		r.applyRhythm(ctx, schedule, completedRunID, control)
	}
}

func (r *Runner) continueAfterTaskDone(ctx context.Context, schedule *LoopSchedule, completedRunID string) {
	if !HasTaskDoneTrigger(schedule) {
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      completedRunID,
			ScheduleID: schedule.ID,
			EventType:  EventTypeProgress,
			Level:      EventLevelWarn,
			Message:    "task_done continue ignored because schedule has no task_done trigger",
		})
		return
	}
	if r.hasActiveRun(ctx, schedule.ID, completedRunID) {
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      completedRunID,
			ScheduleID: schedule.ID,
			EventType:  EventTypeProgress,
			Level:      EventLevelWarn,
			Message:    "task_done continue skipped because another run is active",
		})
		return
	}
	if r.maxRunsReached(ctx, schedule) {
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      completedRunID,
			ScheduleID: schedule.ID,
			EventType:  EventTypeProgress,
			Level:      EventLevelWarn,
			Message:    "task_done continue skipped because max_runs_per_day reached",
		})
		return
	}
	run, task, err := r.run(ctx, schedule, TriggerTypeTaskDone, "task_done loop run created")
	if err != nil {
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      completedRunID,
			ScheduleID: schedule.ID,
			EventType:  EventTypeFailed,
			Level:      EventLevelError,
			Message:    "task_done continue failed: " + err.Error(),
		})
		return
	}
	_ = r.store.AppendEvent(ctx, &LoopEvent{
		RunID:      run.ID,
		ScheduleID: schedule.ID,
		TaskID:     task.ID,
		EventType:  EventTypeTaskDoneTriggered,
		Message:    "task_done trigger submitted next loop run",
	})
}

func (r *Runner) hasActiveRun(ctx context.Context, scheduleID, exceptRunID string) bool {
	runs, err := r.store.ListRuns(ctx, scheduleID, 10)
	if err != nil {
		return true
	}
	for _, run := range runs {
		if run.ID == exceptRunID {
			continue
		}
		if run.Status == RunStatusPending || run.Status == RunStatusRunning {
			return true
		}
	}
	return false
}

func (r *Runner) applyRhythm(ctx context.Context, schedule *LoopSchedule, completedRunID string, control LoopControl) {
	next := control.NextRunAt
	if next == nil && control.Delay > 0 {
		value := time.Now().In(loadScheduleLocation(schedule.Timezone)).Add(control.Delay)
		next = &value
	}
	if next == nil {
		return
	}
	if err := r.store.UpdateScheduleNextRun(ctx, schedule.ID, next); err != nil {
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      completedRunID,
			ScheduleID: schedule.ID,
			EventType:  EventTypeFailed,
			Level:      EventLevelError,
			Message:    "failed to apply loop rhythm: " + err.Error(),
		})
		return
	}
	_ = r.store.AppendEvent(ctx, &LoopEvent{
		RunID:      completedRunID,
		ScheduleID: schedule.ID,
		EventType:  EventTypeProgress,
		Message:    "loop rhythm updated next_run_at",
	})
}

func (r *Runner) writeBrainBlock(ctx context.Context, scheduleID, runID, result string) {
	content, ok := ExtractBrainBlock(result)
	if !ok {
		return
	}
	schedule, err := r.store.GetSchedule(ctx, scheduleID)
	if err != nil {
		return
	}
	path, err := r.brain.Write(schedule, content)
	if err != nil {
		_ = r.store.AppendEvent(ctx, &LoopEvent{
			RunID:      runID,
			ScheduleID: scheduleID,
			EventType:  EventTypeFailed,
			Level:      EventLevelError,
			Message:    "brain write failed: " + err.Error(),
		})
		return
	}
	_ = r.store.AppendEvent(ctx, &LoopEvent{
		RunID:      runID,
		ScheduleID: scheduleID,
		EventType:  EventTypeProgress,
		Message:    "brain updated: " + path,
	})
}

func (r *Runner) maxRunsReached(ctx context.Context, schedule *LoopSchedule) bool {
	if schedule.MaxRunsPerDay <= 0 {
		return false
	}
	loc := loadScheduleLocation(schedule.Timezone)
	now := time.Now().In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).UTC()
	count, err := r.store.CountRunsSince(ctx, schedule.ID, dayStart)
	if err != nil {
		return true
	}
	return count >= schedule.MaxRunsPerDay
}

func firstNonEmptyLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func parseWaitDuration(value string) time.Duration {
	if matches := loopWaitPattern.FindStringSubmatch(value); len(matches) >= 2 {
		if duration, err := parseHumanDuration(matches[1]); err == nil {
			return duration
		}
	}
	return 0
}

func parseNextRunAt(value string) *time.Time {
	matches := loopNextRunPattern.FindStringSubmatch(value)
	if len(matches) < 2 {
		return nil
	}
	raw := strings.TrimSpace(matches[1])
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, raw, loadScheduleLocation("Asia/Shanghai")); err == nil {
			return &parsed
		}
	}
	return nil
}

func parseHumanDuration(value string) (time.Duration, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "分钟", "m")
	value = strings.ReplaceAll(value, "分", "m")
	value = strings.ReplaceAll(value, "小时", "h")
	value = strings.ReplaceAll(value, "时", "h")
	value = strings.ReplaceAll(value, "天", "d")
	value = strings.ReplaceAll(value, " ", "")
	if strings.HasSuffix(value, "d") {
		days, err := time.ParseDuration(strings.TrimSuffix(value, "d") + "h")
		if err != nil {
			return 0, err
		}
		return days * 24, nil
	}
	return time.ParseDuration(value)
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if max <= 0 || len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "..."
}
