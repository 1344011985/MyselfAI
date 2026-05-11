package loop

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrActiveRun = errors.New("loop already has an active run")

type Store interface {
	CreateSchedule(ctx context.Context, schedule *LoopSchedule) error
	GetSchedule(ctx context.Context, id string) (*LoopSchedule, error)
	ListSchedules(ctx context.Context, userID string, limit int) ([]*LoopSchedule, error)
	ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]*LoopSchedule, error)
	ClaimDueSchedule(ctx context.Context, id string, now time.Time, nextRunAt *time.Time) (bool, error)
	UpdateScheduleStatus(ctx context.Context, id string, status ScheduleStatus) error

	CreateRun(ctx context.Context, run *LoopRun) error
	GetRun(ctx context.Context, id string) (*LoopRun, error)
	ListRuns(ctx context.Context, scheduleID string, limit int) ([]*LoopRun, error)
	CountRunsSince(ctx context.Context, scheduleID string, since time.Time) (int, error)
	UpdateRunParentTask(ctx context.Context, id, parentTaskID string) error
	UpdateRunStatus(ctx context.Context, id string, status RunStatus, resultSummary, nextAction, errText string) error
	UpdateRunFailure(ctx context.Context, id string, status RunStatus, errText string, diagnosis ErrorDiagnosis) error
	UpdateRunReport(ctx context.Context, id string, report RunReport) error
	UpdateScheduleNextRun(ctx context.Context, id string, nextRunAt *time.Time) error

	AppendEvent(ctx context.Context, event *LoopEvent) error
	ListEvents(ctx context.Context, runID string, limit int) ([]*LoopEvent, error)
}

type SQLiteStore struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*SQLiteStore, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	s := &SQLiteStore{db: db}
	if err := s.initSchema(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) CreateSchedule(ctx context.Context, schedule *LoopSchedule) error {
	if schedule == nil {
		return fmt.Errorf("schedule is nil")
	}
	if err := normalizeSchedule(schedule); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO loop_schedules(
			id, user_id, group_id, title, project_key, goal, schedule_expr, timezone,
			executor, safety_profile, status, max_runs_per_day, max_child_tasks,
			plan_json, last_run_at, next_run_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, schedule.ID, schedule.UserID, schedule.GroupID, schedule.Title, schedule.ProjectKey,
		schedule.Goal, schedule.ScheduleExpr, schedule.Timezone, schedule.Executor,
		string(schedule.SafetyProfile), string(schedule.Status), schedule.MaxRunsPerDay,
		schedule.MaxChildTasks, schedule.PlanJSON, timeValue(schedule.LastRunAt), timeValue(schedule.NextRunAt))
	if err != nil {
		return err
	}
	created, err := s.GetSchedule(ctx, schedule.ID)
	if err != nil {
		return err
	}
	*schedule = *created
	return nil
}

func (s *SQLiteStore) GetSchedule(ctx context.Context, id string) (*LoopSchedule, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, group_id, title, project_key, goal, schedule_expr, timezone,
			executor, safety_profile, status, max_runs_per_day, max_child_tasks,
			plan_json, last_run_at, next_run_at, created_at, updated_at
		FROM loop_schedules
		WHERE id = ?
	`, id)
	return scanSchedule(row)
}

func (s *SQLiteStore) ListSchedules(ctx context.Context, userID string, limit int) ([]*LoopSchedule, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows *sql.Rows
	var err error
	if strings.TrimSpace(userID) == "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, user_id, group_id, title, project_key, goal, schedule_expr, timezone,
				executor, safety_profile, status, max_runs_per_day, max_child_tasks,
				plan_json, last_run_at, next_run_at, created_at, updated_at
			FROM loop_schedules
			ORDER BY updated_at DESC, created_at DESC
			LIMIT ?
		`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, user_id, group_id, title, project_key, goal, schedule_expr, timezone,
				executor, safety_profile, status, max_runs_per_day, max_child_tasks,
				plan_json, last_run_at, next_run_at, created_at, updated_at
			FROM loop_schedules
			WHERE user_id = ?
			ORDER BY updated_at DESC, created_at DESC
			LIMIT ?
		`, userID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []*LoopSchedule
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func (s *SQLiteStore) ListDueSchedules(ctx context.Context, now time.Time, limit int) ([]*LoopSchedule, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, group_id, title, project_key, goal, schedule_expr, timezone,
			executor, safety_profile, status, max_runs_per_day, max_child_tasks,
			plan_json, last_run_at, next_run_at, created_at, updated_at
		FROM loop_schedules
		WHERE status = ? AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at ASC, updated_at ASC
		LIMIT ?
	`, string(ScheduleStatusActive), timeValue(&now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []*LoopSchedule
	for rows.Next() {
		schedule, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func (s *SQLiteStore) ClaimDueSchedule(ctx context.Context, id string, now time.Time, nextRunAt *time.Time) (bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, fmt.Errorf("schedule id is required")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE loop_schedules
		SET last_run_at = ?, next_run_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = ? AND next_run_at IS NOT NULL AND next_run_at <= ?
	`, timeValue(&now), timeValue(nextRunAt), id, string(ScheduleStatusActive), timeValue(&now))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *SQLiteStore) UpdateScheduleStatus(ctx context.Context, id string, status ScheduleStatus) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("schedule id is required")
	}
	if strings.TrimSpace(string(status)) == "" {
		return fmt.Errorf("schedule status is required")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE loop_schedules
		SET status = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, string(status), id)
	return err
}

func (s *SQLiteStore) CreateRun(ctx context.Context, run *LoopRun) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if err := normalizeRun(run); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO loop_runs(
			id, schedule_id, trigger_type, status, parent_task_id,
			result_summary, next_action, error, error_stage, error_kind, error_retryable,
			retry_count, next_retry_at, artifact_summary, diff_summary, log_summary,
			started_at, done_at
		)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (
			SELECT 1
			FROM loop_runs
			WHERE schedule_id = ? AND status IN (?, ?)
		)
	`, run.ID, run.ScheduleID, string(run.TriggerType), string(run.Status), run.ParentTaskID,
		run.ResultSummary, run.NextAction, run.Error, run.ErrorStage, run.ErrorKind, intFromBool(run.ErrorRetryable),
		run.RetryCount, timeValue(run.NextRetryAt), run.ArtifactSummary, run.DiffSummary, run.LogSummary,
		timeValue(run.StartedAt), timeValue(run.DoneAt),
		run.ScheduleID, string(RunStatusPending), string(RunStatusRunning))
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrActiveRun
	}
	created, err := s.GetRun(ctx, run.ID)
	if err != nil {
		return err
	}
	*run = *created
	return nil
}

func (s *SQLiteStore) GetRun(ctx context.Context, id string) (*LoopRun, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, schedule_id, trigger_type, status, parent_task_id,
			result_summary, next_action, error, error_stage, error_kind, error_retryable,
			retry_count, next_retry_at, artifact_summary, diff_summary, log_summary,
			started_at, done_at, created_at, updated_at
		FROM loop_runs
		WHERE id = ?
	`, id)
	return scanRun(row)
}

func (s *SQLiteStore) ListRuns(ctx context.Context, scheduleID string, limit int) ([]*LoopRun, error) {
	if strings.TrimSpace(scheduleID) == "" {
		return nil, fmt.Errorf("schedule id is required")
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, schedule_id, trigger_type, status, parent_task_id,
			result_summary, next_action, error, error_stage, error_kind, error_retryable,
			retry_count, next_retry_at, artifact_summary, diff_summary, log_summary,
			started_at, done_at, created_at, updated_at
		FROM loop_runs
		WHERE schedule_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, scheduleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*LoopRun
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) CountRunsSince(ctx context.Context, scheduleID string, since time.Time) (int, error) {
	if strings.TrimSpace(scheduleID) == "" {
		return 0, fmt.Errorf("schedule id is required")
	}
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM loop_runs
		WHERE schedule_id = ? AND created_at >= ?
	`, scheduleID, timeValue(&since)).Scan(&count)
	return count, err
}

func (s *SQLiteStore) UpdateRunParentTask(ctx context.Context, id, parentTaskID string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("run id is required")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE loop_runs
		SET parent_task_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, parentTaskID, id)
	return err
}

func (s *SQLiteStore) UpdateRunStatus(ctx context.Context, id string, status RunStatus, resultSummary, nextAction, errText string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("run id is required")
	}
	if strings.TrimSpace(string(status)) == "" {
		return fmt.Errorf("run status is required")
	}
	startedAtExpr := "started_at"
	doneAtExpr := "done_at"
	if status == RunStatusRunning {
		startedAtExpr = "COALESCE(started_at, CURRENT_TIMESTAMP)"
	}
	if isTerminalRunStatus(status) {
		doneAtExpr = "CURRENT_TIMESTAMP"
	}
	stmt := fmt.Sprintf(`
		UPDATE loop_runs
		SET status = ?, result_summary = ?, next_action = ?, error = ?,
			started_at = %s, done_at = %s, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, startedAtExpr, doneAtExpr)
	_, err := s.db.ExecContext(ctx, stmt, string(status), resultSummary, nextAction, errText, id)
	return err
}

func (s *SQLiteStore) UpdateRunFailure(ctx context.Context, id string, status RunStatus, errText string, diagnosis ErrorDiagnosis) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("run id is required")
	}
	if status == "" {
		status = RunStatusFailed
	}
	if strings.TrimSpace(diagnosis.Stage) == "" {
		diagnosis.Stage = "unknown"
	}
	if strings.TrimSpace(diagnosis.Kind) == "" {
		diagnosis.Kind = "unknown"
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE loop_runs
		SET status = ?, error = ?, error_stage = ?, error_kind = ?, error_retryable = ?,
			retry_count = ?, next_retry_at = ?, done_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, string(status), errText, diagnosis.Stage, diagnosis.Kind, intFromBool(diagnosis.Retryable),
		diagnosis.RetryCount, timeValue(diagnosis.NextRetry), id)
	return err
}

func (s *SQLiteStore) UpdateRunReport(ctx context.Context, id string, report RunReport) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("run id is required")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE loop_runs
		SET artifact_summary = ?, diff_summary = ?, log_summary = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, strings.TrimSpace(report.ArtifactSummary), strings.TrimSpace(report.DiffSummary), strings.TrimSpace(report.LogSummary), id)
	return err
}

func (s *SQLiteStore) UpdateScheduleNextRun(ctx context.Context, id string, nextRunAt *time.Time) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("schedule id is required")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE loop_schedules
		SET next_run_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, timeValue(nextRunAt), id)
	return err
}

func (s *SQLiteStore) AppendEvent(ctx context.Context, event *LoopEvent) error {
	if event == nil {
		return fmt.Errorf("event is nil")
	}
	if err := normalizeEvent(event); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO loop_events(run_id, schedule_id, task_id, event_type, level, message, meta_json)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, event.RunID, event.ScheduleID, event.TaskID, string(event.EventType), string(event.Level), event.Message, event.MetaJSON)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err == nil {
		event.ID = id
	}
	return nil
}

func (s *SQLiteStore) ListEvents(ctx context.Context, runID string, limit int) ([]*LoopEvent, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run id is required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, schedule_id, task_id, event_type, level, message, meta_json, created_at
		FROM (
			SELECT id, run_id, schedule_id, task_id, event_type, level, message, meta_json, created_at
			FROM loop_events
			WHERE run_id = ?
			ORDER BY id DESC
			LIMIT ?
		)
		ORDER BY id ASC
	`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*LoopEvent
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *SQLiteStore) initSchema() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS loop_schedules (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			group_id TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			project_key TEXT NOT NULL DEFAULT '',
			goal TEXT NOT NULL,
			schedule_expr TEXT NOT NULL,
			timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
			executor TEXT NOT NULL DEFAULT '',
			safety_profile TEXT NOT NULL DEFAULT 'conservative',
			status TEXT NOT NULL DEFAULT 'active',
			max_runs_per_day INTEGER NOT NULL DEFAULT 100,
			max_child_tasks INTEGER NOT NULL DEFAULT 3,
			plan_json TEXT NOT NULL DEFAULT '',
			last_run_at DATETIME,
			next_run_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_loop_schedules_user_status_next_run
			ON loop_schedules(user_id, status, next_run_at);

		CREATE TABLE IF NOT EXISTS loop_runs (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL,
			trigger_type TEXT NOT NULL DEFAULT 'scheduled',
			status TEXT NOT NULL DEFAULT 'pending',
			parent_task_id TEXT NOT NULL DEFAULT '',
			result_summary TEXT NOT NULL DEFAULT '',
			next_action TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			error_stage TEXT NOT NULL DEFAULT '',
			error_kind TEXT NOT NULL DEFAULT '',
			error_retryable INTEGER NOT NULL DEFAULT 0,
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at DATETIME,
			artifact_summary TEXT NOT NULL DEFAULT '',
			diff_summary TEXT NOT NULL DEFAULT '',
			log_summary TEXT NOT NULL DEFAULT '',
			started_at DATETIME,
			done_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_loop_runs_schedule_created
			ON loop_runs(schedule_id, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_loop_runs_schedule_status
			ON loop_runs(schedule_id, status);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_loop_runs_one_active_per_schedule
			ON loop_runs(schedule_id)
			WHERE status IN ('pending', 'running');

		CREATE TABLE IF NOT EXISTS loop_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			schedule_id TEXT NOT NULL,
			task_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL,
			level TEXT NOT NULL DEFAULT 'info',
			message TEXT NOT NULL,
			meta_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_loop_events_run_created
			ON loop_events(run_id, id);
			CREATE INDEX IF NOT EXISTS idx_loop_events_schedule_created
				ON loop_events(schedule_id, id);
		`)
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		"ALTER TABLE loop_runs ADD COLUMN error_stage TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE loop_runs ADD COLUMN error_kind TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE loop_runs ADD COLUMN error_retryable INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE loop_runs ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE loop_runs ADD COLUMN next_retry_at DATETIME",
		"ALTER TABLE loop_runs ADD COLUMN artifact_summary TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE loop_runs ADD COLUMN diff_summary TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE loop_runs ADD COLUMN log_summary TEXT NOT NULL DEFAULT ''",
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("alter loop_runs: %w", err)
		}
	}
	return nil
}

func normalizeSchedule(schedule *LoopSchedule) error {
	schedule.UserID = strings.TrimSpace(schedule.UserID)
	schedule.GroupID = strings.TrimSpace(schedule.GroupID)
	schedule.Title = strings.TrimSpace(schedule.Title)
	schedule.ProjectKey = strings.TrimSpace(schedule.ProjectKey)
	schedule.Goal = strings.TrimSpace(schedule.Goal)
	schedule.ScheduleExpr = strings.TrimSpace(schedule.ScheduleExpr)
	schedule.Timezone = strings.TrimSpace(schedule.Timezone)
	schedule.Executor = strings.TrimSpace(schedule.Executor)
	schedule.PlanJSON = strings.TrimSpace(schedule.PlanJSON)
	if schedule.UserID == "" {
		return fmt.Errorf("user id is required")
	}
	if schedule.Title == "" {
		return fmt.Errorf("title is required")
	}
	if schedule.Goal == "" {
		return fmt.Errorf("goal is required")
	}
	if schedule.ScheduleExpr == "" {
		return fmt.Errorf("schedule expr is required")
	}
	if schedule.ID == "" {
		id, err := newID("loop")
		if err != nil {
			return err
		}
		schedule.ID = id
	}
	if schedule.Timezone == "" {
		schedule.Timezone = "Asia/Shanghai"
	}
	if schedule.SafetyProfile == "" {
		schedule.SafetyProfile = SafetyProfileConservative
	}
	if schedule.Status == "" {
		schedule.Status = ScheduleStatusActive
	}
	if schedule.MaxRunsPerDay <= 0 {
		schedule.MaxRunsPerDay = 100
	}
	if schedule.MaxChildTasks <= 0 {
		schedule.MaxChildTasks = 3
	}
	return nil
}

func normalizeRun(run *LoopRun) error {
	run.ScheduleID = strings.TrimSpace(run.ScheduleID)
	run.ParentTaskID = strings.TrimSpace(run.ParentTaskID)
	run.ResultSummary = strings.TrimSpace(run.ResultSummary)
	run.NextAction = strings.TrimSpace(run.NextAction)
	run.Error = strings.TrimSpace(run.Error)
	run.ErrorStage = strings.TrimSpace(run.ErrorStage)
	run.ErrorKind = strings.TrimSpace(run.ErrorKind)
	run.ArtifactSummary = strings.TrimSpace(run.ArtifactSummary)
	run.DiffSummary = strings.TrimSpace(run.DiffSummary)
	run.LogSummary = strings.TrimSpace(run.LogSummary)
	if run.ScheduleID == "" {
		return fmt.Errorf("schedule id is required")
	}
	if run.ID == "" {
		id, err := newID("run")
		if err != nil {
			return err
		}
		run.ID = id
	}
	if run.TriggerType == "" {
		run.TriggerType = TriggerTypeScheduled
	}
	if run.Status == "" {
		run.Status = RunStatusPending
	}
	return nil
}

func normalizeEvent(event *LoopEvent) error {
	event.RunID = strings.TrimSpace(event.RunID)
	event.ScheduleID = strings.TrimSpace(event.ScheduleID)
	event.TaskID = strings.TrimSpace(event.TaskID)
	event.Message = strings.TrimSpace(event.Message)
	event.MetaJSON = strings.TrimSpace(event.MetaJSON)
	if event.RunID == "" {
		return fmt.Errorf("run id is required")
	}
	if event.ScheduleID == "" {
		return fmt.Errorf("schedule id is required")
	}
	if event.EventType == "" {
		return fmt.Errorf("event type is required")
	}
	if event.Message == "" {
		return fmt.Errorf("message is required")
	}
	if event.Level == "" {
		event.Level = EventLevelInfo
	}
	return nil
}

func scanSchedule(scanner interface{ Scan(dest ...any) error }) (*LoopSchedule, error) {
	var schedule LoopSchedule
	var safetyProfile, status string
	var lastRunAt, nextRunAt, createdAt, updatedAt sql.NullString
	if err := scanner.Scan(
		&schedule.ID, &schedule.UserID, &schedule.GroupID, &schedule.Title, &schedule.ProjectKey,
		&schedule.Goal, &schedule.ScheduleExpr, &schedule.Timezone, &schedule.Executor,
		&safetyProfile, &status, &schedule.MaxRunsPerDay, &schedule.MaxChildTasks,
		&schedule.PlanJSON, &lastRunAt, &nextRunAt, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	schedule.SafetyProfile = SafetyProfile(safetyProfile)
	schedule.Status = ScheduleStatus(status)
	schedule.LastRunAt = parseNullTime(lastRunAt)
	schedule.NextRunAt = parseNullTime(nextRunAt)
	schedule.CreatedAt = parseTime(createdAt)
	schedule.UpdatedAt = parseTime(updatedAt)
	return &schedule, nil
}

func scanRun(scanner interface{ Scan(dest ...any) error }) (*LoopRun, error) {
	var run LoopRun
	var triggerType, status string
	var errorRetryable int
	var startedAt, doneAt, createdAt, updatedAt, nextRetryAt sql.NullString
	if err := scanner.Scan(
		&run.ID, &run.ScheduleID, &triggerType, &status, &run.ParentTaskID,
		&run.ResultSummary, &run.NextAction, &run.Error, &run.ErrorStage, &run.ErrorKind, &errorRetryable,
		&run.RetryCount, &nextRetryAt, &run.ArtifactSummary, &run.DiffSummary, &run.LogSummary,
		&startedAt, &doneAt, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	run.TriggerType = TriggerType(triggerType)
	run.Status = RunStatus(status)
	run.ErrorRetryable = errorRetryable != 0
	run.NextRetryAt = parseNullTime(nextRetryAt)
	run.StartedAt = parseNullTime(startedAt)
	run.DoneAt = parseNullTime(doneAt)
	run.CreatedAt = parseTime(createdAt)
	run.UpdatedAt = parseTime(updatedAt)
	return &run, nil
}

func scanEvent(scanner interface{ Scan(dest ...any) error }) (*LoopEvent, error) {
	var event LoopEvent
	var eventType, level string
	var createdAt sql.NullString
	if err := scanner.Scan(
		&event.ID, &event.RunID, &event.ScheduleID, &event.TaskID,
		&eventType, &level, &event.Message, &event.MetaJSON, &createdAt,
	); err != nil {
		return nil, err
	}
	event.EventType = EventType(eventType)
	event.Level = EventLevel(level)
	event.CreatedAt = parseTime(createdAt)
	return &event, nil
}

func isTerminalRunStatus(status RunStatus) bool {
	return status == RunStatusDone || status == RunStatusFailed || status == RunStatusCancelled
}

func intFromBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

func timeValue(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func parseNullTime(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	t := parseTime(value)
	return &t
}

func parseTime(value sql.NullString) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	raw := strings.TrimSpace(value.String)
	if raw == "" {
		return time.Time{}
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func newID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(buf), nil
}
