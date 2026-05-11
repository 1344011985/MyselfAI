package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/1344011985/MyselfAI/internal/memory"
)

// --- /remember handler ---

type rememberHandler struct{ store memory.Store }

func (h *rememberHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	content := strings.TrimSpace(strings.TrimPrefix(msg.Content, "/remember"))
	if content == "" {
		return "请提供要记住的内容，例如：/remember 我喜欢简洁的回答", nil
	}
	if err := h.store.AddMemory(msg.UserID, content); err != nil {
		return "保存记忆失败，请稍后重试。", nil
	}
	return fmt.Sprintf("已记住：%s", content), nil
}
