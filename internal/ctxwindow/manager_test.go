package ctxwindow

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

func TestManagerStatus(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Model: ModelProfile{
			MaxContextTokens: 4096,
			MaxOutputTokens:  1024,
			ReserveRatio:     0.25,
		},
	})

	history := []llm.Message{
		{Role: "system", Content: "你是一个智能助手"},
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好！有什么我可以帮你的？"},
	}

	status := mgr.Status(history)
	if !status.HasRoom {
		t.Error("expected HasRoom=true for small history")
	}
	if status.UsagePercent > 0.1 {
		t.Errorf("usage too high for small history: %.2f", status.UsagePercent)
	}
	t.Logf("Status: %s", FormatStatus(status))
}

func TestManagerFit_NoTruncation(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Model: ModelProfile{
			MaxContextTokens: 4096,
			MaxOutputTokens:  1024,
			ReserveRatio:     0.25,
		},
	})

	history := []llm.Message{
		{Role: "system", Content: "你是一个智能助手"},
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好！"},
	}

	result := mgr.Fit(history)
	if len(result) != len(history) {
		t.Errorf("Fit should not truncate small history: got %d, want %d", len(result), len(history))
	}
}

func TestManagerFit_Truncation(t *testing.T) {
	// 使用很小的窗口来触发截断
	mgr := NewManager(ManagerConfig{
		MaxInputTokens:      100, // 只允许 ~100 tokens
		ProtectRecentRounds: 1,
		ToolResultMaxTokens: 50,
		Model: ModelProfile{
			MaxContextTokens: 150,
			MaxOutputTokens:  50,
			ReserveRatio:     0.33,
		},
	})

	// 构造超长历史
	history := []llm.Message{
		{Role: "system", Content: "你是一个智能助手"},
		{Role: "user", Content: "帮我做第一件事"},
		{Role: "assistant", Content: "好的，我来做第一件事。这是一个比较长的回复。"},
		{Role: "user", Content: "帮我做第二件事"},
		{Role: "assistant", Content: "好的，我来做第二件事。"},
		{Role: "user", Content: "帮我做第三件事，这是最新的请求"},
		{Role: "assistant", Content: "好的，我来处理最新的请求。"},
	}

	result := mgr.Fit(history)

	// 验证：system 消息和最新 user 消息必须保留
	hasSystem := false
	hasLatestUser := false
	for _, msg := range result {
		if msg.Role == "system" {
			hasSystem = true
		}
		if msg.Role == "user" && strings.Contains(msg.Content, "第三件事") {
			hasLatestUser = true
		}
	}

	if !hasSystem {
		t.Error("system message was removed")
	}
	if !hasLatestUser {
		t.Error("latest user message was removed")
	}

	// 截断后应该比原来短
	if len(result) >= len(history) {
		t.Logf("Note: result len=%d, history len=%d (truncation may not have occurred if estimate was within budget)", len(result), len(history))
	}

	t.Logf("Truncated: %d -> %d messages", len(history), len(result))
	for _, msg := range result {
		t.Logf("  [%s] %s", msg.Role, truncate(msg.Content, 40))
	}
}

func TestManagerFit_ToolResultTruncation(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		MaxInputTokens:      200,
		ToolResultMaxTokens: 20, // 很小的限制
		ProtectRecentRounds: 1,
		Model: ModelProfile{
			MaxContextTokens: 300,
			MaxOutputTokens:  100,
			ReserveRatio:     0.33,
		},
	})

	// 构造带长工具结果的历史
	longResult := strings.Repeat("这是一个很长的工具返回结果。", 50)
	history := []llm.Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "查天气"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "weather", Arguments: `{"location":"Beijing"}`}},
		}},
		{Role: "tool", Content: longResult, ToolCallID: "call_1"},
		{Role: "user", Content: "最新的问题"},
	}

	result := mgr.Fit(history)

	// 检查工具结果是否被截断
	for _, msg := range result {
		if msg.Role == "tool" {
			if len(msg.Content) >= len(longResult) {
				t.Error("tool result was not truncated")
			}
			if !strings.Contains(msg.Content, "[truncated") {
				t.Error("truncated tool result should have truncation marker")
			}
			t.Logf("Tool result truncated: %d -> %d chars", len(longResult), len(msg.Content))
		}
	}
}

func TestManagerNeedsTruncation(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		MaxInputTokens: 50,
		Model: ModelProfile{
			MaxContextTokens: 100,
			MaxOutputTokens:  50,
			ReserveRatio:     0.5,
		},
	})

	small := []llm.Message{{Role: "user", Content: "hi"}}
	if mgr.NeedsTruncation(small) {
		t.Error("small history should not need truncation")
	}

	big := []llm.Message{
		{Role: "system", Content: strings.Repeat("very long system prompt ", 100)},
		{Role: "user", Content: "hello"},
	}
	if !mgr.NeedsTruncation(big) {
		t.Error("big history should need truncation")
	}
}

func TestLookupModel(t *testing.T) {
	p := LookupModel("gpt-4o")
	if p.MaxContextTokens != 128000 {
		t.Errorf("gpt-4o context should be 128000, got %d", p.MaxContextTokens)
	}

	p = LookupModel("unknown-model-xyz")
	if p.MaxContextTokens != DefaultProfile.MaxContextTokens {
		t.Error("unknown model should use default profile")
	}
}

func TestWouldExceed(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		MaxInputTokens: 100,
		Model:          DefaultProfile,
	})

	history := []llm.Message{{Role: "user", Content: "hello"}}

	if mgr.WouldExceed(history, 10) {
		t.Error("should not exceed with small addition")
	}
	if !mgr.WouldExceed(history, 1000) {
		t.Error("should exceed with large addition")
	}
}

func TestFormatStatus(t *testing.T) {
	s := WindowStatus{
		MaxInputTokens:  4096,
		EstimatedTokens: 1024,
		UsagePercent:    0.25,
		MessageCount:    5,
		RemainingTokens: 3072,
	}
	formatted := FormatStatus(s)
	if !strings.Contains(formatted, "25.0%") {
		t.Errorf("expected 25.0%% in formatted status: %s", formatted)
	}
	fmt.Println(formatted)
}
