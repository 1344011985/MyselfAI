package newsearch

import (
	"context"
	"testing"
	"time"
)

func TestSearcher(t *testing.T) {
	searcher := NewSearcher()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("GetHotNews", func(t *testing.T) {
		result, err := searcher.GetHotNews(ctx)
		if err != nil {
			t.Logf("GetHotNews error: %v", err)
			// Don't fail the test as external services might be unavailable
			return
		}

		if result == "" {
			t.Error("Expected non-empty result")
		}

		t.Logf("Hot news result:\n%s", result)
	})

	t.Run("SearchWithQuery", func(t *testing.T) {
		items, err := searcher.Search(ctx, "科技")
		if err != nil {
			t.Logf("Search error: %v", err)
			// Don't fail the test as external services might be unavailable
			return
		}

		if len(items) == 0 {
			t.Log("No items found, might be network issue")
			return
		}

		t.Logf("Found %d items for query '科技'", len(items))
		for i, item := range items {
			t.Logf("[%d] %s - %s", i+1, item.Title, item.Source)
		}
	})
}

func TestBingNewsSearcher(t *testing.T) {
	bing := &BingNewsSearcher{client: NewSearcher().client}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	items, err := bing.Search(ctx, "technology")
	if err != nil {
		t.Logf("Bing search error: %v (this might be expected if Bing blocks requests)", err)
		return
	}

	if len(items) > 0 {
		t.Logf("Bing found %d items", len(items))
		for i, item := range items {
			if i >= 3 {
				break
			}
			t.Logf("[%d] %s", i+1, item.Title)
		}
	}
}

func TestFormatNewsItems(t *testing.T) {
	items := []NewsItem{
		{
			Title:       "测试新闻标题",
			Description: "这是一条测试新闻的描述内容",
			URL:         "https://example.com/news/1",
			Source:      "测试来源",
			PublishedAt: "2026-02-24",
		},
		{
			Title:       "另一条测试新闻",
			Description: "另一条新闻的描述",
			URL:         "https://example.com/news/2",
			Source:      "测试来源2",
		},
	}

	result := FormatNewsItems(items)
	if result == "" {
		t.Error("Expected non-empty formatted result")
	}

	t.Logf("Formatted result:\n%s", result)

	// Test with empty items
	emptyResult := FormatNewsItems([]NewsItem{})
	if emptyResult != "未找到相关新闻" {
		t.Errorf("Expected '未找到相关新闻', got '%s'", emptyResult)
	}
}

func TestCleanHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "<![CDATA[Test Content]]>",
			expected: "Test Content",
		},
		{
			input:    "<p>Hello <strong>World</strong></p>",
			expected: "Hello World",
		},
		{
			input:    "Test &amp; Example &lt;tag&gt;",
			expected: "Test & Example <tag>",
		},
		{
			input:    "Multiple&nbsp;&nbsp;spaces",
			expected: "Multiple  spaces",
		},
	}

	for _, tt := range tests {
		result := cleanHTML(tt.input)
		if result != tt.expected {
			t.Errorf("cleanHTML(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}
