package feishu

import (
	"context"

	"github.com/1344011985/MyselfAI/internal/command"
	"github.com/1344011985/MyselfAI/internal/taskqueue"
)

// dispatchAsync 提交异步任务：创建 streaming card、注册 CompletionFn，立即返回不阻塞。
// msgID 用于发 reply 和替换 reaction。
func (b *Bot) dispatchAsync(
	ctx context.Context,
	userID, chatID, msgID, content, receiveID, receiveIDType string,
	isGroup bool,
) {
	// 创建 streaming card
	stream := newStreamingSession(b.cfg.Feishu.AppID, b.cfg.Feishu.AppSecret, receiveID, receiveIDType,
		func(s string) { b.logger.Info(s) })

	var thinkingMsgID string
	if stream == nil {
		// fallback：发 Patch-based thinking card
		thinkingMsgID = b.sendThinkingCard(ctx, msgID)
	}

	// progressFn：实时更新 streaming card
	var lastProgress string
	var progressFn func(string)
	if stream != nil {
		progressFn = func(partial string) {
			lastProgress = partial
			stream.update(partial)
		}
	}

	// completionFn：关闭 streaming card + 发独立文字回复 + 替换 reaction
	completionFn := func(res taskqueue.CompletionResult) {
		defer func() {
			if r := recover(); r != nil {
				b.logger.Error("panic in completionFn", "recover", r)
			}
		}()

		reply := res.Result
		if res.Error != nil {
			reply = "处理失败：" + res.Error.Error()
		}

		if stream != nil {
			finalContent := lastProgress
			if finalContent == "" {
				finalContent = reply
			}
			stream.close(finalContent)
			if reply != "" && msgID != "" {
				if err := b.sendReply(context.Background(), msgID, reply); err != nil {
					b.logger.Error("reply after stream failed, fallback to direct message", "err", err)
					_ = b.sendDirectText(context.Background(), receiveID, receiveIDType, reply)
				}
			}
		} else if thinkingMsgID != "" {
			if err := b.patchCard(context.Background(), thinkingMsgID, reply); err != nil {
				b.logger.Error("patch card failed, sending new reply", "err", err)
				if replyErr := b.sendReply(context.Background(), msgID, reply); replyErr != nil {
					b.logger.Error("reply after patch failure failed, fallback to direct message", "err", replyErr)
					_ = b.sendDirectText(context.Background(), receiveID, receiveIDType, reply)
				}
			}
		} else {
			if err := b.sendReply(context.Background(), msgID, reply); err != nil {
				b.logger.Error("reply failed, fallback to direct message", "err", err)
				_ = b.sendDirectText(context.Background(), receiveID, receiveIDType, reply)
			}
		}

		// EYES → DONE
		if msgID != "" {
			go func() {
				b.removeReaction(context.Background(), msgID, "EYES")
				b.addReaction(context.Background(), msgID, "DONE")
			}()
		}

		// 清理 group history
		if isGroup {
			b.clearHistory(chatID)
		}
	}

	msg := &command.IncomingMessage{
		UserID:     userID,
		GroupID:    chatID,
		Content:    content,
		ProgressFn: progressFn,
	}

	// 裸命令（模型切换、执行器切换等）走同步 Route，不提交异步任务
	if b.router != nil && b.router.IsSyncCommand(content) {
		reply := b.dispatch(context.Background(), userID, chatID, content, progressFn)
		completionFn(taskqueue.CompletionResult{Result: reply})
		return
	}

	if b.router != nil {
		if _, err := b.router.SubmitAsync(ctx, msg, true, completionFn); err != nil {
			b.logger.Warn("submit async failed, fallback to sync dispatch", "err", err)
			reply := b.dispatch(context.Background(), userID, chatID, content, progressFn)
			completionFn(taskqueue.CompletionResult{Result: reply})
		}
	} else {
		reply := b.dispatch(context.Background(), userID, chatID, content, progressFn)
		completionFn(taskqueue.CompletionResult{Result: reply})
	}
}
