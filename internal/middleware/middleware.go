package middleware

import (
	"context"
	"log/slog"
)

// PromptRequest is the shared context passed through the middleware chain.
type PromptRequest struct {
	UserID       string
	Content      string // original user message
	SystemPrompt string // middleware can append to this
}

// PromptMiddleware intercepts and augments the prompt before it reaches the agent.
type PromptMiddleware interface {
	Name() string
	Process(ctx context.Context, req *PromptRequest) error
}

// Chain executes middlewares in order. Each middleware runs in its own panic boundary.
// A panic or error from one middleware is logged and skipped — it never stops the chain.
type Chain []PromptMiddleware

// Process runs all middlewares in order, recovering panics independently per middleware.
func (c Chain) Process(ctx context.Context, req *PromptRequest) error {
	for _, m := range c {
		runMiddleware(ctx, m, req)
	}
	return nil
}

// runMiddleware executes a single middleware with panic recovery.
func runMiddleware(ctx context.Context, m PromptMiddleware, req *PromptRequest) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("middleware panic recovered",
				"middleware", m.Name(),
				"recover", r,
			)
		}
	}()
	if err := m.Process(ctx, req); err != nil {
		slog.Warn("middleware error",
			"middleware", m.Name(),
			"err", err,
		)
	}
}
