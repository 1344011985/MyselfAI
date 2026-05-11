package command

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/1344011985/MyselfAI/internal/browser"
	"github.com/1344011985/MyselfAI/internal/claude"
	"github.com/1344011985/MyselfAI/internal/memory"
)

// --- /browse handler ---

type browseHandler struct {
	manager  *browser.Manager
	runner   *claude.Runner
	selector *claude.ModelSelector
	store    memory.Store
	logger   Logger
}

// refPattern matches tokens like e1, e2, e99 at the start of a word.
var refPattern = regexp.MustCompile(`^e\d+$`)

func (h *browseHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	args := strings.TrimSpace(strings.TrimPrefix(msg.Content, "/browse"))
	if args == "" {
		return h.usage(), nil
	}

	parts := strings.Fields(args)

	// /browse 点击 e3
	if len(parts) == 2 && parts[0] == "点击" && refPattern.MatchString(parts[1]) {
		return h.handleClickRef(ctx, msg, parts[1])
	}

	// /browse 输入 e3 文字
	if len(parts) >= 3 && parts[0] == "输入" && refPattern.MatchString(parts[1]) {
		text := strings.Join(parts[2:], " ")
		return h.handleTypeRef(ctx, msg, parts[1], text)
	}

	// /browse aria  — take aria snapshot of current page
	if parts[0] == "aria" {
		return h.handleAria(ctx, msg)
	}

	// /browse 等待 <selector>
	if parts[0] == "等待" && len(parts) >= 2 {
		selector := strings.Join(parts[1:], " ")
		if err := h.manager.WaitForSelector(msg.UserID, selector); err != nil {
			return fmt.Sprintf("等待元素 %q 失败：%v", selector, err), nil
		}
		return fmt.Sprintf("元素 %q 已出现", selector), nil
	}

	// /browse 滚动 下 [pixels]  /  /browse 滚动 上 [pixels]
	if parts[0] == "滚动" && len(parts) >= 2 {
		pixels := 500
		if len(parts) >= 3 {
			if n, err := strconv.Atoi(parts[2]); err == nil && n > 0 {
				pixels = n
			}
		}
		switch parts[1] {
		case "下":
			if err := h.manager.ScrollDown(msg.UserID, pixels); err != nil {
				return fmt.Sprintf("向下滚动失败：%v", err), nil
			}
			return fmt.Sprintf("已向下滚动 %d px", pixels), nil
		case "上":
			if err := h.manager.ScrollUp(msg.UserID, pixels); err != nil {
				return fmt.Sprintf("向上滚动失败：%v", err), nil
			}
			return fmt.Sprintf("已向上滚动 %d px", pixels), nil
		}
	}

	// /browse 回车 e3  (press Enter then click ref, convenience alias)
	// /browse 按键 Enter  (raw key press)
	if (parts[0] == "回车" || parts[0] == "按键") && len(parts) >= 2 {
		key := parts[1]
		if parts[0] == "回车" {
			key = "Enter"
		}
		if err := h.manager.PressKey(msg.UserID, key); err != nil {
			return fmt.Sprintf("按键 %q 失败：%v", key, err), nil
		}
		return fmt.Sprintf("已按下 %s", key), nil
	}

	// /browse 保存登录 <name>
	if parts[0] == "保存登录" && len(parts) >= 2 {
		name := parts[1]
		path, err := h.manager.SaveState(msg.UserID, name)
		if err != nil {
			return fmt.Sprintf("保存登录状态失败：%v", err), nil
		}
		return fmt.Sprintf("登录状态已保存：%s", path), nil
	}

	// /browse 恢复登录 <name>
	if parts[0] == "恢复登录" && len(parts) >= 2 {
		name := parts[1]
		if err := h.manager.LoadState(msg.UserID, name); err != nil {
			return fmt.Sprintf("恢复登录状态失败：%v", err), nil
		}
		return fmt.Sprintf("登录状态 %q 已恢复", name), nil
	}

	// /browse <url> [instruction]
	url, instruction := parseArgs(args)
	if url == "" {
		return "请提供有效的 URL，例如：/browse https://example.com", nil
	}

	if msg.ProgressFn != nil {
		msg.ProgressFn("正在打开页面...")
	}

	if err := h.manager.Navigate(msg.UserID, url); err != nil {
		h.logger.Error("browser navigate failed", "url", url, "err", err)
		return fmt.Sprintf("无法打开页面：%v\n请检查 URL 是否正确，以及系统是否安装了 Chrome/Chromium。", err), nil
	}

	wantScreenshot := strings.Contains(instruction, "截图")
	wantAria := strings.Contains(instruction, "aria") || strings.Contains(instruction, "元素")

	switch {
	case wantScreenshot:
		return h.handleScreenshot(ctx, msg, url, instruction)
	case wantAria:
		return h.handleAria(ctx, msg)
	default:
		return h.handleText(ctx, msg, url, instruction)
	}
}

func (h *browseHandler) usage() string {
	return "/browse 用法：\n" +
		"  /browse <url>                  — 打开网页，AI 总结内容\n" +
		"  /browse <url> 截图             — 截图并让 AI 分析\n" +
		"  /browse <url> aria             — 提取可交互元素列表 (e1/e2...)\n" +
		"  /browse aria                   — 对当前页面提取 aria 快照\n" +
		"  /browse 点击 e3                — 点击编号为 e3 的元素\n" +
		"  /browse 输入 e3 文字内容       — 在 e3 元素输入文字\n" +
		"  /browse 等待 <selector>        — 等待 CSS selector 出现 (最多15s)\n" +
		"  /browse 滚动 下 [像素]         — 向下滚动（默认500px）\n" +
		"  /browse 滚动 上 [像素]         — 向上滚动\n" +
		"  /browse 回车 e3                — 按下 Enter 键\n" +
		"  /browse 按键 Enter             — 按下指定键（Enter/Tab/Escape等）\n" +
		"  /browse 保存登录 <name>        — 保存当前 cookies 到文件\n" +
		"  /browse 恢复登录 <name>        — 从文件恢复 cookies\n" +
		"  /browse <url> <指令>           — 打开网页后 AI 执行指令\n" +
		"示例：\n" +
		"  /browse https://github.com/trending\n" +
		"  /browse https://example.com aria\n" +
		"  /browse 点击 e5\n" +
		"  /browse 输入 e2 hello world\n" +
		"  /browse 滚动 下 1000\n" +
		"  /browse 按键 Enter\n" +
		"  /browse 保存登录 github\n" +
		"  /browse 恢复登录 github"
}

// handleAria takes an aria snapshot and returns the formatted element list.
func (h *browseHandler) handleAria(_ context.Context, msg *IncomingMessage) (string, error) {
	if msg.ProgressFn != nil {
		msg.ProgressFn("正在提取可交互元素...")
	}

	nodes, err := h.manager.AriaSnapshot(msg.UserID)
	if err != nil {
		return fmt.Sprintf("aria 快照失败：%v", err), nil
	}
	if len(nodes) == 0 {
		return "当前页面未发现可交互元素。", nil
	}

	url, _ := h.manager.GetCurrentURL(msg.UserID)
	header := fmt.Sprintf("页面：%s\n共 %d 个可交互元素：\n\n", url, len(nodes))
	return header + browser.FormatAriaSnapshot(nodes), nil
}

// handleClickRef clicks the ref element.
func (h *browseHandler) handleClickRef(_ context.Context, msg *IncomingMessage, ref string) (string, error) {
	if msg.ProgressFn != nil {
		msg.ProgressFn(fmt.Sprintf("正在点击 %s...", ref))
	}
	if err := h.manager.ClickRef(msg.UserID, ref); err != nil {
		return fmt.Sprintf("点击 %s 失败：%v", ref, err), nil
	}
	// wait a bit for page reaction, then give a short aria snapshot
	nodes, _ := h.manager.AriaSnapshot(msg.UserID)
	url, _ := h.manager.GetCurrentURL(msg.UserID)
	reply := fmt.Sprintf("已点击 %s。当前页面：%s\n", ref, url)
	if len(nodes) > 0 {
		reply += fmt.Sprintf("页面现有 %d 个可交互元素（使用 /browse aria 查看详情）", len(nodes))
	}
	return reply, nil
}

// handleTypeRef types text into the ref element.
func (h *browseHandler) handleTypeRef(_ context.Context, msg *IncomingMessage, ref, text string) (string, error) {
	if msg.ProgressFn != nil {
		msg.ProgressFn(fmt.Sprintf("正在输入到 %s...", ref))
	}
	if err := h.manager.TypeRef(msg.UserID, ref, text); err != nil {
		return fmt.Sprintf("输入到 %s 失败：%v", ref, err), nil
	}
	return fmt.Sprintf("已在 %s 输入：%s", ref, text), nil
}

// handleScreenshot 截图后传给 Claude 分析。
func (h *browseHandler) handleScreenshot(ctx context.Context, msg *IncomingMessage, url, instruction string) (string, error) {
	if msg.ProgressFn != nil {
		msg.ProgressFn("正在截图...")
	}

	imgPath, err := h.manager.Screenshot(msg.UserID)
	if err != nil {
		h.logger.Error("browser screenshot failed", "err", err)
		return fmt.Sprintf("截图失败：%v", err), nil
	}

	prompt := buildPrompt(url, instruction, true)

	userPref, _ := h.store.GetModelPreference(msg.UserID)
	modelKey := h.selector.SelectModel(userPref, prompt, 1, 0)
	modelName := h.selector.GetModelName(modelKey)

	result, err := h.runner.RunWithModel(ctx, prompt, "", "", []string{imgPath}, modelName, msg.ProgressFn)
	if err != nil {
		return fmt.Sprintf("AI 分析失败：%v", err), nil
	}
	return result.Text, nil
}

// handleText 获取页面文字后传给 Claude 分析。
func (h *browseHandler) handleText(ctx context.Context, msg *IncomingMessage, url, instruction string) (string, error) {
	if msg.ProgressFn != nil {
		msg.ProgressFn("正在读取页面内容...")
	}

	text, err := h.manager.GetText(msg.UserID)
	if err != nil {
		h.logger.Error("browser get text failed", "err", err)
		return fmt.Sprintf("获取页面内容失败：%v", err), nil
	}

	const maxLen = 8000
	if len([]rune(text)) > maxLen {
		runes := []rune(text)
		text = string(runes[:maxLen]) + "\n...(内容已截断)"
	}

	prompt := buildPrompt(url, instruction, false) + "\n\n页面内容：\n" + text

	userPref, _ := h.store.GetModelPreference(msg.UserID)
	modelKey := h.selector.SelectModel(userPref, prompt, 0, 0)
	modelName := h.selector.GetModelName(modelKey)

	result, err := h.runner.RunWithModel(ctx, prompt, "", "", nil, modelName, msg.ProgressFn)
	if err != nil {
		return fmt.Sprintf("AI 分析失败：%v", err), nil
	}
	return result.Text, nil
}

// parseArgs 从 "/browse <url> [instruction]" 的 args 部分分离出 url 和指令。
func parseArgs(args string) (url, instruction string) {
	parts := strings.SplitN(args, " ", 2)
	url = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		instruction = strings.TrimSpace(parts[1])
	}
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	return url, instruction
}

// buildPrompt 根据 url、指令和是否截图构造 Claude prompt。
func buildPrompt(url, instruction string, isScreenshot bool) string {
	if instruction != "" && !strings.Contains(instruction, "截图") {
		if isScreenshot {
			return fmt.Sprintf("以下是网页 %s 的截图。请根据指令完成任务：%s", url, instruction)
		}
		return fmt.Sprintf("以下是网页 %s 的文字内容。请根据指令完成任务：%s", url, instruction)
	}
	if isScreenshot {
		return fmt.Sprintf("以下是网页 %s 的截图。请描述页面内容，提取关键信息并做简洁总结。", url)
	}
	return fmt.Sprintf("以下是网页 %s 的文字内容。请提取关键信息并做简洁总结，用中文回复。", url)
}
