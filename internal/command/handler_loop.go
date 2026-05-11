package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/1344011985/MyselfAI/internal/loop"
	"github.com/1344011985/MyselfAI/internal/memory"
)

type loopHandler struct {
	store  loop.Store
	runner *loop.Runner
	memory memory.Store
}

var dailyTimePattern = regexp.MustCompile(`(?i)(?:每天|daily).{0,12}?(\d{1,2})(?:[:：点](\d{1,2})?)?`)
var intervalPattern = regexp.MustCompile(`(?i)(?:每|every)\s*(\d{1,4})\s*(分钟|分|minute|minutes|min|m|小时|钟头|hour|hours|hr|hrs|h)`)
var everyMinutePattern = regexp.MustCompile(`(?i)(?:每分钟|every\s+minute)`)
var taskDonePattern = regexp.MustCompile(`(?i)(task[_ -]?done|任务完成|完成后继续|跑完继续|自我迭代|持续推进|继续下一步)`)

func (h *loopHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	if h.store == nil {
		return "Loop Runtime 尚未初始化。", nil
	}
	parts := strings.Fields(strings.TrimSpace(msg.Content))
	if len(parts) < 2 {
		return loopUsage(), nil
	}

	switch strings.ToLower(parts[1]) {
	case "create":
		goal := strings.TrimSpace(msg.Content)
		goal = strings.TrimSpace(strings.TrimPrefix(goal, parts[0]))
		goal = strings.TrimSpace(strings.TrimPrefix(goal, parts[1]))
		return h.create(ctx, msg, goal)
	case "list":
		return h.list(ctx, msg)
	case "status":
		if len(parts) < 3 {
			return "用法：/loop status <loop_id>", nil
		}
		return h.status(ctx, msg, parts[2])
	case "runs":
		if len(parts) < 3 {
			return "用法：/loop runs <loop_id> [limit]", nil
		}
		limit := 8
		if len(parts) >= 4 {
			if parsed, err := strconv.Atoi(parts[3]); err == nil && parsed > 0 && parsed <= 30 {
				limit = parsed
			}
		}
		return h.runs(ctx, msg, parts[2], limit)
	case "runlog":
		if len(parts) < 3 {
			return "用法：/loop runlog <run_id>", nil
		}
		return h.runLog(ctx, msg, parts[2])
	case "pause":
		if len(parts) < 3 {
			return "用法：/loop pause <loop_id>", nil
		}
		return h.updateStatus(ctx, msg, parts[2], loop.ScheduleStatusPaused)
	case "resume":
		if len(parts) < 3 {
			return "用法：/loop resume <loop_id>", nil
		}
		return h.updateStatus(ctx, msg, parts[2], loop.ScheduleStatusActive)
	case "run":
		if len(parts) < 3 {
			return "用法：/loop run <loop_id>", nil
		}
		return h.run(ctx, msg, parts[2])
	default:
		return loopUsage(), nil
	}
}

func (h *loopHandler) create(ctx context.Context, msg *IncomingMessage, goal string) (string, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return "用法：/loop create <目标>", nil
	}

	plan := buildRuleBasedPlan(goal)
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	executor := inferExecutor(goal)
	if executor == "" && h.memory != nil {
		if pref, err := h.memory.GetExecutorPreference(msg.UserID); err == nil {
			executor = strings.TrimSpace(pref)
		}
	}
	schedule := &loop.LoopSchedule{
		UserID:        msg.UserID,
		GroupID:       msg.GroupID,
		Title:         plan.Title,
		ProjectKey:    plan.ProjectKey,
		Goal:          plan.Objective,
		ScheduleExpr:  scheduleExpr(plan.Schedule),
		Timezone:      plan.Schedule.Timezone,
		Executor:      executor,
		SafetyProfile: loop.SafetyProfileConservative,
		PlanJSON:      string(planJSON),
		NextRunAt:     nextRunAt(plan.Schedule),
	}
	if err := h.store.CreateSchedule(ctx, schedule); err != nil {
		return "创建 loop 失败：" + err.Error(), nil
	}

	return fmt.Sprintf("已创建 loop：%s\n标题：%s\n计划：%s\n安全策略：%s",
		schedule.ID, schedule.Title, schedule.ScheduleExpr, schedule.SafetyProfile), nil
}

func (h *loopHandler) list(ctx context.Context, msg *IncomingMessage) (string, error) {
	schedules, err := h.store.ListSchedules(ctx, msg.UserID, 20)
	if err != nil {
		return "读取 loop 列表失败：" + err.Error(), nil
	}
	if len(schedules) == 0 {
		return "当前没有 loop。", nil
	}
	var sb strings.Builder
	for _, schedule := range schedules {
		fmt.Fprintf(&sb, "%s  %s  %s  %s\n", schedule.ID, schedule.Status, schedule.ScheduleExpr, shorten(schedule.Title, 32))
	}
	return strings.TrimSpace(sb.String()), nil
}

func (h *loopHandler) status(ctx context.Context, msg *IncomingMessage, id string) (string, error) {
	schedule, err := h.store.GetSchedule(ctx, id)
	if err != nil {
		return "loop 不存在或读取失败。", nil
	}
	if schedule.UserID != msg.UserID {
		return "只能查看你自己的 loop。", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Loop：%s\n", schedule.ID)
	fmt.Fprintf(&sb, "标题：%s\n", schedule.Title)
	fmt.Fprintf(&sb, "状态：%s\n", schedule.Status)
	fmt.Fprintf(&sb, "计划：%s\n", schedule.ScheduleExpr)
	fmt.Fprintf(&sb, "项目：%s\n", emptyAsDash(schedule.ProjectKey))
	fmt.Fprintf(&sb, "执行器：%s\n", emptyAsDash(schedule.Executor))
	fmt.Fprintf(&sb, "安全策略：%s\n", schedule.SafetyProfile)
	if schedule.NextRunAt != nil {
		fmt.Fprintf(&sb, "下次运行：%s\n", formatScheduleTime(*schedule.NextRunAt, schedule.Timezone))
	}
	fmt.Fprintf(&sb, "目标：%s", schedule.Goal)
	return sb.String(), nil
}

func (h *loopHandler) runs(ctx context.Context, msg *IncomingMessage, id string, limit int) (string, error) {
	schedule, err := h.store.GetSchedule(ctx, id)
	if err != nil {
		return "loop 不存在或读取失败。", nil
	}
	if schedule.UserID != msg.UserID {
		return "只能查看你自己的 loop。", nil
	}
	runs, err := h.store.ListRuns(ctx, id, limit)
	if err != nil {
		return "读取 loop run 失败：" + err.Error(), nil
	}
	if len(runs) == 0 {
		return "这个 loop 还没有 run 记录。", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Loop Runs：%s  %s\n", schedule.ID, schedule.Title)
	for _, run := range runs {
		fmt.Fprintf(&sb, "\n%s  %s  %s", run.ID, run.Status, run.TriggerType)
		if run.ParentTaskID != "" {
			fmt.Fprintf(&sb, "  task=%s", run.ParentTaskID)
		}
		if run.NextAction != "" {
			fmt.Fprintf(&sb, "  action=%s", run.NextAction)
		}
		if run.StartedAt != nil && run.DoneAt != nil {
			fmt.Fprintf(&sb, "  cost=%s", run.DoneAt.Sub(*run.StartedAt).Round(time.Second))
		}
		fmt.Fprintf(&sb, "\n时间：%s", formatScheduleTime(run.CreatedAt, schedule.Timezone))
		if run.Error != "" {
			fmt.Fprintf(&sb, "\n错误：%s/%s retry=%t count=%d", emptyAsDash(run.ErrorStage), emptyAsDash(run.ErrorKind), run.ErrorRetryable, run.RetryCount)
			if run.NextRetryAt != nil {
				fmt.Fprintf(&sb, " next=%s", formatScheduleTime(*run.NextRetryAt, schedule.Timezone))
			}
			fmt.Fprintf(&sb, "\n%s", shorten(run.Error, 180))
		} else if run.ResultSummary != "" {
			fmt.Fprintf(&sb, "\n摘要：%s", shorten(run.ResultSummary, 180))
		}
		if run.ArtifactSummary != "" {
			fmt.Fprintf(&sb, "\n产物：%s", shorten(run.ArtifactSummary, 160))
		}
		if run.DiffSummary != "" {
			fmt.Fprintf(&sb, "\nDiff：%s", shorten(run.DiffSummary, 160))
		}
		fmt.Fprintf(&sb, "\n查看详情：/loop runlog %s\n", run.ID)
	}
	return strings.TrimSpace(sb.String()), nil
}

func (h *loopHandler) runLog(ctx context.Context, msg *IncomingMessage, runID string) (string, error) {
	run, err := h.store.GetRun(ctx, runID)
	if err != nil {
		return "run 不存在或读取失败。", nil
	}
	schedule, err := h.store.GetSchedule(ctx, run.ScheduleID)
	if err != nil {
		return "run 所属 loop 不存在或读取失败。", nil
	}
	if schedule.UserID != msg.UserID {
		return "只能查看你自己的 loop run。", nil
	}
	events, _ := h.store.ListEvents(ctx, runID, 30)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Run：%s\nLoop：%s  %s\n", run.ID, schedule.ID, schedule.Title)
	fmt.Fprintf(&sb, "状态：%s  触发：%s\n", run.Status, run.TriggerType)
	if run.ParentTaskID != "" {
		fmt.Fprintf(&sb, "Parent Task：%s\n", run.ParentTaskID)
	}
	fmt.Fprintf(&sb, "创建：%s\n", formatScheduleTime(run.CreatedAt, schedule.Timezone))
	if run.StartedAt != nil {
		fmt.Fprintf(&sb, "开始：%s\n", formatScheduleTime(*run.StartedAt, schedule.Timezone))
	}
	if run.DoneAt != nil {
		fmt.Fprintf(&sb, "结束：%s\n", formatScheduleTime(*run.DoneAt, schedule.Timezone))
	}
	if run.StartedAt != nil && run.DoneAt != nil {
		fmt.Fprintf(&sb, "耗时：%s\n", run.DoneAt.Sub(*run.StartedAt).Round(time.Second))
	}
	if run.Error != "" {
		fmt.Fprintf(&sb, "\n## 错误归因\nstage：%s\nkind：%s\nretryable：%t\nretry_count：%d\n",
			emptyAsDash(run.ErrorStage), emptyAsDash(run.ErrorKind), run.ErrorRetryable, run.RetryCount)
		if run.NextRetryAt != nil {
			fmt.Fprintf(&sb, "next_retry_at：%s\n", formatScheduleTime(*run.NextRetryAt, schedule.Timezone))
		}
		fmt.Fprintf(&sb, "error：%s\n", trimForLoopReply(run.Error, 1200))
	}
	appendRunSection(&sb, "产物", run.ArtifactSummary)
	appendRunSection(&sb, "Diff", run.DiffSummary)
	appendRunSection(&sb, "日志", run.LogSummary)
	if run.ResultSummary != "" {
		appendRunSection(&sb, "摘要", run.ResultSummary)
	}
	if len(events) > 0 {
		sb.WriteString("\n## Events\n")
		for _, event := range events {
			fmt.Fprintf(&sb, "[%s] %s %s: %s\n",
				formatScheduleTime(event.CreatedAt, schedule.Timezone), event.Level, event.EventType, shorten(event.Message, 220))
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

func (h *loopHandler) updateStatus(ctx context.Context, msg *IncomingMessage, id string, status loop.ScheduleStatus) (string, error) {
	schedule, err := h.store.GetSchedule(ctx, id)
	if err != nil {
		return "loop 不存在或读取失败。", nil
	}
	if schedule.UserID != msg.UserID {
		return "只能操作你自己的 loop。", nil
	}
	if err := h.store.UpdateScheduleStatus(ctx, id, status); err != nil {
		return "更新 loop 状态失败：" + err.Error(), nil
	}
	return fmt.Sprintf("loop %s 已更新为 %s", id, status), nil
}

func (h *loopHandler) run(ctx context.Context, msg *IncomingMessage, id string) (string, error) {
	if h.runner == nil {
		return "Loop Runner 尚未初始化。", nil
	}
	run, task, err := h.runner.RunManual(ctx, id, msg.UserID)
	if err != nil {
		if err.Error() == "permission denied" {
			return "只能运行你自己的 loop。", nil
		}
		if errors.Is(err, loop.ErrActiveRun) {
			return h.activeRunMessage(ctx, id), nil
		}
		return "启动 loop run 失败：" + err.Error(), nil
	}
	return fmt.Sprintf("已启动 loop run：%s\nparent task：%s\n状态：%s", run.ID, task.ID, run.Status), nil
}

func (h *loopHandler) activeRunMessage(ctx context.Context, id string) string {
	runs, err := h.store.ListRuns(ctx, id, 10)
	if err != nil {
		return "这个 loop 已有未结束的 run，先等它结束或暂停后再手动触发。"
	}
	for _, run := range runs {
		if run.Status == loop.RunStatusPending || run.Status == loop.RunStatusRunning {
			msg := fmt.Sprintf("这个 loop 已有未结束的 run：%s\n状态：%s", run.ID, run.Status)
			if strings.TrimSpace(run.ParentTaskID) != "" {
				msg += "\nparent task：" + run.ParentTaskID
			}
			msg += "\n查看：/loop runlog " + run.ID
			return msg
		}
	}
	return "这个 loop 已有未结束的 run，先等它结束或暂停后再手动触发。"
}

func loopUsage() string {
	return `用法：
/loop create <目标>
/loop list
/loop status <loop_id>
/loop runs <loop_id> [limit]
/loop runlog <run_id>
/loop pause <loop_id>
/loop resume <loop_id>
/loop run <loop_id>`
}

func buildRuleBasedPlan(goal string) loop.LoopPlan {
	title := shorten(goal, 28)
	projectKey := inferProjectKey(goal)
	schedule := inferPlanSchedule(goal)
	return loop.LoopPlan{
		Title:      title,
		ProjectKey: projectKey,
		Objective:  goal,
		Schedule:   schedule,
		SuccessCriteria: []string{
			"能生成本轮状态摘要",
			"能识别一个最小下一步或说明不执行原因",
			"高风险操作会要求人工确认",
		},
		Checklist: []string{
			"读取相关项目状态和 TODO",
			"检查最近任务和事件",
			"判断是否存在低风险可推进事项",
		},
		AllowedActions: []string{
			"read_files",
			"run_tests",
			"edit_docs",
			"small_code_changes",
		},
		ApprovalRequiredFor: []string{
			"delete_files",
			"install_dependencies",
			"restart_service",
			"deploy",
			"modify_credentials",
		},
		Notify: loop.PlanNotify{
			OnDone:             true,
			OnError:            true,
			OnApprovalRequired: true,
		},
	}
}

func inferPlanSchedule(goal string) loop.PlanSchedule {
	schedule := loop.PlanSchedule{
		Kind:     "manual",
		Timezone: "Asia/Shanghai",
	}
	matches := dailyTimePattern.FindStringSubmatch(goal)
	if len(matches) >= 2 {
		hour, err := strconv.Atoi(matches[1])
		if err == nil && hour >= 0 && hour <= 23 {
			minute := 0
			if len(matches) >= 3 && matches[2] != "" {
				if parsed, err := strconv.Atoi(matches[2]); err == nil && parsed >= 0 && parsed <= 59 {
					minute = parsed
				}
			}
			schedule.Kind = "daily"
			schedule.Time = fmt.Sprintf("%02d:%02d", hour, minute)
		}
	}
	intervalMatches := intervalPattern.FindStringSubmatch(goal)
	if len(intervalMatches) >= 3 {
		amount, err := strconv.Atoi(intervalMatches[1])
		if err == nil && amount > 0 {
			minutes := amount
			unit := strings.ToLower(intervalMatches[2])
			switch unit {
			case "小时", "钟头", "hour", "hours", "hr", "hrs", "h":
				minutes = amount * 60
			}
			schedule.Kind = "interval"
			schedule.IntervalMinutes = minutes
			schedule.Time = ""
		}
	} else if everyMinutePattern.MatchString(goal) {
		schedule.Kind = "interval"
		schedule.IntervalMinutes = 1
		schedule.Time = ""
	}
	if taskDonePattern.MatchString(goal) {
		schedule.TriggerOnTaskDone = true
	}
	return schedule
}

func scheduleExpr(schedule loop.PlanSchedule) string {
	switch schedule.Kind {
	case "daily":
		if schedule.Time != "" {
			expr := "daily:" + schedule.Time
			if schedule.TriggerOnTaskDone {
				return expr + "+task_done"
			}
			return expr
		}
	case "interval":
		if schedule.IntervalMinutes > 0 {
			expr := fmt.Sprintf("interval:%dm", schedule.IntervalMinutes)
			if schedule.TriggerOnTaskDone {
				return expr + "+task_done"
			}
			return expr
		}
	}
	if schedule.TriggerOnTaskDone {
		return "task_done"
	}
	return "manual"
}

func nextRunAt(schedule loop.PlanSchedule) *time.Time {
	if schedule.Kind == "interval" && schedule.IntervalMinutes > 0 {
		next := time.Now().In(loadLocation(schedule.Timezone)).Add(time.Duration(schedule.IntervalMinutes) * time.Minute)
		return &next
	}
	if schedule.Kind != "daily" || schedule.Time == "" {
		return nil
	}
	parts := strings.Split(schedule.Time, ":")
	if len(parts) != 2 {
		return nil
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return nil
	}
	loc := loadLocation(schedule.Timezone)
	now := time.Now().In(loc)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return &next
}

func formatScheduleTime(t time.Time, timezone string) string {
	return t.In(loadLocation(timezone)).Format("2006-01-02 15:04:05")
}

func loadLocation(timezone string) *time.Location {
	if strings.TrimSpace(timezone) == "" {
		timezone = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return loc
}

func inferProjectKey(goal string) string {
	lower := strings.ToLower(goal)
	switch {
	case strings.Contains(lower, "myself-ai"):
		return "myself-ai"
	default:
		return ""
	}
}

func inferExecutor(goal string) string {
	lower := strings.ToLower(goal)
	switch {
	case strings.Contains(lower, "codex"):
		return "codex"
	case strings.Contains(lower, "kiro"):
		return "kiro"
	case strings.Contains(lower, "claude"):
		return "claude"
	default:
		return ""
	}
}

func emptyAsDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func appendRunSection(sb *strings.Builder, title, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	fmt.Fprintf(sb, "\n## %s\n%s\n", title, trimForLoopReply(content, 2400))
}

func trimForLoopReply(content string, maxRunes int) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return content
	}
	return string(runes[:maxRunes]) + "..."
}
