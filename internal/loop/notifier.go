package loop

import "context"

type NotifyTarget struct {
	UserID  string
	GroupID string
}

type Notifier interface {
	Notify(ctx context.Context, target NotifyTarget, content string) error
}
