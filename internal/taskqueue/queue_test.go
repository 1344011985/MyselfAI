package taskqueue

import (
	"strings"
	"testing"

	"github.com/1344011985/MyselfAI/internal/memory"
)

func TestFormatRecentHistoryChronological(t *testing.T) {
	entries := []memory.HistoryEntry{
		{Input: "第三轮", Response: "第三答"},
		{Input: "第二轮", Response: "第二答"},
		{Input: "第一轮", Response: "第一答"},
	}

	got := formatRecentHistory(entries, 3)

	first := strings.Index(got, "用户：第一轮")
	second := strings.Index(got, "用户：第二轮")
	third := strings.Index(got, "用户：第三轮")
	if first < 0 || second < 0 || third < 0 {
		t.Fatalf("missing expected turns:\n%s", got)
	}
	if !(first < second && second < third) {
		t.Fatalf("history should be injected oldest-to-newest:\n%s", got)
	}
}

func TestFormatRecentHistoryLimitAndTruncate(t *testing.T) {
	long := strings.Repeat("长", 900)
	entries := []memory.HistoryEntry{
		{Input: "最新", Response: long},
		{Input: "旧的", Response: "旧答"},
	}

	got := formatRecentHistory(entries, 1)

	if strings.Contains(got, "旧的") {
		t.Fatalf("history should respect max turns:\n%s", got)
	}
	if !strings.Contains(got, "用户：最新") {
		t.Fatalf("missing newest turn:\n%s", got)
	}
	if strings.Count(got, "长") != 800 || !strings.Contains(got, "...") {
		t.Fatalf("long response should be truncated with ellipsis:\n%s", got)
	}
}
