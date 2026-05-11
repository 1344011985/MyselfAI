package codex

import (
	"strings"
	"testing"
)

func TestBuildPromptUsesCodexContextPack(t *testing.T) {
	systemPrompt := strings.Join([]string{
		"你是一个运行在飞书机器人上的 AI 助手，由 Claude 驱动。",
		"",
		"## 行为准则",
		"- 直接给出核心答案",
		"- 用中文回复",
		"",
		"## 安全边界（严格遵守）",
		"- 禁止执行任何删除、格式化、清空数据的危险操作",
		"",
		"## 长期记忆与笔记",
		"# MEMORY.md",
		"- 当前笔记目录：`~/.myself-ai/notes`",
		"",
		"## 当前任务：测试",
		"**指令**：检查上下文",
		"",
		"## 可用命令",
		"- /ask <问题> —— 向 Claude 提问",
		"",
		"## Skills",
		"### coding-agent",
		"先读代码，再修改。",
	}, "\n")

	got := buildPrompt(systemPrompt, "现在笔记目录在哪？")

	if strings.Contains(got, "由 Claude 驱动") {
		t.Fatalf("Codex prompt should not include Claude-oriented identity prompt:\n%s", got)
	}
	if strings.Contains(got, "/ask <问题>") {
		t.Fatalf("Codex prompt should not include outer bot command menu:\n%s", got)
	}
	if !strings.Contains(got, "CODEX CONTEXT PACK") {
		t.Fatalf("Codex prompt should include context pack:\n%s", got)
	}
	if !strings.Contains(got, "## 行为准则") || !strings.Contains(got, "## 安全边界（严格遵守）") {
		t.Fatalf("Codex prompt should preserve filtered system instructions:\n%s", got)
	}
	if !strings.Contains(got, "~/.myself-ai/notes") {
		t.Fatalf("Codex prompt should preserve long-term memory context:\n%s", got)
	}
	if !strings.Contains(got, "## Skills") || !strings.Contains(got, "先读代码，再修改。") {
		t.Fatalf("Codex prompt should preserve matched skill prompts:\n%s", got)
	}
	if !strings.Contains(got, "USER TASK (highest priority):\n现在笔记目录在哪？") {
		t.Fatalf("Codex prompt should keep user task explicit:\n%s", got)
	}
}

func TestExtractMarkdownSectionStopsAtNextHeading(t *testing.T) {
	input := "prefix\n\n## 长期记忆与笔记\nkeep\n\n## 可用命令\ndrop"
	got := extractMarkdownSection(input, "## 长期记忆与笔记")
	if got != "## 长期记忆与笔记\nkeep" {
		t.Fatalf("unexpected section: %q", got)
	}
}
