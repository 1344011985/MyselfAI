package kiro

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/1344011985/MyselfAI/internal/claude"
)

const defaultBinPath = "kiro-cli"

// ansiEscape matches ANSI/VT escape sequences to strip from output.
var ansiEscape = regexp.MustCompile(`\x1b(\[[0-9;?]*[A-Za-z]|\][^\x07]*\x07)`)

// Runner invokes the Kiro CLI as a subprocess.
type Runner struct {
	binPath string
	timeout time.Duration
	model   string
}

func New(binPath string, timeoutSeconds int, model string) *Runner {
	if strings.TrimSpace(binPath) == "" {
		binPath = defaultBinPath
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 3600
	}
	return &Runner{binPath: binPath, timeout: time.Duration(timeoutSeconds) * time.Second, model: model}
}

func (r *Runner) EffectiveModel(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" || strings.HasPrefix(modelName, "claude-") || strings.HasPrefix(modelName, "gpt-") {
		return strings.TrimSpace(r.model)
	}
	return modelName
}

// RunWithModel matches the claude.Runner surface so taskqueue can dispatch uniformly.
func (r *Runner) RunWithModel(ctx context.Context, userPrompt, sessionID, systemPrompt string, imagePaths []string, modelName string, progressFn func(string)) (*claude.RunResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	fullPrompt := buildPrompt(systemPrompt, userPrompt)

	args := []string{"chat", "--no-interactive", "--trust-all-tools"}

	// Use configured model if no override, skip claude-/gpt- names that don't belong to Kiro.
	modelName = r.EffectiveModel(modelName)
	if modelName != "" {
		args = append(args, "--model", modelName)
	}

	if progressFn != nil {
		progressFn("Kiro 任务已启动，正在执行…")
	}

	cmd := exec.CommandContext(runCtx, r.binPath, args...)
	cmd.Stdin = strings.NewReader(fullPrompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	text := parseOutput(stdout.String())

	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("kiro request timed out after %s", r.timeout)
		}
		if text != "" {
			return &claude.RunResult{Text: text}, nil
		}
		errMsg := strings.TrimSpace(stripANSI(stderr.String()))
		if errMsg == "" {
			errMsg = strings.TrimSpace(stripANSI(stdout.String()))
		}
		if len(errMsg) > 1000 {
			errMsg = errMsg[:1000]
		}
		return nil, fmt.Errorf("kiro exited with error: %s", errMsg)
	}
	if text == "" {
		return nil, fmt.Errorf("no result from kiro")
	}
	if progressFn != nil {
		progressFn(text)
	}
	return &claude.RunResult{Text: text}, nil
}

func buildPrompt(systemPrompt, userPrompt string) string {
	userPrompt = strings.TrimSpace(userPrompt)
	if userPrompt == "" {
		return ""
	}
	if sp := strings.TrimSpace(systemPrompt); sp != "" {
		return sp + "\n\n---\n\n" + userPrompt
	}
	return userPrompt
}

func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// parseOutput strips ANSI codes and extracts the AI response lines.
// Kiro prefixes its response with "> " after ANSI stripping.
func parseOutput(raw string) string {
	clean := stripANSI(raw)
	var result []string
	inResponse := false
	for _, line := range strings.Split(clean, "\n") {
		line = strings.TrimRight(line, "\r")
		if isNoiseLine(line) {
			continue
		}
		if strings.HasPrefix(line, "> ") {
			inResponse = true
			result = append(result, line[2:])
			continue
		}
		if inResponse && strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return strings.TrimSpace(strings.Join(result, "\n"))
}

func isNoiseLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return true
	}
	// Spinner animation frames contain block characters.
	if strings.ContainsAny(line, "▰▱") {
		return true
	}
	noiseSubstrings := []string{
		"Opening browser", "Logging in", "Logged in successfully",
		"Fetching profiles", "Device authorized",
		"All tools are now trusted", "Agents can sometimes",
		"Learn more at", "Credits:", "▸ Credits",
	}
	for _, n := range noiseSubstrings {
		if strings.Contains(line, n) {
			return true
		}
	}
	return false
}
