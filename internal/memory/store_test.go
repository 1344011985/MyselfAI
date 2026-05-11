package memory

import (
	"fmt"
	"sort"
	"testing"

	"pgregory.net/rapid"
)

// newMemStore creates an in-memory SQLite store; fatals on error.
func newMemStore(t interface {
	Helper()
	Fatalf(string, ...any)
	Cleanup(func())
}) Store {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Feature: myself-ai, Property 1: Session round-trip
func TestProperty1_SessionRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s, err := NewSQLiteStore(":memory:")
		if err != nil {
			rt.Fatalf("store: %v", err)
		}
		defer s.Close()

		userID := rapid.StringMatching(`[a-z0-9]{1,20}`).Draw(rt, "userID")
		sessionID := rapid.StringMatching(`[a-zA-Z0-9]{1,40}`).Draw(rt, "sessionID")

		if err := s.SaveSession(userID, sessionID); err != nil {
			rt.Fatalf("SaveSession: %v", err)
		}
		got, err := s.GetSession(userID)
		if err != nil {
			rt.Fatalf("GetSession: %v", err)
		}
		if got != sessionID {
			rt.Fatalf("round-trip mismatch: saved %q, got %q", sessionID, got)
		}
	})
}

// Feature: myself-ai, Property 2: 记忆注入完整性
func TestProperty2_MemoryIntegrity(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s, err := NewSQLiteStore(":memory:")
		if err != nil {
			rt.Fatalf("store: %v", err)
		}
		defer s.Close()

		userID := rapid.StringMatching(`[a-z0-9]{1,20}`).Draw(rt, "userID")
		n := rapid.IntRange(0, 20).Draw(rt, "n")

		written := make([]string, n)
		for i := 0; i < n; i++ {
			written[i] = fmt.Sprintf("memory-%d", i)
			if err := s.AddMemory(userID, written[i]); err != nil {
				rt.Fatalf("AddMemory: %v", err)
			}
		}

		got, err := s.GetMemories(userID)
		if err != nil {
			rt.Fatalf("GetMemories: %v", err)
		}
		if len(got) != n {
			rt.Fatalf("expected %d memories, got %d", n, len(got))
		}

		sortedW := append([]string{}, written...)
		sortedG := append([]string{}, got...)
		sort.Strings(sortedW)
		sort.Strings(sortedG)
		for i := range sortedW {
			if sortedW[i] != sortedG[i] {
				rt.Fatalf("content mismatch at %d: want %q got %q", i, sortedW[i], sortedG[i])
			}
		}
	})
}

// Feature: myself-ai, Property 3: 记忆删除彻底性
func TestProperty3_MemoryDeletion(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s, err := NewSQLiteStore(":memory:")
		if err != nil {
			rt.Fatalf("store: %v", err)
		}
		defer s.Close()

		userID := rapid.StringMatching(`[a-z0-9]{1,20}`).Draw(rt, "userID")
		n := rapid.IntRange(0, 10).Draw(rt, "n")

		for i := 0; i < n; i++ {
			if err := s.AddMemory(userID, fmt.Sprintf("mem-%d", i)); err != nil {
				rt.Fatalf("AddMemory: %v", err)
			}
		}
		if err := s.DeleteMemories(userID); err != nil {
			rt.Fatalf("DeleteMemories: %v", err)
		}
		got, err := s.GetMemories(userID)
		if err != nil {
			rt.Fatalf("GetMemories: %v", err)
		}
		if len(got) != 0 {
			rt.Fatalf("expected empty after delete, got %d", len(got))
		}
	})
}

// Feature: myself-ai, Property 4: 历史记录追加不变性
func TestProperty4_HistoryAppendInvariant(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s, err := NewSQLiteStore(":memory:")
		if err != nil {
			rt.Fatalf("store: %v", err)
		}
		defer s.Close()

		userID := rapid.StringMatching(`[a-z0-9]{1,20}`).Draw(rt, "userID")
		n := rapid.IntRange(0, 15).Draw(rt, "n")
		k := rapid.IntRange(1, 20).Draw(rt, "k")

		inputs := make([]string, n)
		for i := 0; i < n; i++ {
			inputs[i] = fmt.Sprintf("input-%d", i)
			if err := s.SaveHistory(userID, inputs[i], fmt.Sprintf("resp-%d", i)); err != nil {
				rt.Fatalf("SaveHistory: %v", err)
			}
		}

		got, err := s.GetHistory(userID, k)
		if err != nil {
			rt.Fatalf("GetHistory: %v", err)
		}

		expected := n
		if k < n {
			expected = k
		}
		if len(got) != expected {
			rt.Fatalf("expected %d entries (n=%d k=%d), got %d", expected, n, k, len(got))
		}

		// Newest first: last written input should be first returned
		if n > 0 && len(got) > 0 {
			if got[0].Input != inputs[n-1] {
				rt.Fatalf("newest-first violated: want %q got %q", inputs[n-1], got[0].Input)
			}
		}
	})
}
