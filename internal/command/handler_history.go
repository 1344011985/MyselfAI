package command

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/1344011985/MyselfAI/internal/memory"
)

// --- /history handler ---

type historyHandler struct{ store memory.Store }

func (h *historyHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	n := 5
	parts := strings.Fields(msg.Content)
	if len(parts) >= 2 {
		if v, err := strconv.Atoi(parts[1]); err == nil && v > 0 {
			n = v
		}
	}

	entries, err := h.store.GetHistory(msg.UserID, n)
	if err != nil {
		return "获取历史记录失败，请稍后重试。", nil
	}
	if len(entries) == 0 {
		return "暂无对话历史。", nil
	}

	var sb strings.Builder
	for i, e := range entries {
		fmt.Fprintf(&sb, "[%d] 你：%s\n    Bot：%s\n", i+1, e.Input, e.Response)
	}
	return strings.TrimSpace(sb.String()), nil
}
