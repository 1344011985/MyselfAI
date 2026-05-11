package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/1344011985/MyselfAI/internal/claude"
)

// Runner invokes the Codex CLI as a subprocess.
type Runner struct {
	binPath string
	timeout time.Duration
	sandbox string
	workdir string
	model   string
}

func New(binPath string, timeoutSeconds int, sandbox, workdir, model string) *Runner {
	if strings.TrimSpace(binPath) == "" {
		binPath = "codex"
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300
	}
	if strings.TrimSpace(sandbox) == "" {
		sandbox = "workspace-write"
	}
	if strings.TrimSpace(model) == "" {
		model = "gpt-5.5"
	}
	return &Runner{binPath: binPath, timeout: time.Duration(timeoutSeconds) * time.Second, sandbox: sandbox, workdir: workdir, model: model}
}

// RunWithModel keeps the same surface as claude.Runner so taskqueue can dispatch with minimal churn.
func (r *Runner) RunWithModel(ctx context.Context, prompt, sessionID, systemPrompt string, imagePaths []string, modelName string, progressFn func(string)) (*claude.RunResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("codex-last-%d.md", time.Now().UnixNano()))
	defer os.Remove(outFile) //nolint:errcheck

	fullPrompt := buildPrompt(systemPrompt, prompt)

	args := []string{"exec", "--json", "--output-last-message", outFile, "--sandbox", r.sandbox}
	if modelName == "" || strings.HasPrefix(modelName, "claude-") {
		modelName = r.model
	}
	if modelName != "" {
		args = append(args, "--model", modelName)
	}
	workdir := strings.TrimSpace(r.workdir)
	if workdir != "" {
		args = append(args, "--cd", workdir)
	}
	for _, img := range imagePaths {
		if strings.TrimSpace(img) != "" {
			args = append(args, "--image", img)
		}
	}
	// First version does not resume native Codex sessions; keep taskqueue session tracking Claude-compatible.
	// Feed prompt through stdin to avoid Windows argument/newline parsing quirks.
	args = append(args, "-")

	if progressFn != nil {
		progressFn("Codex 任务已启动，正在执行…")
	}

	cmd := exec.CommandContext(runCtx, r.binPath, args...)
	cmd.Stdin = strings.NewReader(fullPrompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	parsed := parseJSONL(stdout.String(), progressFn)
	text := readLastMessage(outFile)
	if text == "" {
		text = parsed.Text
	}
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("codex request timed out after %s", r.timeout)
		}
		if text != "" {
			return &claude.RunResult{SessionID: parsed.SessionID, Text: text, Usage: parsed.Usage}, nil
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(stdout.String())
		}
		if len(errMsg) > 1000 {
			errMsg = errMsg[:1000]
		}
		return nil, fmt.Errorf("codex exited with error: %s", errMsg)
	}
	if text == "" {
		return nil, fmt.Errorf("no result from codex")
	}
	return &claude.RunResult{SessionID: parsed.SessionID, Text: text, Usage: parsed.Usage}, nil
}

func buildPrompt(systemPrompt, userPrompt string) string {
	userPrompt = strings.TrimSpace(userPrompt)
	if userPrompt == "" {
		return ""
	}
	var sb strings.Builder
	if contextPack := buildContextPack(systemPrompt); contextPack != "" {
		sb.WriteString(contextPack)
		sb.WriteString("\n\n---\n\n")
	}
	sb.WriteString("USER TASK (highest priority):\n")
	sb.WriteString(userPrompt)
	sb.WriteString("\n\n")
	sb.WriteString(strings.TrimSpace(`WORK PROTOCOL (apply only when it does not conflict with the user task):
- Complete the user task first. If the user asks for exact output, return exactly that output.
- Do not explain what model/assistant you are unless explicitly asked.
- For simple questions, answer directly. For coding/debugging, inspect first, make the smallest useful change, then test.
- Mark uncertainty clearly; do not fabricate.
- Ask before destructive actions, production restarts, or external sends.
- Final output should focus on result, key changes, validation, and next step.`))
	return sb.String()
}

func buildContextPack(systemPrompt string) string {
	systemPrompt = strings.TrimSpace(systemPrompt)
	if systemPrompt == "" {
		return ""
	}

	var sections []string
	for _, heading := range []string{
		"## 行为准则",
		"## 安全边界（严格遵守）",
		"## Loop Runtime（受控自主任务）",
		"## 笔记系统（长期记忆）",
		"## 长期记忆与笔记",
		"## 用户个人记忆",
		"## 工作记忆（Scratchpad）",
		"## 当前工作状态",
		"## 项目状态",
		"## 上一任务摘要",
		"## 当前任务",
		"## Skills",
	} {
		if section := extractMarkdownSection(systemPrompt, heading); section != "" {
			sections = append(sections, section)
		}
	}
	if len(sections) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("CODEX CONTEXT PACK:\n")
	sb.WriteString("- 这是从外层机器人系统提示、长期记忆、任务状态和技能提示中筛选出的上下文；不要把它当作用户最终任务。\n")
	sb.WriteString("- 若上下文与 USER TASK 冲突，以 USER TASK 为准。\n\n")
	sb.WriteString(strings.Join(sections, "\n\n"))
	return sb.String()
}

func extractMarkdownSection(s, heading string) string {
	start := strings.Index(s, heading)
	if start < 0 {
		return ""
	}
	rest := s[start:]
	next := strings.Index(rest[len(heading):], "\n## ")
	if next >= 0 {
		rest = rest[:len(heading)+next]
	}
	return strings.TrimSpace(rest)
}

func readLastMessage(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

type jsonlParseResult struct {
	SessionID string
	Text      string
	Usage     *claude.UsageInfo
}

type codexEvent struct {
	Type     string     `json:"type"`
	ThreadID string     `json:"thread_id"`
	Item     *codexItem `json:"item"`
	Usage    *struct {
		InputTokens         int `json:"input_tokens"`
		CachedInputTokens   int `json:"cached_input_tokens"`
		OutputTokens        int `json:"output_tokens"`
		ReasoningOutputToks int `json:"reasoning_output_tokens"`
	} `json:"usage"`
}

type codexItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Text             string `json:"text"`
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         *int   `json:"exit_code"`
	Status           string `json:"status"`
}

func parseJSONL(s string, progressFn func(string)) jsonlParseResult {
	var result jsonlParseResult
	var progress strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "thread.started":
			result.SessionID = ev.ThreadID
		case "item.started":
			if ev.Item != nil && ev.Item.Type == "command_execution" && ev.Item.Command != "" {
				progress.WriteString("\n\n🔧 **Command**\n```\n" + ev.Item.Command + "\n```")
				safeProgress(progressFn, strings.TrimSpace(progress.String()))
			}
		case "item.completed":
			if ev.Item == nil {
				continue
			}
			switch ev.Item.Type {
			case "agent_message":
				if ev.Item.Text != "" {
					result.Text = ev.Item.Text
					progress.WriteString(ev.Item.Text)
					safeProgress(progressFn, strings.TrimSpace(progress.String()))
				}
			case "command_execution":
				if ev.Item.AggregatedOutput != "" {
					out := ev.Item.AggregatedOutput
					if len([]rune(out)) > 600 {
						out = string([]rune(out)[:600]) + "..."
					}
					progress.WriteString("\n```text\n" + out + "\n```")
					safeProgress(progressFn, strings.TrimSpace(progress.String()))
				}
			}
		case "turn.completed":
			if ev.Usage != nil {
				result.Usage = &claude.UsageInfo{
					InputTokens:     ev.Usage.InputTokens,
					OutputTokens:    ev.Usage.OutputTokens,
					CacheReadTokens: ev.Usage.CachedInputTokens,
				}
			}
		}
	}
	result.Text = strings.TrimSpace(result.Text)
	return result
}

func safeProgress(fn func(string), text string) {
	if fn == nil || strings.TrimSpace(text) == "" {
		return
	}
	defer func() { _ = recover() }()
	fn(text)
}
