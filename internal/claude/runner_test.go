package claude

import (
	"context"
	"testing"
)

func TestRunnerNew(t *testing.T) {
	r := New("claude", 30)
	if r == nil {
		t.Fatal("runner is nil")
	}
}

func TestRunWithModelTimeoutContext(t *testing.T) {
	r := New("claude", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.RunWithModel(ctx, "hi", "", "", nil, "", nil)
	if err == nil {
		t.Fatal("expected error when context already cancelled")
	}
}
