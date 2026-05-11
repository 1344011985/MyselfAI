package command

import (
	"context"
	"strings"

	"github.com/1344011985/MyselfAI/internal/newsearch"
)

// --- /news handler ---

type newsHandler struct {
	searcher *newsearch.Searcher
	logger   Logger
}

func (h *newsHandler) Handle(ctx context.Context, msg *IncomingMessage) (string, error) {
	query := strings.TrimSpace(strings.TrimPrefix(msg.Content, "/news"))

	h.logger.Info("news search requested", "user_id", msg.UserID, "query", query)

	var result string
	var err error

	if query == "" {
		result, err = h.searcher.GetHotNews(ctx)
	} else {
		items, searchErr := h.searcher.Search(ctx, query)
		if searchErr != nil {
			err = searchErr
		} else {
			result = newsearch.FormatNewsItems(items)
		}
	}

	if err != nil {
		h.logger.Error("news search failed", "err", err)
		return "获取新闻失败，请稍后重试。可能的原因：网络连接问题或搜索引擎不可用。", nil
	}

	return result, nil
}
