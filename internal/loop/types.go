package loop

import "time"

type ScheduleStatus string

const (
	ScheduleStatusActive ScheduleStatus = "active"
	ScheduleStatusPaused ScheduleStatus = "paused"
)

type SafetyProfile string

const (
	SafetyProfileObserveOnly    SafetyProfile = "observe_only"
	SafetyProfileConservative   SafetyProfile = "conservative"
	SafetyProfileManualApproval SafetyProfile = "manual_approval"
)

type TriggerType string

const (
	TriggerTypeScheduled TriggerType = "scheduled"
	TriggerTypeManual    TriggerType = "manual"
	TriggerTypeTaskDone  TriggerType = "task_done"
)

type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusDone      RunStatus = "done"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
)

type EventLevel string

const (
	EventLevelInfo  EventLevel = "info"
	EventLevelWarn  EventLevel = "warn"
	EventLevelError EventLevel = "error"
)

type EventType string

const (
	EventTypeCreated             EventType = "created"
	EventTypeDueClaimed          EventType = "due_claimed"
	EventTypePlanningStarted     EventType = "planning_started"
	EventTypeParentTaskSubmitted EventType = "parent_task_submitted"
	EventTypeChildTaskSubmitted  EventType = "child_task_submitted"
	EventTypeProgress            EventType = "progress"
	EventTypeReviewRequested     EventType = "review_requested"
	EventTypeCompleted           EventType = "completed"
	EventTypeFailed              EventType = "failed"
	EventTypePaused              EventType = "paused"
	EventTypeTaskDoneTriggered   EventType = "task_done_triggered"
	EventTypeApprovalRequired    EventType = "approval_required"
)

type LoopSchedule struct {
	ID            string         `json:"id"`
	UserID        string         `json:"user_id"`
	GroupID       string         `json:"group_id,omitempty"`
	Title         string         `json:"title"`
	ProjectKey    string         `json:"project_key,omitempty"`
	Goal          string         `json:"goal"`
	ScheduleExpr  string         `json:"schedule_expr"`
	Timezone      string         `json:"timezone"`
	Executor      string         `json:"executor,omitempty"`
	SafetyProfile SafetyProfile  `json:"safety_profile"`
	Status        ScheduleStatus `json:"status"`
	MaxRunsPerDay int            `json:"max_runs_per_day"`
	MaxChildTasks int            `json:"max_child_tasks"`
	PlanJSON      string         `json:"plan_json,omitempty"`
	LastRunAt     *time.Time     `json:"last_run_at,omitempty"`
	NextRunAt     *time.Time     `json:"next_run_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type LoopRun struct {
	ID              string      `json:"id"`
	ScheduleID      string      `json:"schedule_id"`
	TriggerType     TriggerType `json:"trigger_type"`
	Status          RunStatus   `json:"status"`
	ParentTaskID    string      `json:"parent_task_id,omitempty"`
	ResultSummary   string      `json:"result_summary,omitempty"`
	NextAction      string      `json:"next_action,omitempty"`
	Error           string      `json:"error,omitempty"`
	ErrorStage      string      `json:"error_stage,omitempty"`
	ErrorKind       string      `json:"error_kind,omitempty"`
	ErrorRetryable  bool        `json:"error_retryable,omitempty"`
	RetryCount      int         `json:"retry_count,omitempty"`
	NextRetryAt     *time.Time  `json:"next_retry_at,omitempty"`
	ArtifactSummary string      `json:"artifact_summary,omitempty"`
	DiffSummary     string      `json:"diff_summary,omitempty"`
	LogSummary      string      `json:"log_summary,omitempty"`
	StartedAt       *time.Time  `json:"started_at,omitempty"`
	DoneAt          *time.Time  `json:"done_at,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

type ErrorDiagnosis struct {
	Stage      string
	Kind       string
	Retryable  bool
	RetryCount int
	NextRetry  *time.Time
}

type RunReport struct {
	ArtifactSummary string
	DiffSummary     string
	LogSummary      string
}

type LoopEvent struct {
	ID         int64      `json:"id"`
	RunID      string     `json:"run_id"`
	ScheduleID string     `json:"schedule_id"`
	TaskID     string     `json:"task_id,omitempty"`
	EventType  EventType  `json:"event_type"`
	Level      EventLevel `json:"level"`
	Message    string     `json:"message"`
	MetaJSON   string     `json:"meta_json,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type LoopPlan struct {
	Title               string       `json:"title"`
	ProjectKey          string       `json:"project_key"`
	Objective           string       `json:"objective"`
	Schedule            PlanSchedule `json:"schedule"`
	SuccessCriteria     []string     `json:"success_criteria"`
	Checklist           []string     `json:"checklist"`
	AllowedActions      []string     `json:"allowed_actions"`
	ApprovalRequiredFor []string     `json:"approval_required_for"`
	Notify              PlanNotify   `json:"notify"`
}

type PlanSchedule struct {
	Kind              string `json:"kind"`
	Time              string `json:"time"`
	IntervalMinutes   int    `json:"interval_minutes"`
	Weekday           string `json:"weekday"`
	Timezone          string `json:"timezone"`
	TriggerOnTaskDone bool   `json:"trigger_on_task_done"`
}

type PlanNotify struct {
	OnStart            bool `json:"on_start"`
	OnDone             bool `json:"on_done"`
	OnError            bool `json:"on_error"`
	OnApprovalRequired bool `json:"on_approval_required"`
}
