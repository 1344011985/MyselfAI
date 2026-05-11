package loop

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	maxRunReportSectionBytes = 2400
	maxLoopAutoRetries       = 3
)

func DiagnoseLoopError(stage string, err error) ErrorDiagnosis {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		stage = "unknown"
	}
	diag := ErrorDiagnosis{
		Stage:     stage,
		Kind:      "executor_error",
		Retryable: true,
	}
	if err == nil {
		return diag
	}
	text := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.Canceled) || strings.Contains(text, "cancelled") || strings.Contains(text, "canceled"):
		diag.Kind = "cancelled"
		diag.Retryable = false
	case strings.Contains(text, "approval_required") || strings.Contains(text, "approval required"):
		diag.Kind = "approval_required"
		diag.Retryable = false
	case strings.Contains(text, "api key") || strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "forbidden") || strings.Contains(text, "permission denied") ||
		strings.Contains(text, "not configured") || strings.Contains(text, "invalid model"):
		diag.Kind = "provider_config"
		diag.Retryable = false
	case strings.Contains(text, "rate limit") || strings.Contains(text, "too many requests") ||
		strings.Contains(text, " 429") || strings.Contains(text, "status 429"):
		diag.Kind = "rate_limit"
		diag.Retryable = true
	case strings.Contains(text, "timeout") || strings.Contains(text, "timed out") ||
		strings.Contains(text, "deadline exceeded"):
		diag.Kind = "timeout"
		diag.Retryable = true
	case strings.Contains(text, "econnreset") || strings.Contains(text, "connection reset") ||
		strings.Contains(text, "connection refused") || strings.Contains(text, "no such host") ||
		strings.Contains(text, "network") || strings.Contains(text, "tls handshake") ||
		strings.Contains(text, "unable to connect"):
		diag.Kind = "network"
		diag.Retryable = true
	}
	return diag
}

func ErrorFromResult(result string) error {
	line := firstResultLine(result)
	if line == "" {
		return nil
	}
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "api error:") ||
		strings.HasPrefix(lower, "error: api error:") ||
		strings.Contains(lower, "api error: unable to connect to api") {
		return errors.New(line)
	}
	return nil
}

func firstResultLine(result string) string {
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func RetryBackoff(kind string, retryCount int) time.Duration {
	if retryCount <= 0 {
		retryCount = 1
	}
	switch strings.TrimSpace(kind) {
	case "rate_limit":
		switch retryCount {
		case 1:
			return 30 * time.Minute
		case 2:
			return 60 * time.Minute
		default:
			return 2 * time.Hour
		}
	case "timeout":
		switch retryCount {
		case 1:
			return 10 * time.Minute
		case 2:
			return 30 * time.Minute
		default:
			return 60 * time.Minute
		}
	default:
		switch retryCount {
		case 1:
			return 5 * time.Minute
		case 2:
			return 15 * time.Minute
		default:
			return 45 * time.Minute
		}
	}
}

func ExtractRunReport(result string) RunReport {
	sections := markdownSections(result)
	report := RunReport{
		ArtifactSummary: firstSection(sections, "artifacts", "run artifacts", "outputs", "产物", "输出产物"),
		DiffSummary:     firstSection(sections, "diff", "git diff", "changes", "变更", "代码变更"),
		LogSummary:      firstSection(sections, "logs", "log", "verification", "验证", "日志", "运行日志"),
	}
	report.ArtifactSummary = truncateBytes(report.ArtifactSummary, maxRunReportSectionBytes)
	report.DiffSummary = truncateBytes(report.DiffSummary, maxRunReportSectionBytes)
	report.LogSummary = truncateBytes(report.LogSummary, maxRunReportSectionBytes)
	return report
}

func DecisionFromResult(result string) string {
	sections := markdownSections(result)
	return strings.ToLower(strings.TrimSpace(firstSection(sections, "decision", "决策")))
}

type RunRisk struct {
	Pause     bool
	EventType EventType
	Message   string
}

func DetectRunRisk(decision string, report RunReport) RunRisk {
	if strings.Contains(strings.ToLower(strings.TrimSpace(decision)), "approval_required") {
		return RunRisk{
			Pause:     true,
			EventType: EventTypeApprovalRequired,
			Message:   "approval_required requested; loop paused for manual review",
		}
	}
	text := strings.ToLower(strings.Join([]string{
		report.ArtifactSummary,
		report.DiffSummary,
		report.LogSummary,
	}, "\n"))
	if hasArtifactConflictSignal(text) {
		return RunRisk{
			Pause:     true,
			EventType: EventTypeReviewRequested,
			Message:   "artifact conflict or race detected; loop paused for manual review",
		}
	}
	return RunRisk{}
}

func hasArtifactConflictSignal(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, marker := range []string{
		"race 输",
		"race lost",
		"冲突夹带",
		"产物冲突",
		"编号冲突",
		"撞号",
		"重复章节",
		"duplicate chapter",
		"artifact conflict",
		"待裁决",
	} {
		if strings.Contains(text, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func markdownSections(markdown string) map[string]string {
	sections := make(map[string]string)
	var current string
	var lines []string
	flush := func() {
		if current == "" {
			return
		}
		body := strings.TrimSpace(strings.Join(lines, "\n"))
		if body != "" {
			sections[current] = body
		}
	}
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") {
			flush()
			current = normalizeSectionTitle(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")))
			lines = nil
			continue
		}
		if current != "" {
			lines = append(lines, line)
		}
	}
	flush()
	return sections
}

func firstSection(sections map[string]string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(sections[normalizeSectionTitle(name)]); value != "" {
			return value
		}
	}
	return ""
}

func normalizeSectionTitle(title string) string {
	title = strings.TrimSpace(strings.ToLower(title))
	title = strings.Trim(title, ":：")
	return strings.Join(strings.Fields(title), " ")
}

func truncateBytes(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	runes := []rune(value)
	var b strings.Builder
	for _, r := range runes {
		if b.Len()+len(string(r)) > max {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String()) + "..."
}
