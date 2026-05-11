package skills

import (
	"context"
	"log/slog"

	"github.com/1344011985/MyselfAI/internal/middleware"
)

// SkillsMiddleware augments the system prompt with matched skills.
type SkillsMiddleware struct {
	hub *Hub
}

// NewSkillsMiddleware creates a SkillsMiddleware.
func NewSkillsMiddleware(hub *Hub) *SkillsMiddleware {
	return &SkillsMiddleware{hub: hub}
}

// Name implements middleware.PromptMiddleware.
func (m *SkillsMiddleware) Name() string { return "skills" }

// Process implements middleware.PromptMiddleware.
// Panics are recovered and demoted to no-op — the original prompt is preserved.
func (m *SkillsMiddleware) Process(_ context.Context, req *middleware.PromptRequest) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("skills middleware panic recovered", "recover", r)
			err = nil
		}
	}()
	req.SystemPrompt = m.hub.Augment(req.SystemPrompt, req.Content)
	return nil
}
