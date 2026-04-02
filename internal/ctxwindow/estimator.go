package ctxwindow

import (
	"encoding/json"
	"unicode/utf8"
)

// TokenEstimator 估算文本的 token 数量
// 使用启发式方法，无需外部 tokenizer 依赖
// 对英文约 1 token ≈ 4 chars，中文约 1 token ≈ 1.5 chars
// 误差范围 ±15%，足够做上下文窗口管理决策
type TokenEstimator struct {
	// OverheadPerMessage 每条消息的固定开销 token 数
	// OpenAI 格式中每条消息有 role/content 等字段开销
	OverheadPerMessage int

	// OverheadPerToolCall 每次工具调用的额外开销
	OverheadPerToolCall int
}

// DefaultEstimator 返回默认配置的估算器
func DefaultEstimator() *TokenEstimator {
	return &TokenEstimator{
		OverheadPerMessage:  4, // <|im_start|>role\ncontent<|im_end|>
		OverheadPerToolCall: 8, // function name + JSON 结构开销
	}
}

// EstimateText 估算纯文本的 token 数
func (e *TokenEstimator) EstimateText(text string) int {
	if text == "" {
		return 0
	}

	tokens := 0
	cjkCount := 0
	asciiCount := 0

	for _, r := range text {
		if isCJKRune(r) {
			cjkCount++
		} else if r < 128 {
			asciiCount++
		} else {
			// 其他 Unicode 字符，按 CJK 处理
			cjkCount++
		}
	}

	// 英文：约 4 字符 = 1 token
	// 中文：约 1.5 字符 = 1 token（实际每个汉字 1-2 token）
	tokens += (asciiCount + 3) / 4   // 向上取整
	tokens += (cjkCount*2 + 2) / 3   // 中文偏保守估算

	if tokens == 0 && utf8.RuneCountInString(text) > 0 {
		tokens = 1
	}

	return tokens
}

// EstimateJSON 估算 JSON 数据的 token 数
func (e *TokenEstimator) EstimateJSON(data string) int {
	// JSON 中的结构字符（{, }, [, ], :, ,）通常合并在 token 中
	// 大约比纯文本多 10-20% 的开销
	base := e.EstimateText(data)
	return base + base/8 // +12.5%
}

// EstimateMessage 估算单条消息的 token 数
func (e *TokenEstimator) EstimateMessage(role, content string, toolCalls int) int {
	tokens := e.OverheadPerMessage
	tokens += e.EstimateText(role)
	tokens += e.EstimateText(content)
	tokens += toolCalls * e.OverheadPerToolCall
	return tokens
}

// isCJKRune 判断是否为 CJK 字符
func isCJKRune(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||   // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) ||   // CJK Unified Ideographs Extension A
		(r >= 0xF900 && r <= 0xFAFF) ||   // CJK Compatibility Ideographs
		(r >= 0x3000 && r <= 0x303F) ||   // CJK Symbols and Punctuation
		(r >= 0xFF00 && r <= 0xFFEF) ||   // Fullwidth Forms
		(r >= 0x3040 && r <= 0x309F) ||   // Hiragana
		(r >= 0x30A0 && r <= 0x30FF) ||   // Katakana
		(r >= 0xAC00 && r <= 0xD7AF)      // Hangul
}

// EstimateToolCallJSON 估算工具调用的 JSON 参数 token 数
func (e *TokenEstimator) EstimateToolCallJSON(args string) int {
	// 验证是否为有效 JSON
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		// 不是有效 JSON，按文本估算
		return e.EstimateText(args)
	}
	return e.EstimateJSON(args)
}
