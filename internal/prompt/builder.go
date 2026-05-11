package prompt

import (
	"fmt"
	"strings"
)

// BuildInput 传入 Build 方法的参数
type BuildInput struct {
	Task            TaskInfo
	Scratchpad      string // 短期工作记忆（scratchpad 内容）
	ProjectState    string // 项目状态快照（project_key 对应的 .md 内容摘要）
	PrevTaskSummary string // 上一个相关 task 的 Summary
	IsResume        bool   // true = CLI session resume 成功，false = 需要全量重建
}

// TaskInfo 任务基本信息（避免循环依赖，不直接引用 taskqueue.Task）
type TaskInfo struct {
	ID         string
	Type       string
	Title      string
	Goal       string
	Content    string
	ProjectKey string
	Executor   string
}

// Builder 组装 system prompt
type Builder interface {
	Build(input BuildInput) string
}

type defaultBuilder struct {
	basePrompt string // 静态基础 prompt（coding 规范、安全规则等）
}

// New 创建 PromptBuilder，basePrompt 为可选的静态约束层
func New(basePrompt string) Builder {
	return &defaultBuilder{basePrompt: basePrompt}
}

// Build 根据 isResume 选择轻量注入或全量重建
func (b *defaultBuilder) Build(input BuildInput) (result string) {
	defer func() {
		if r := recover(); r != nil {
			result = fmt.Sprintf("[PromptBuilder panic recovered: %v]\n\n%s", r, input.Task.Content)
		}
	}()

	if input.IsResume {
		return b.buildLightweight(input)
	}
	return b.buildFull(input)
}

// buildLightweight resume 成功时的轻量注入（增量 diff：新指令 + 状态变更）
func (b *defaultBuilder) buildLightweight(input BuildInput) string {
	var parts []string

	// 约束层（静态 prompt）
	if b.basePrompt != "" {
		parts = append(parts, b.basePrompt)
	}

	// 新指令层：task 目标 + 内容
	taskSection := b.buildTaskSection(input.Task)
	if taskSection != "" {
		parts = append(parts, taskSection)
	}

	// 状态变更层：只注入 scratchpad 变更（如有）
	if input.Scratchpad != "" {
		parts = append(parts, "## 当前工作状态\n"+input.Scratchpad)
	}

	return strings.Join(parts, "\n\n")
}

// buildFull resume 失败时的全量重建（scratchpad + project_state + 上个 task summary）
func (b *defaultBuilder) buildFull(input BuildInput) string {
	var parts []string

	// 约束层（静态 prompt）
	if b.basePrompt != "" {
		parts = append(parts, b.basePrompt)
	}

	// 基础层：task 目标 + 内容
	taskSection := b.buildTaskSection(input.Task)
	if taskSection != "" {
		parts = append(parts, taskSection)
	}

	// 项目层：project_state 快照
	if input.ProjectState != "" {
		parts = append(parts, "## 项目状态\n"+input.ProjectState)
	}

	// 记忆层：scratchpad 内容
	if input.Scratchpad != "" {
		parts = append(parts, "## 工作记忆（Scratchpad）\n"+input.Scratchpad)
	}

	// 历史层：上一个相关 task 的 Summary
	if input.PrevTaskSummary != "" {
		parts = append(parts, "## 上一任务摘要\n"+input.PrevTaskSummary)
	}

	return strings.Join(parts, "\n\n")
}

// buildTaskSection 组装 task 目标描述区块
func (b *defaultBuilder) buildTaskSection(t TaskInfo) string {
	var lines []string
	if t.Title != "" {
		lines = append(lines, "## 当前任务："+t.Title)
	} else {
		lines = append(lines, "## 当前任务")
	}
	if t.Goal != "" {
		lines = append(lines, "**目标**："+t.Goal)
	}
	if t.Content != "" {
		lines = append(lines, "**指令**："+t.Content)
	}
	if t.ProjectKey != "" {
		lines = append(lines, "**项目**："+t.ProjectKey)
	}
	if len(lines) == 1 {
		// 只有标题，无实质内容，返回空
		return ""
	}
	return strings.Join(lines, "\n")
}
