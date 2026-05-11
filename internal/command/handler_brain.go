package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/1344011985/MyselfAI/internal/loop"
)

type thinkHandler struct {
	store loop.Store
	brain *loop.BrainStore
}

type brainHandler struct {
	store loop.Store
	brain *loop.BrainStore
}

func (h *thinkHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	if h.store == nil || h.brain == nil {
		return "Loop Brain 尚未初始化。", nil
	}
	parts := strings.Fields(msg.Content)
	if len(parts) < 3 {
		return "用法：/think <loop_id> <想法>", nil
	}
	schedule, err := h.store.GetSchedule(ctx, parts[1])
	if err != nil {
		return "loop 不存在或读取失败。", nil
	}
	if schedule.UserID != msg.UserID {
		return "只能写入你自己的 loop brain。", nil
	}
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(msg.Content), parts[0]))
	text = strings.TrimSpace(strings.TrimPrefix(text, parts[1]))
	if text == "" {
		return "用法：/think <loop_id> <想法>", nil
	}
	path, err := h.brain.AppendInbox(schedule, "user", text)
	if err != nil {
		return "写入 brain inbox 失败：" + err.Error(), nil
	}
	return fmt.Sprintf("已写入 brain inbox：%s", path), nil
}

func (h *brainHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	if h.store == nil || h.brain == nil {
		return "Loop Brain 尚未初始化。", nil
	}
	parts := strings.Fields(msg.Content)
	if len(parts) < 2 {
		return brainUsage(), nil
	}
	subcmd := strings.ToLower(parts[1])
	switch subcmd {
	case "list":
		return h.list(ctx, msg)
	case "show":
		if len(parts) < 3 {
			return "用法：/brain show <loop_id>", nil
		}
		return h.show(ctx, msg, parts[2], false)
	case "inbox":
		if len(parts) < 3 {
			return "用法：/brain inbox <loop_id>", nil
		}
		return h.show(ctx, msg, parts[2], true)
	case "path":
		if len(parts) < 3 {
			return "用法：/brain path <loop_id>", nil
		}
		return h.path(ctx, msg, parts[2])
	default:
		return h.show(ctx, msg, parts[1], false)
	}
}

func (h *brainHandler) list(ctx context.Context, msg *IncomingMessage) (string, error) {
	schedules, err := h.store.ListSchedules(ctx, msg.UserID, 20)
	if err != nil {
		return "读取 loop 列表失败：" + err.Error(), nil
	}
	if len(schedules) == 0 {
		return "当前没有 loop brain。", nil
	}
	var sb strings.Builder
	for _, schedule := range schedules {
		fmt.Fprintf(&sb, "%s  %s  %s\n", schedule.ID, schedule.Status, shorten(schedule.Title, 32))
	}
	return strings.TrimSpace(sb.String()), nil
}

func (h *brainHandler) show(ctx context.Context, msg *IncomingMessage, id string, inboxOnly bool) (string, error) {
	schedule, err := h.authorizedSchedule(ctx, msg, id)
	if err != nil {
		return err.Error(), nil
	}
	content, path, err := h.brain.Read(schedule)
	if err != nil {
		return "读取 brain 失败：" + err.Error(), nil
	}
	if inboxOnly {
		content = extractSection(content, "Inbox")
		if strings.TrimSpace(content) == "" {
			content = "Inbox 为空。"
		}
	}
	return fmt.Sprintf("Brain：%s\n\n%s", path, trimForReply(content, 12000)), nil
}

func (h *brainHandler) path(ctx context.Context, msg *IncomingMessage, id string) (string, error) {
	schedule, err := h.authorizedSchedule(ctx, msg, id)
	if err != nil {
		return err.Error(), nil
	}
	path, err := h.brain.Ensure(schedule)
	if err != nil {
		return "创建 brain 失败：" + err.Error(), nil
	}
	return path, nil
}

func (h *brainHandler) authorizedSchedule(ctx context.Context, msg *IncomingMessage, id string) (*loop.LoopSchedule, error) {
	schedule, err := h.store.GetSchedule(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("loop 不存在或读取失败。")
	}
	if schedule.UserID != msg.UserID {
		return nil, fmt.Errorf("只能查看你自己的 loop brain。")
	}
	return schedule, nil
}

func brainUsage() string {
	return `用法：
/brain list
/brain show <loop_id>
/brain inbox <loop_id>
/brain path <loop_id>
/think <loop_id> <想法>`
}

func extractSection(content, name string) string {
	header := "## " + name
	start := strings.Index(content, header)
	if start < 0 {
		return ""
	}
	body := content[start+len(header):]
	if idx := strings.Index(body, "\n## "); idx >= 0 {
		body = body[:idx]
	}
	return strings.TrimSpace(body)
}

func trimForReply(content string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "\n\n...（已截断）"
}
