package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/1344011985/MyselfAI/internal/config"
	"github.com/1344011985/MyselfAI/internal/loop"
)

type LoopNotifier struct {
	appID     string
	appSecret string
	logger    *slog.Logger
}

func NewLoopNotifier(cfg *config.Config, logger *slog.Logger) *LoopNotifier {
	if cfg == nil || cfg.Feishu.AppID == "" || cfg.Feishu.AppSecret == "" {
		return nil
	}
	return &LoopNotifier{
		appID:     cfg.Feishu.AppID,
		appSecret: cfg.Feishu.AppSecret,
		logger:    logger,
	}
}

func (n *LoopNotifier) Notify(ctx context.Context, target loop.NotifyTarget, content string) error {
	if n == nil {
		return nil
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	receiveID := strings.TrimSpace(target.GroupID)
	receiveIDType := "chat_id"
	if receiveID == "" {
		receiveID = strings.TrimSpace(target.UserID)
		receiveIDType = "open_id"
	}
	if receiveID == "" {
		return fmt.Errorf("empty feishu receive id")
	}
	if err := sendFeishuText(ctx, n.appID, n.appSecret, receiveID, receiveIDType, content); err != nil {
		return err
	}
	if n.logger != nil {
		n.logger.Info("feishu loop notify sent", "receive_id_type", receiveIDType)
	}
	return nil
}

func sendFeishuText(ctx context.Context, appID, appSecret, receiveID, receiveIDType, content string) error {
	contentJSON, _ := json.Marshal(map[string]string{"text": content})
	payload, _ := json.Marshal(map[string]interface{}{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(contentJSON),
	})

	for attempt := 0; attempt < 2; attempt++ {
		token, err := getTenantAccessToken(appID, appSecret)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "POST",
			fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=%s", receiveIDType),
			strings.NewReader(string(payload)),
		)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("send feishu loop notify: %w", err)
		}
		var result struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&result)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return fmt.Errorf("decode feishu send text response: %w", decodeErr)
		}
		if result.Code == 0 {
			return nil
		}
		if attempt == 0 && isInvalidAccessToken(result.Code, result.Msg) {
			invalidateTenantAccessToken(appID)
			continue
		}
		return fmt.Errorf("feishu send text failed: code=%d msg=%s", result.Code, result.Msg)
	}
	return fmt.Errorf("feishu send text failed")
}
