package newsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SearchEngine defines the interface for different search engines
type SearchEngine interface {
	Search(ctx context.Context, query string) ([]NewsItem, error)
	Name() string
}

// NewsItem represents a single news item
type NewsItem struct {
	Title       string
	Description string
	URL         string
	Source      string
	PublishedAt string
}

// Searcher aggregates multiple search engines
type Searcher struct {
	engines []SearchEngine
	client  *http.Client
}

// NewSearcher creates a new news searcher with default engines
func NewSearcher() *Searcher {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	return &Searcher{
		engines: []SearchEngine{
			&BingNewsSearcher{client: client},
			&BaiduNewsSearcher{client: client},
		},
		client: client,
	}
}

// Search searches for news using available engines
func (s *Searcher) Search(ctx context.Context, query string) ([]NewsItem, error) {
	if query == "" {
		query = "热点新闻"
	}

	// Try engines one by one until one succeeds
	var lastErr error
	for _, engine := range s.engines {
		items, err := engine.Search(ctx, query)
		if err == nil && len(items) > 0 {
			return items, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all search engines failed: %w", lastErr)
	}
	return nil, fmt.Errorf("no results found")
}

// BingNewsSearcher searches Bing News
type BingNewsSearcher struct {
	client *http.Client
}

func (b *BingNewsSearcher) Name() string {
	return "Bing News"
}

func (b *BingNewsSearcher) Search(ctx context.Context, query string) ([]NewsItem, error) {
	// Use Bing News RSS feed
	searchURL := fmt.Sprintf("https://www.bing.com/news/search?q=%s&format=rss", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bing news returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Parse RSS feed (simplified - just extract basic info)
	return parseRSSFeed(string(body), "Bing News")
}

// BaiduNewsSearcher searches Baidu News
type BaiduNewsSearcher struct {
	client *http.Client
}

func (b *BaiduNewsSearcher) Name() string {
	return "Baidu News"
}

func (b *BaiduNewsSearcher) Search(ctx context.Context, query string) ([]NewsItem, error) {
	// Use Baidu News search
	searchURL := fmt.Sprintf("https://www.baidu.com/s?rtt=1&bsst=1&tn=news&word=%s", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("baidu news returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Parse HTML (simplified - extract basic news items)
	return parseBaiduNews(string(body))
}

// parseRSSFeed parses a simple RSS feed
func parseRSSFeed(content, source string) ([]NewsItem, error) {
	var items []NewsItem

	// Very basic RSS parsing - looking for <item> tags
	itemSections := strings.Split(content, "<item>")
	for i, section := range itemSections {
		if i == 0 {
			continue // Skip header
		}

		item := NewsItem{Source: source}

		// Extract title
		if start := strings.Index(section, "<title>"); start != -1 {
			start += 7
			if end := strings.Index(section[start:], "</title>"); end != -1 {
				item.Title = cleanHTML(section[start : start+end])
			}
		}

		// Extract description
		if start := strings.Index(section, "<description>"); start != -1 {
			start += 13
			if end := strings.Index(section[start:], "</description>"); end != -1 {
				item.Description = cleanHTML(section[start : start+end])
			}
		}

		// Extract link
		if start := strings.Index(section, "<link>"); start != -1 {
			start += 6
			if end := strings.Index(section[start:], "</link>"); end != -1 {
				item.URL = strings.TrimSpace(section[start : start+end])
			}
		}

		// Extract pubDate
		if start := strings.Index(section, "<pubDate>"); start != -1 {
			start += 9
			if end := strings.Index(section[start:], "</pubDate>"); end != -1 {
				item.PublishedAt = strings.TrimSpace(section[start : start+end])
			}
		}

		if item.Title != "" {
			items = append(items, item)
			if len(items) >= 10 {
				break
			}
		}
	}

	return items, nil
}

// parseBaiduNews parses Baidu news HTML
func parseBaiduNews(content string) ([]NewsItem, error) {
	var items []NewsItem

	// Look for news items in Baidu's HTML structure
	// This is a simplified parser - production would use proper HTML parser
	lines := strings.Split(content, "\n")
	var currentItem *NewsItem

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Look for news titles (Baidu uses class="news-title" or similar)
		if strings.Contains(line, "c-title") || strings.Contains(line, "news-title") {
			if currentItem != nil && currentItem.Title != "" {
				items = append(items, *currentItem)
			}
			currentItem = &NewsItem{Source: "Baidu News"}

			// Extract title from the line
			title := extractTextBetween(line, ">", "<")
			currentItem.Title = cleanHTML(title)
		}

		// Look for URLs
		if currentItem != nil && strings.Contains(line, "href=") {
			href := extractTextBetween(line, "href=\"", "\"")
			if href != "" && !strings.Contains(href, "javascript:") {
				currentItem.URL = href
			}
		}

		// Look for descriptions
		if currentItem != nil && (strings.Contains(line, "c-abstract") || strings.Contains(line, "news-abstract")) {
			desc := extractTextBetween(line, ">", "<")
			currentItem.Description = cleanHTML(desc)
		}

		if len(items) >= 10 {
			break
		}
	}

	if currentItem != nil && currentItem.Title != "" {
		items = append(items, *currentItem)
	}

	return items, nil
}

// extractTextBetween extracts text between two delimiters
func extractTextBetween(text, start, end string) string {
	startIdx := strings.Index(text, start)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(start)

	endIdx := strings.Index(text[startIdx:], end)
	if endIdx == -1 {
		return ""
	}

	return text[startIdx : startIdx+endIdx]
}

// cleanHTML removes HTML tags and decodes entities
func cleanHTML(s string) string {
	// Remove CDATA
	s = strings.ReplaceAll(s, "<![CDATA[", "")
	s = strings.ReplaceAll(s, "]]>", "")

	// Remove HTML tags
	for strings.Contains(s, "<") && strings.Contains(s, ">") {
		start := strings.Index(s, "<")
		end := strings.Index(s[start:], ">")
		if end == -1 {
			break
		}
		s = s[:start] + s[start+end+1:]
	}

	// Decode common HTML entities
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")

	return strings.TrimSpace(s)
}

// FormatNewsItems formats news items into a readable string
func FormatNewsItems(items []NewsItem) string {
	if len(items) == 0 {
		return "未找到相关新闻"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条新闻：\n\n", len(items)))

	for i, item := range items {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, item.Title))
		if item.Description != "" {
			// Limit description length
			desc := item.Description
			if len(desc) > 100 {
				desc = desc[:100] + "..."
			}
			sb.WriteString(fmt.Sprintf("   %s\n", desc))
		}
		if item.URL != "" {
			sb.WriteString(fmt.Sprintf("   链接: %s\n", item.URL))
		}
		if item.PublishedAt != "" {
			sb.WriteString(fmt.Sprintf("   发布时间: %s\n", item.PublishedAt))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

// GetHotNews gets hot/trending news without specific query
func (s *Searcher) GetHotNews(ctx context.Context) (string, error) {
	// Try to get hot news from a reliable source
	items, err := s.getFromAPI(ctx)
	if err != nil || len(items) == 0 {
		// Fallback to search
		items, err = s.Search(ctx, "今日热点")
		if err != nil {
			return "", err
		}
	}

	return FormatNewsItems(items), nil
}

// getFromAPI tries to get news from a free news API
func (s *Searcher) getFromAPI(ctx context.Context) ([]NewsItem, error) {
	// Try using a simple news aggregator API
	// Using Toutiao hot search as an example (you may need to adjust based on availability)
	apiURL := "https://www.toutiao.com/hot-event/hot-board/?origin=toutiao_pc"

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Try to parse JSON response
	var result struct {
		Data []struct {
			Title     string `json:"Title"`
			ClusterID string `json:"ClusterId"`
			HotValue  string `json:"HotValue"`
			Image     string `json:"Image"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	var items []NewsItem
	for _, item := range result.Data {
		if item.Title == "" {
			continue
		}

		newsItem := NewsItem{
			Title:       item.Title,
			Description: fmt.Sprintf("热度: %s", item.HotValue),
			URL:         fmt.Sprintf("https://www.toutiao.com/trending/%s/", item.ClusterID),
			Source:      "今日头条热榜",
		}
		items = append(items, newsItem)

		if len(items) >= 10 {
			break
		}
	}

	return items, nil
}
