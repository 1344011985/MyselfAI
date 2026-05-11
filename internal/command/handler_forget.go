package command

import (
	"context"

	"github.com/1344011985/MyselfAI/internal/memory"
)

// --- /forget handler ---

type forgetHandler struct{ store memory.Store }

func (h *forgetHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	if err := h.store.DeleteMemories(msg.UserID); err != nil {
		return "清除记忆失败，请稍后重试。", nil
	}
	return "已清除所有记忆。", nil
}
