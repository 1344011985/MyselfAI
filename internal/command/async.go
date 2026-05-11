package command

import (
	"context"
	"strings"

	"github.com/1344011985/MyselfAI/internal/taskqueue"
)

func (r *Router) SubmitAsync(ctx context.Context, msg *IncomingMessage, continueSession bool, completionFn func(taskqueue.CompletionResult)) (*taskqueue.Task, error) {
	if r.tasks == nil {
		return nil, nil
	}
	content := strings.TrimSpace(msg.Content)
	taskType := taskqueue.TaskTypeChat
	executor := ""
	lower := strings.ToLower(content)
	if strings.HasPrefix(lower, "/codex") {
		executor = "codex"
		content = strings.TrimSpace(content[len("/codex"):])
	} else if strings.HasPrefix(lower, "/claude") {
		executor = "claude"
		content = strings.TrimSpace(content[len("/claude"):])
	} else if strings.HasPrefix(lower, "/kiro") {
		executor = "kiro"
		content = strings.TrimSpace(content[len("/kiro"):])
	} else if strings.HasPrefix(content, "/ask") {
		content = strings.TrimSpace(strings.TrimPrefix(content, "/ask"))
	} else if strings.HasPrefix(content, "/") {
		taskType = taskqueue.TaskTypeCommand
	}
	if content == "" {
		content = strings.TrimSpace(msg.Content)
	}

	// 没有显式 executor 前缀时，读取用户持久化偏好
	if executor == "" {
		if pref, err := r.store.GetExecutorPreference(msg.UserID); err == nil && pref != "" && pref != "claude" {
			executor = pref
		}
	}

	// 提取简短 title：取前 30 个字符
	title := extractTitle(content, 30)

	return r.tasks.Submit(ctx, taskqueue.SubmitRequest{
		UserID:          msg.UserID,
		GroupID:         msg.GroupID,
		Content:         content,
		ContinueSession: continueSession,
		ProgressFn:      msg.ProgressFn,
		CompletionFn:    completionFn,
		Type:            taskType,
		Title:           title,
		Executor:        executor,
	})
}

// extractTitle 从内容中提取简短标题（取第一行前 maxRunes 个字符）
func extractTitle(content string, maxRunes int) string {
	// 取第一行
	line := content
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		line = content[:idx]
	}
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return line
}
