package feishu

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// streamingSession manages a Feishu CardKit streaming card session.
// It provides real-time typewriter-effect output by updating card content incrementally.
type streamingSession struct {
	appID     string
	appSecret string
	cardID    string
	messageID string
	sequence  int
	closed    bool
	mu        sync.Mutex

	// Throttle: avoid updating more than 10 times/sec
	lastUpdateTime time.Time
	pendingText    string
	updateInterval time.Duration

	log func(string)
}

// tokenCacheMu and tokenCacheMap cache tenant access tokens.
var (
	tokenCacheMu  sync.Mutex
	tokenCacheMap = make(map[string]tokenEntry)
)

type tokenEntry struct {
	token     string
	expiresAt time.Time
}

func getTenantAccessToken(appID, appSecret string) (string, error) {
	cacheKey := appID
	tokenCacheMu.Lock()
	if entry, ok := tokenCacheMap[cacheKey]; ok && time.Now().Add(60*time.Second).Before(entry.expiresAt) {
		tokenCacheMu.Unlock()
		return entry.token, nil
	}
	tokenCacheMu.Unlock()

	payload, _ := json.Marshal(map[string]string{"app_id": appID, "app_secret": appSecret})
	resp, err := http.Post(
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		"application/json",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return "", fmt.Errorf("fetch token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.Code != 0 || result.TenantAccessToken == "" {
		return "", fmt.Errorf("token error: code=%d msg=%s", result.Code, result.Msg)
	}

	expireAt := time.Now().Add(time.Duration(result.Expire) * time.Second)
	tokenCacheMu.Lock()
	tokenCacheMap[cacheKey] = tokenEntry{token: result.TenantAccessToken, expiresAt: expireAt}
	tokenCacheMu.Unlock()

	return result.TenantAccessToken, nil
}

func invalidateTenantAccessToken(appID string) {
	tokenCacheMu.Lock()
	delete(tokenCacheMap, appID)
	tokenCacheMu.Unlock()
}

func isInvalidAccessToken(code int, msg string) bool {
	return code == 99991663 || strings.Contains(strings.ToLower(msg), "invalid access token")
}

// newStreamingSession creates and starts a CardKit streaming card.
// receiveID is the chat_id or open_id; receiveIDType is "chat_id" or "open_id".
// Returns nil if CardKit API is unavailable (falls back gracefully).
func newStreamingSession(appID, appSecret, receiveID, receiveIDType string, log func(string)) *streamingSession {
	s := &streamingSession{
		appID:          appID,
		appSecret:      appSecret,
		sequence:       1,
		updateInterval: 100 * time.Millisecond,
		log:            log,
	}

	if err := s.start(receiveID, receiveIDType); err != nil {
		if log != nil {
			log(fmt.Sprintf("feishu: streaming card start failed (falling back): %v", err))
		}
		return nil
	}
	return s
}

func (s *streamingSession) start(receiveID, receiveIDType string) error {
	token, err := getTenantAccessToken(s.appID, s.appSecret)
	if err != nil {
		return err
	}

	// Step 1: Create card entity with streaming_mode=true
	innerCard := map[string]interface{}{
		"schema": "2.0",
		"config": map[string]interface{}{
			"streaming_mode": true,
			"summary":        map[string]interface{}{"content": "[生成中...]"},
			"streaming_config": map[string]interface{}{
				"print_frequency_ms": map[string]interface{}{"default": 50},
				"print_step":         map[string]interface{}{"default": 2},
			},
		},
		"body": map[string]interface{}{
			"elements": []map[string]interface{}{
				{
					"tag":        "markdown",
					"content":    "",
					"element_id": "content",
				},
			},
		},
	}
	innerCardJSON, _ := json.Marshal(innerCard)
	createPayload, _ := json.Marshal(map[string]string{
		"type": "card_json",
		"data": string(innerCardJSON),
	})

	req, _ := http.NewRequest("POST", "https://open.feishu.cn/open-apis/cardkit/v1/cards", strings.NewReader(string(createPayload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("create card: %w", err)
	}
	defer resp.Body.Close()

	var createResult struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			CardID string `json:"card_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResult); err != nil {
		return fmt.Errorf("decode create response: %w", err)
	}
	if createResult.Code != 0 || createResult.Data.CardID == "" {
		return fmt.Errorf("create card failed: code=%d msg=%s", createResult.Code, createResult.Msg)
	}
	s.cardID = createResult.Data.CardID

	// Step 2: Send the card as a message
	sendPayload, _ := json.Marshal(map[string]interface{}{
		"receive_id": receiveID,
		"msg_type":   "interactive",
		"content":    fmt.Sprintf(`{"type":"card","data":{"card_id":"%s"}}`, s.cardID),
	})

	token2, _ := getTenantAccessToken(s.appID, s.appSecret)
	req2, _ := http.NewRequest("POST",
		fmt.Sprintf("https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=%s", receiveIDType),
		strings.NewReader(string(sendPayload)),
	)
	req2.Header.Set("Authorization", "Bearer "+token2)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return fmt.Errorf("send card: %w", err)
	}
	defer resp2.Body.Close()

	var sendResult struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&sendResult); err != nil {
		return fmt.Errorf("decode send response: %w", err)
	}
	if sendResult.Code != 0 || sendResult.Data.MessageID == "" {
		return fmt.Errorf("send card failed: code=%d msg=%s", sendResult.Code, sendResult.Msg)
	}
	s.messageID = sendResult.Data.MessageID

	if s.log != nil {
		s.log(fmt.Sprintf("feishu: streaming card started cardID=%s messageID=%s", s.cardID, s.messageID))
	}
	return nil
}

// update updates the card content with new text (throttled to ~10fps).
func (s *streamingSession) update(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.cardID == "" {
		return
	}

	now := time.Now()
	if now.Sub(s.lastUpdateTime) < s.updateInterval {
		s.pendingText = text
		return
	}

	s.pendingText = ""
	s.lastUpdateTime = now
	s.sequence++
	seq := s.sequence

	go s.doUpdate(text, seq)
}

func (s *streamingSession) doUpdate(text string, seq int) {
	token, err := getTenantAccessToken(s.appID, s.appSecret)
	if err != nil {
		return
	}

	body, _ := json.Marshal(map[string]interface{}{
		"content":  text,
		"sequence": seq,
		"uuid":     fmt.Sprintf("s_%s_%d", s.cardID, seq),
	})

	url := fmt.Sprintf("https://open.feishu.cn/open-apis/cardkit/v1/cards/%s/elements/content/content", s.cardID)
	req, _ := http.NewRequest("PUT", url, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if s.log != nil {
			s.log(fmt.Sprintf("feishu: streaming update error: %v", err))
		}
		return
	}
	resp.Body.Close()
}

// close finalizes the streaming card and disables streaming mode.
func (s *streamingSession) close(finalText string) {
	s.mu.Lock()
	if s.closed || s.cardID == "" {
		s.mu.Unlock()
		return
	}
	s.closed = true
	text := finalText
	if text == "" && s.pendingText != "" {
		text = s.pendingText
	}
	s.sequence++
	seq := s.sequence
	s.mu.Unlock()

	// Send final content update synchronously
	if text != "" {
		s.doUpdate(text, seq)
		s.mu.Lock()
		s.sequence++
		seq = s.sequence
		s.mu.Unlock()
	}

	// Wait 200ms to ensure the content update arrives before the settings PATCH
	time.Sleep(200 * time.Millisecond)

	// Close streaming mode
	token, err := getTenantAccessToken(s.appID, s.appSecret)
	if err != nil {
		return
	}

	summary := truncateSummary(text, 50)
	settingsJSON, _ := json.Marshal(map[string]interface{}{
		"config": map[string]interface{}{
			"streaming_mode": false,
			"summary":        map[string]interface{}{"content": summary},
		},
	})

	body, _ := json.Marshal(map[string]interface{}{
		"settings": string(settingsJSON),
		"sequence": seq,
		"uuid":     fmt.Sprintf("c_%s_%d", s.cardID, seq),
	})

	url := fmt.Sprintf("https://open.feishu.cn/open-apis/cardkit/v1/cards/%s/settings", s.cardID)
	req, _ := http.NewRequest("PATCH", url, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if s.log != nil {
			s.log(fmt.Sprintf("feishu: streaming close error: %v", err))
		}
		return
	}
	resp.Body.Close()

	if s.log != nil {
		s.log(fmt.Sprintf("feishu: streaming card closed cardID=%s", s.cardID))
	}
}

func truncateSummary(text string, max int) string {
	if text == "" {
		return ""
	}
	clean := strings.ReplaceAll(text, "\n", " ")
	clean = strings.TrimSpace(clean)
	runes := []rune(clean)
	if len(runes) <= max {
		return clean
	}
	return string(runes[:max-3]) + "..."
}
