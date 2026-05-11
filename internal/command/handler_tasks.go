package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/1344011985/MyselfAI/internal/taskqueue"
)

type tasksHandler struct{ queue taskqueue.Queue }

type statusHandler struct{ queue taskqueue.Queue }

type cancelHandler struct{ queue taskqueue.Queue }

func (h *tasksHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	tasks, err := h.queue.ListByUser(msg.UserID, 10)
	if err != nil {
		return "获取任务列表失败，请稍后重试。", nil
	}
	if len(tasks) == 0 {
		return "当前没有任务记录。", nil
	}
	var sb strings.Builder
	for _, t := range tasks {
		fmt.Fprintf(&sb, "%s  %s  %s\n", t.ID, t.Status, shorten(t.Content, 40))
	}
	return strings.TrimSpace(sb.String()), nil
}

func (h *statusHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	parts := strings.Fields(msg.Content)
	if len(parts) < 2 {
		return "用法：/status <task_id>", nil
	}
	t, err := h.queue.Get(parts[1])
	if err != nil {
		return "任务不存在或读取失败。", nil
	}
	if t.UserID != msg.UserID {
		return "只能查看你自己的任务。", nil
	}
	resp := fmt.Sprintf("任务：%s\n状态：%s", t.ID, t.Status)
	if t.Error != "" {
		resp += "\n错误：" + t.Error
	}
	if t.Result != "" {
		resp += "\n结果：\n" + t.Result
	}
	return resp, nil
}

func (h *cancelHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	parts := strings.Fields(msg.Content)
	var t *taskqueue.Task
	var err error
	if len(parts) >= 2 {
		t, err = h.queue.Get(parts[1])
	} else {
		t, err = h.latestActiveTask(msg.UserID)
	}
	if err != nil {
		return "任务不存在或读取失败。", nil
	}
	if t == nil {
		return "当前没有可停止的进行中任务。", nil
	}
	if t.UserID != msg.UserID {
		return "只能取消你自己的任务。", nil
	}
	if err := h.queue.Cancel(t.ID); err != nil {
		return "取消任务失败，请稍后重试。", nil
	}
	return "已提交取消请求：" + t.ID, nil
}

func (h *cancelHandler) latestActiveTask(userID string) (*taskqueue.Task, error) {
	tasks, err := h.queue.ListByUser(userID, 20)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.Status == taskqueue.StatusRunning || t.Status == taskqueue.StatusPending {
			return t, nil
		}
	}
	return nil, nil
}

type verifyHandler struct{ queue taskqueue.Queue }

func (h *verifyHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	parts := strings.Fields(msg.Content)
	if len(parts) < 3 {
		return "用法：/verify <task_id> passed|failed", nil
	}
	taskID := parts[1]
	status := strings.ToLower(parts[2])
	if status != "passed" && status != "failed" {
		return "verify_status 只能是 passed 或 failed", nil
	}
	t, err := h.queue.Get(taskID)
	if err != nil {
		return "任务不存在或读取失败。", nil
	}
	if t.UserID != msg.UserID {
		return "只能操作你自己的任务。", nil
	}
	if err := h.queue.UpdateVerifyStatus(taskID, status); err != nil {
		return "更新验证状态失败，请稍后重试。", nil
	}
	return fmt.Sprintf("任务 %s 验证状态已更新为 %s", taskID, status), nil
}

func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
