package taskqueue

import "time"

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// TaskType 任务类型
type TaskType string

const (
	TaskTypeChat     TaskType = "chat"
	TaskTypeProject  TaskType = "project"
	TaskTypeReview   TaskType = "review"
	TaskTypeResearch TaskType = "research"
	TaskTypeCommand  TaskType = "command"
)

type Task struct {
	// --- 已有字段 ---
	ID              string     `json:"id"`
	UserID          string     `json:"user_id"`
	GroupID         string     `json:"group_id"`
	Content         string     `json:"content"`
	Status          Status     `json:"status"`
	Result          string     `json:"result"`
	Error           string     `json:"error"`
	SessionID       string     `json:"session_id"`
	ContinueSession bool       `json:"continue_session"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	DoneAt          *time.Time `json:"done_at,omitempty"`

	// --- 新增字段 (Sprint 1.1) ---
	Type         TaskType `json:"type,omitempty"`          // chat / project / review / research / command
	Title        string   `json:"title,omitempty"`         // 简短标题
	Goal         string   `json:"goal,omitempty"`          // 任务目标描述
	ProjectKey   string   `json:"project_key,omitempty"`   // 关联项目标识
	Executor     string   `json:"executor,omitempty"`      // claude_code / codex / local
	VerifyStatus string   `json:"verify_status,omitempty"` // none / pending / passed / failed
	ParentTaskID string   `json:"parent_task_id,omitempty"`
	Summary      string   `json:"summary,omitempty"`   // 结果摘要
	MetaJSON     string   `json:"meta_json,omitempty"` // 扩展元数据 JSON blob

	// --- 新增字段 (Sprint 1.2) ---
	ExecutorSessionID string `json:"executor_session_id,omitempty"` // CLI 原生 session ID
}

// CompletionResult 任务完成时传递给 CompletionFn 的结果
type CompletionResult struct {
	Task   *Task
	Result string
	Error  error
}

type SubmitRequest struct {
	UserID          string
	GroupID         string
	Content         string
	ContinueSession bool
	ProgressFn      func(string)
	CompletionFn    func(CompletionResult) // 任务完成后的回调

	// --- 新增字段 (Sprint 1.1) ---
	Type       TaskType
	Title      string
	Goal       string
	ProjectKey string
	Executor   string
}
