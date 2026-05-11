package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/1344011985/MyselfAI/internal/claude"
	"github.com/1344011985/MyselfAI/internal/imageutil"
	"github.com/1344011985/MyselfAI/internal/memory"
)

// --- /ask handler ---

type askHandler struct {
	store        memory.Store
	runner       *claude.Runner
	downloader   *imageutil.Downloader
	selector     *claude.ModelSelector
	systemPrompt string
	logger       Logger
}

// withSystemPrompt returns a shallow copy of the handler with a different systemPrompt.
// The original handler is not modified.
func (h *askHandler) withSystemPrompt(prompt string) *askHandler {
	copy := *h
	copy.systemPrompt = prompt
	return &copy
}

func (h *askHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	prompt := strings.TrimPrefix(msg.Content, "/ask")
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "请提供问题内容，例如：/ask 你好", nil
	}

	sessionID, err := h.store.GetSession(msg.UserID)
	if err != nil {
		h.logger.Error("get session failed", "user_id", msg.UserID, "err", err)
		sessionID = ""
	}

	memories, err := h.store.GetMemories(msg.UserID)
	if err != nil {
		h.logger.Error("get memories failed", "user_id", msg.UserID, "err", err)
		memories = nil
	}

	var promptParts []string
	if h.systemPrompt != "" {
		promptParts = append(promptParts, h.systemPrompt)
	}
	if len(memories) > 0 {
		promptParts = append(promptParts, "## 用户个人记忆\n"+strings.Join(memories, "\n"))
	}
	systemPrompt := strings.Join(promptParts, "\n\n")

	var imagePaths []string
	if h.downloader != nil {
		for _, url := range msg.ImageURLs {
			path, err := h.downloader.Download(url)
			if err != nil {
				h.logger.Error("download image failed", "url", url, "err", err)
				continue
			}
			imagePaths = append(imagePaths, path)
		}
	}

	history, _ := h.store.GetHistory(msg.UserID, 100)
	conversationTurns := len(history)

	userPref, _ := h.store.GetModelPreference(msg.UserID)
	modelKey := h.selector.SelectModel(userPref, prompt, len(imagePaths), conversationTurns)
	modelName := h.selector.GetModelName(modelKey)

	h.logger.Info("selected model", "user_id", msg.UserID, "model", modelKey, "preference", userPref)

	// Run with selected model, pass progress callback for streaming
	result, err := h.runner.RunWithModel(ctx, prompt, sessionID, systemPrompt, imagePaths, modelName, msg.ProgressFn)
	if err != nil {
		// If the session ID is stale (e.g. Claude Code restarted), retry without it
		if sessionID != "" && strings.Contains(err.Error(), "No conversation found") {
			h.logger.Info("stale session ID, retrying without session", "user_id", msg.UserID, "session_id", sessionID)
			if clearErr := h.store.SaveSession(msg.UserID, ""); clearErr != nil {
				h.logger.Error("clear session failed", "err", clearErr)
			}
			result, err = h.runner.RunWithModel(ctx, prompt, "", systemPrompt, imagePaths, modelName, msg.ProgressFn)
		}
		if err != nil {
			return fmt.Sprintf("执行出错：%v", err), nil
		}
	}

	if err := h.store.SaveSession(msg.UserID, result.SessionID); err != nil {
		h.logger.Error("save session failed", "user_id", msg.UserID, "err", err)
	}
	if err := h.store.SaveHistory(msg.UserID, prompt, result.Text); err != nil {
		h.logger.Error("save history failed", "user_id", msg.UserID, "err", err)
	}

	if result.Usage != nil {
		cost := h.selector.CalculateCost(
			modelKey,
			result.Usage.InputTokens,
			result.Usage.OutputTokens,
			result.Usage.CacheCreationTokens,
			result.Usage.CacheReadTokens,
		)
		usageRecord := &memory.UsageRecord{
			UserID:              msg.UserID,
			SessionID:           result.SessionID,
			Model:               modelKey,
			InputTokens:         result.Usage.InputTokens,
			OutputTokens:        result.Usage.OutputTokens,
			CacheCreationTokens: result.Usage.CacheCreationTokens,
			CacheReadTokens:     result.Usage.CacheReadTokens,
			TotalCostUSD:        cost,
			CreatedAt:           time.Now(),
		}
		if err := h.store.RecordUsage(usageRecord); err != nil {
			h.logger.Error("record usage failed", "err", err)
		}
	}

	return result.Text, nil
}
