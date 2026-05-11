package claude

import (
	"regexp"
	"strings"

	"github.com/1344011985/MyselfAI/internal/config"
)

// TaskComplexity represents the complexity level of a user's task.
type TaskComplexity int

const (
	ComplexitySimple  TaskComplexity = iota // 简单任务：打招呼、简单问答
	ComplexityMedium                        // 中等任务：代码问题、解释、分析
	ComplexityComplex                       // 复杂任务：架构设计、重构、代码生成
)

// ModelSelector selects the appropriate Claude model based on task complexity.
type ModelSelector struct {
	cfg *config.Config
}

// NewModelSelector creates a new ModelSelector.
func NewModelSelector(cfg *config.Config) *ModelSelector {
	return &ModelSelector{cfg: cfg}
}

// SelectModel determines which model to use based on task characteristics.
func (s *ModelSelector) SelectModel(
	userPreference string, // "auto", "haiku", "sonnet", "opus", or ""
	content string,
	imageCount int,
	conversationTurns int,
) string {
	// 1. If user has explicit preference (not "auto"), honor it
	if userPreference != "" && userPreference != "auto" {
		if _, ok := s.cfg.Claude.Models[userPreference]; ok {
			return userPreference
		}
	}

	// 2. If auto-select is disabled, use default model
	if !s.cfg.Claude.AutoSelect {
		return s.cfg.Claude.DefaultModel
	}

	// 3. Auto-select based on complexity
	complexity := s.analyzeComplexity(content, imageCount, conversationTurns)

	switch complexity {
	case ComplexitySimple, ComplexityMedium:
		return "sonnet"
	case ComplexityComplex:
		return "opus"
	default:
		return s.cfg.Claude.DefaultModel
	}
}

// GetModelName returns the full model name for a given model key.
func (s *ModelSelector) GetModelName(modelKey string) string {
	if model, ok := s.cfg.Claude.Models[modelKey]; ok {
		return model.Name
	}
	// Fallback to default model
	if defaultModel, ok := s.cfg.Claude.Models[s.cfg.Claude.DefaultModel]; ok {
		return defaultModel.Name
	}
	return "claude-sonnet-4-6"
}

// analyzeComplexity determines task complexity based on various factors.
func (s *ModelSelector) analyzeComplexity(content string, imageCount, conversationTurns int) TaskComplexity {
	// Convert to lowercase for keyword matching
	lower := strings.ToLower(content)
	contentLen := len([]rune(content))

	// Check for simple task indicators
	if s.isSimpleTask(lower, contentLen, imageCount, conversationTurns) {
		return ComplexitySimple
	}

	// Check for complex task indicators
	if s.isComplexTask(lower, contentLen, imageCount, conversationTurns) {
		return ComplexityComplex
	}

	// Default to medium complexity
	return ComplexityMedium
}

// isSimpleTask checks if the task is simple enough for haiku.
func (s *ModelSelector) isSimpleTask(lower string, contentLen, imageCount, conversationTurns int) bool {
	// Short conversations with simple content
	if conversationTurns < 3 && contentLen < 100 && imageCount == 0 {
		return true
	}

	// Common simple commands/queries
	simplePatterns := []string{
		"^你好", "^hi", "^hello", "^嗨",
		"^谢谢", "^thanks",
		"^什么是", "^who is", "^what is",
		"^怎么", "^how to",
	}

	for _, pattern := range simplePatterns {
		if matched, _ := regexp.MatchString(pattern, lower); matched {
			return true
		}
	}

	// Command messages (starting with /)
	if strings.HasPrefix(lower, "/") {
		// Except /ask with complex content
		if strings.HasPrefix(lower, "/ask") && contentLen > 100 {
			return false
		}
		return true
	}

	return false
}

// isComplexTask checks if the task requires opus.
func (s *ModelSelector) isComplexTask(lower string, contentLen, imageCount, conversationTurns int) bool {
	// Long conversations or multiple images
	if conversationTurns > 10 || imageCount > 2 {
		return true
	}

	// Very long content
	if contentLen > 500 {
		return true
	}

	// Complex task keywords
	complexKeywords := []string{
		"设计", "架构", "重构", "优化整个", "完整的",
		"详细", "深入", "全面",
		"debug", "调试", "排查", "分析问题",
		"实现", "开发", "编写完整",
		"review", "code review", "代码审查",
	}

	for _, keyword := range complexKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}

	// Code-related keywords with sufficient length
	if contentLen > 200 {
		codeKeywords := []string{
			"代码", "函数", "class", "interface",
			"算法", "数据结构",
			"bug", "error", "错误",
		}
		for _, keyword := range codeKeywords {
			if strings.Contains(lower, keyword) {
				return true
			}
		}
	}

	return false
}

// CalculateCost calculates the cost in USD for the given token usage.
func (s *ModelSelector) CalculateCost(
	modelKey string,
	inputTokens, outputTokens, cacheWriteTokens, cacheReadTokens int,
) float64 {
	model, ok := s.cfg.Claude.Models[modelKey]
	if !ok {
		return 0
	}

	cost := 0.0
	cost += float64(inputTokens) * model.InputPriceMTok / 1_000_000
	cost += float64(outputTokens) * model.OutputPriceMTok / 1_000_000
	cost += float64(cacheWriteTokens) * model.CacheWriteMTok / 1_000_000
	cost += float64(cacheReadTokens) * model.CacheReadMTok / 1_000_000

	return cost
}
