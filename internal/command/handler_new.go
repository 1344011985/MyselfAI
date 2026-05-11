package command

import (
	"context"

	"github.com/1344011985/MyselfAI/internal/memory"
)

// --- /new handler ---

type newHandler struct{ store memory.Store }

func (h *newHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	if err := h.store.DeleteSession(msg.UserID); err != nil {
		return "清除会话失败，请稍后重试。", nil
	}
	return "已开启新对话，之前的上下文已清除。", nil
}
