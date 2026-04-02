package ctxwindow

import (
	"testing"
)

func TestEstimateText(t *testing.T) {
	e := DefaultEstimator()

	tests := []struct {
		name     string
		text     string
		minToken int
		maxToken int
	}{
		{"empty", "", 0, 0},
		{"short_en", "hello", 1, 3},
		{"short_cn", "你好", 1, 3},
		{"sentence_en", "The quick brown fox jumps over the lazy dog", 8, 14},
		{"sentence_cn", "今天天气真不错，适合出去走走", 8, 22},
		{"mixed", "Hello 你好世界 World", 4, 12},
		{"code", "func main() { fmt.Println(\"hello\") }", 8, 16},
		{"long_text", generateText(1000), 200, 800},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.EstimateText(tt.text)
			if got < tt.minToken || got > tt.maxToken {
				t.Errorf("EstimateText(%q) = %d, want [%d, %d]",
					truncate(tt.text, 50), got, tt.minToken, tt.maxToken)
			}
		})
	}
}

func TestEstimateMessage(t *testing.T) {
	e := DefaultEstimator()

	// 一条普通 user 消息
	tokens := e.EstimateMessage("user", "帮我查一下天气", 0)
	if tokens < 5 || tokens > 20 {
		t.Errorf("EstimateMessage user = %d, want [5, 20]", tokens)
	}

	// 一条带工具调用的 assistant 消息
	tokens = e.EstimateMessage("assistant", "", 2)
	if tokens < 15 || tokens > 30 {
		t.Errorf("EstimateMessage assistant+tools = %d, want [15, 30]", tokens)
	}
}

func generateText(n int) string {
	s := make([]byte, n)
	for i := range s {
		s[i] = 'a' + byte(i%26)
	}
	return string(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
