package loop

import (
	"fmt"
	"strings"
)

func BuildParentTaskPrompt(schedule *LoopSchedule, brainContent ...string) string {
	if schedule == nil {
		return ""
	}
	brain := ""
	if len(brainContent) > 0 {
		brain = TrimBrainForPrompt(brainContent[0])
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "你是 myself-ai Loop Run Controller。\n\n")
	fmt.Fprintf(&sb, "本轮目标：\n%s\n\n", schedule.Goal)
	fmt.Fprintf(&sb, "Loop 信息：\n")
	fmt.Fprintf(&sb, "- loop_id: %s\n", schedule.ID)
	fmt.Fprintf(&sb, "- title: %s\n", schedule.Title)
	fmt.Fprintf(&sb, "- project_key: %s\n", empty(schedule.ProjectKey, "none"))
	fmt.Fprintf(&sb, "- schedule: %s\n", schedule.ScheduleExpr)
	fmt.Fprintf(&sb, "- safety_profile: %s\n", schedule.SafetyProfile)
	fmt.Fprintf(&sb, "- max_child_tasks: %d\n\n", schedule.MaxChildTasks)
	fmt.Fprintf(&sb, "- task_done_trigger: %t\n", HasTaskDoneTrigger(schedule))
	fmt.Fprintf(&sb, "- max_runs_per_day: %d\n\n", schedule.MaxRunsPerDay)
	if strings.TrimSpace(schedule.PlanJSON) != "" {
		fmt.Fprintf(&sb, "Loop Plan JSON：\n```json\n%s\n```\n\n", schedule.PlanJSON)
	}
	if strings.TrimSpace(brain) != "" {
		fmt.Fprintf(&sb, "Loop Brain File 当前内容：\n```md\n%s\n```\n\n", brain)
	}
	sb.WriteString(`你要完成：
1. 根据 Loop Plan 判断本轮应该观察、检查、执行还是要求人工确认。
2. 只选择一个最小、低风险、可验证的推进事项。
3. 如果需要后续子任务，请给出清晰的 child task 说明和验收标准。
4. 如果触及删除文件、安装依赖、修改凭据、重启服务、部署发布，必须输出 approval_required。
5. 如果 Brain 需要更新，输出一个完整的 brain.md fenced code block；不需要更新则不要输出该代码块。

产物冲突协议：
- 写任何编号产物前，必须先重新读取权威状态文件并列出目标目录；例如章节类产物先读 progress.md / continuity.md，再列 chapters/。
- 对 NNNN-* 这类编号文件，同一编号已经存在时，禁止换标题另存一份；必须停止写入，Decision 输出 approval_required，Loop Control 输出 wait，并在 Artifacts / Logs 写清已有文件路径。
- 写完关键产物后，必须再次读取权威状态文件和目标目录。如果发现其他 run 已经推进同一编号或同一任务，本轮不得把自己的产物标成 canonical，不得继续输出 continue；应在 Artifacts 标注冲突草稿路径，在 Summary 说明等待用户裁决。
- 只有确认没有编号冲突、状态文件已同步、目录中没有重复编号时，才允许输出 continue。

主动汇报协议：
- Runtime 已接入 Feishu 主动通知；你没有直接调用 Feishu API 的工具。
- 如果需要让用户看到进度，直接输出简短进度文本即可，Runtime 会把 executor progress 转发到创建该 loop 的飞书会话。
- 长任务在开始关键检查、完成关键阶段、遇到阻塞、需要人工审批、准备结束本轮时，都应主动给 1-3 行进度。
- 不要刷屏；除关键状态变化外，约 1-2 分钟最多汇报一次即可。
- 需要人工审批时，不要继续执行高风险动作；在 Decision 输出 approval_required，在 Loop Control 输出 wait，并在 Summary 写清楚需要用户批准什么。
- 你不能中断或抢占自己正在运行的任务；如需暂停等待用户，必须结束当前 run 并输出 wait/pause/done。

输出严格使用以下 Markdown 区块：

## Decision
observe_only | create_check_task | create_execute_task | approval_required | no_op

## Reason
简述原因。

## Child Task
如果需要创建子任务，写清楚 title、type、executor、goal、验收标准。
如果不需要，写 none。

## Safety
说明是否触及 approval_required_for。

## Artifacts
列出本轮产生或更新的文件、任务、笔记、brain 路径等；没有则写 none。

## Diff
列出关键代码/文档变更摘要，能贴小段 diff 就贴小段；没有变更则写 none。

## Logs
列出执行过的命令、验证结果、错误日志、阻塞点；没有则写 none。

## Loop Control
continue | wait | done | pause

规则：
- 只有 task_done_trigger=true，且存在明确、低风险、可验证的下一步时，才输出 continue。
- 目标已经完成时输出 done。
- 需要人工确认、缺少输入、触及高风险操作或应等待下次时间触发时输出 wait。
- 如需自主调整节奏，可写 wait 30m、wait 2h，或增加 next_run_at: 2026-05-10 16:00:00。
- 应暂停整个 loop 时输出 pause。

## Summary
本轮给用户看的 3-5 行摘要。
`)
	sb.WriteString("\n如果你输出 brain.md，必须是完整文件内容，例如：\n")
	sb.WriteString("```brain.md\n")
	sb.WriteString(`# 标题

loop_id: ...

## Objective
...

## Current State
...

## Inbox
...

## Decisions
...

## Next
...

## Recent Runs
...
`)
	sb.WriteString("```\n")
	return sb.String()
}

func empty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
