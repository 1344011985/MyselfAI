package command

import (
	"context"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// knownCommands lists all registered slash commands.
var knownCommands = []string{"/new", "/remember", "/forget", "/history", "/help", "/version", "/loop"}

// Feature: myself-ai, Property 5: 指令路由正确性
// For any message not starting with '/', it should be handled by the ask (fallback) handler.
// For known slash commands, they should not return "未知指令".
func TestProperty5_RouterCorrectness(t *testing.T) {
	// Use a nil runner and nil store — we only test routing, not execution.
	// Handlers that need store/runner will panic; we only test /help, /version, and fallback.
	router := &Router{
		handlers: map[string]Handler{
			"/help":    &helpHandler{},
			"/version": &versionHandler{},
		},
		fallback: &echoHandler{},
	}

	rapid.Check(t, func(rt *rapid.T) {
		// Non-slash messages should go to fallback (echoHandler returns "echo:<content>")
		content := rapid.StringMatching(`[^/][a-zA-Z0-9 ]{0,30}`).Draw(rt, "content")
		msg := &IncomingMessage{UserID: "u1", Content: content}
		reply, err := router.Route(context.Background(), msg)
		if err != nil {
			rt.Fatalf("Route error for non-slash message: %v", err)
		}
		if !strings.HasPrefix(reply, "echo:") {
			rt.Fatalf("expected fallback handler for non-slash message %q, got reply: %q", content, reply)
		}
	})
}

// TestRouter_KnownCommands verifies each known command routes to its handler (not "未知指令").
func TestRouter_KnownCommands(t *testing.T) {
	router := &Router{
		handlers: map[string]Handler{
			"/help":    &helpHandler{},
			"/version": &versionHandler{},
			// Stub the rest so they don't panic
			"/new":      &echoHandler{},
			"/remember": &echoHandler{},
			"/forget":   &echoHandler{},
			"/history":  &echoHandler{},
			"/ask":      &echoHandler{},
		},
		fallback: &echoHandler{},
	}

	for _, cmd := range []string{"/help", "/version", "/new", "/remember", "/forget", "/history"} {
		msg := &IncomingMessage{UserID: "u1", Content: cmd + " test"}
		reply, err := router.Route(context.Background(), msg)
		if err != nil {
			t.Errorf("Route(%q) error: %v", cmd, err)
			continue
		}
		if strings.Contains(reply, "未知指令") {
			t.Errorf("Route(%q) returned '未知指令', expected a real handler", cmd)
		}
	}
}

// TestRouter_UnknownCommand verifies unknown slash commands return the "未知指令" message.
func TestRouter_UnknownCommand(t *testing.T) {
	router := &Router{
		handlers: map[string]Handler{},
		fallback: &echoHandler{},
	}
	msg := &IncomingMessage{UserID: "u1", Content: "/nonexistent"}
	reply, err := router.Route(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(reply, "未知指令") {
		t.Errorf("expected '未知指令' for unknown command, got: %q", reply)
	}
}

// echoHandler is a test double that echoes the message content.
type echoHandler struct{}

func (h *echoHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	return "echo:" + msg.Content, nil
}
