package ctxwindow

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ---------- Mock Summarizer ----------

type mockSummarizer struct {
	summary string
	err     error
	calls   int
}

func (m *mockSummarizer) Summarize(ctx context.Context, messages []llm.Message) (string, error) {
	m.calls++
	if m.err != nil {
		return "", m.err
	}
	if m.summary != "" {
		return m.summary, nil
	}
	// 自动生成摘要
	return fmt.Sprintf("Summary of %d messages: conversation about various topics.", len(messages)), nil
}

// ---------- SmartManager 创建辅助 ----------

func newTestSmartManager(maxInput int, summarizer Summarizer) *SmartManager {
	return NewSmartManager(SmartManagerConfig{
		Base: ManagerConfig{
			MaxInputTokens:      maxInput,
			ProtectRecentRounds: 2,
			ToolResultMaxTokens: 100,
			Model: ModelProfile{
				MaxContextTokens: maxInput * 2,
				MaxOutputTokens:  maxInput / 2,
				ReserveRatio:     0.5,
			},
		},
		Compaction: DefaultCompactionConfig(),
		Summarizer: summarizer,
	})
}

// ---------- SmartFit 测试 ----------

func TestSmartFit_NoTruncationNeeded(t *testing.T) {
	sm := newTestSmartManager(5000, &mockSummarizer{})

	history := []llm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	result := sm.SmartFit(context.Background(), history)

	if result.Strategy != "none" {
		t.Errorf("expected strategy 'none', got %q", result.Strategy)
	}
	if result.FinalCount != result.OriginalCount {
		t.Errorf("no messages should be removed: %d -> %d", result.OriginalCount, result.FinalCount)
	}
}

func TestSmartFit_SummaryCompression(t *testing.T) {
	summarizer := &mockSummarizer{
		summary: "User asked about weather in Beijing (25°C, sunny) and code review.",
	}
	sm := newTestSmartManager(200, summarizer)

	// 构造超长历史：多轮旧对话 + 最近 2 轮
	history := buildLongHistory(10)

	result := sm.SmartFit(context.Background(), history)

	if !result.SummaryInserted {
		t.Error("expected summary to be inserted")
	}
	if result.FinalCount >= result.OriginalCount {
		t.Errorf("expected fewer messages after compaction: %d -> %d", result.OriginalCount, result.FinalCount)
	}
	if summarizer.calls == 0 {
		t.Error("summarizer should have been called")
	}

	// 验证摘要消息存在
	hasSummary := false
	for _, msg := range result.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "Earlier conversation summary") {
			hasSummary = true
			break
		}
	}
	if !hasSummary {
		t.Error("result should contain summary message")
	}

	// 验证最新的 user 消息保留
	hasLatestUser := false
	for _, msg := range result.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Round 10") {
			hasLatestUser = true
			break
		}
	}
	if !hasLatestUser {
		t.Error("latest user message should be preserved")
	}

	t.Logf("Compaction: %d -> %d msgs, strategy=%s, summary=%v",
		result.OriginalCount, result.FinalCount, result.Strategy, result.SummaryInserted)
}

func TestSmartFit_SummaryFallback(t *testing.T) {
	summarizer := &mockSummarizer{
		err: fmt.Errorf("LLM unavailable"),
	}
	sm := newTestSmartManager(200, summarizer)

	history := buildLongHistory(10)

	result := sm.SmartFit(context.Background(), history)

	// 摘要失败，应该降级为 fallback 摘要 + 可能的硬裁剪
	if result.FinalCount >= result.OriginalCount {
		t.Errorf("should still truncate even when summarizer fails: %d -> %d",
			result.OriginalCount, result.FinalCount)
	}

	t.Logf("Fallback result: %d -> %d msgs, strategy=%s",
		result.OriginalCount, result.FinalCount, result.Strategy)
}

func TestSmartFit_NoSummarizer(t *testing.T) {
	sm := newTestSmartManager(200, nil) // 无 summarizer

	history := buildLongHistory(10)

	result := sm.SmartFit(context.Background(), history)

	// 无 summarizer 应该直接硬裁剪
	if result.SummaryInserted {
		t.Error("should not insert summary without summarizer")
	}
	if result.Strategy != "truncate" {
		t.Errorf("expected strategy 'truncate', got %q", result.Strategy)
	}
}

func TestSmartFit_ToolResultTruncation(t *testing.T) {
	sm := newTestSmartManager(300, &mockSummarizer{})

	longTool := strings.Repeat("This is a very long tool result. ", 100)
	history := []llm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Search for info"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "search", Arguments: `{"q":"test"}`}},
		}},
		{Role: "tool", Content: longTool, ToolCallID: "call_1"},
		{Role: "assistant", Content: "Here's what I found."},
		{Role: "user", Content: "Tell me more"},
	}

	result := sm.SmartFit(context.Background(), history)

	// 工具结果应该被截断
	for _, msg := range result.Messages {
		if msg.Role == "tool" && len(msg.Content) >= len(longTool) {
			t.Error("tool result should be truncated")
		}
	}
}

func TestSmartFit_TooFewMessagesToSummarize(t *testing.T) {
	summarizer := &mockSummarizer{}
	sm := NewSmartManager(SmartManagerConfig{
		Base: ManagerConfig{
			MaxInputTokens:      150,
			ProtectRecentRounds: 2,
			ToolResultMaxTokens: 100,
			Model: ModelProfile{
				MaxContextTokens: 300,
				MaxOutputTokens:  150,
				ReserveRatio:     0.5,
			},
		},
		Compaction: CompactionConfig{
			EnableSummary:          true,
			MinMessagesToSummarize: 20, // 很高的阈值
			SummaryFallback:        true,
			SummaryMaxTokens:       400,
		},
		Summarizer: summarizer,
	})

	history := buildLongHistory(5)

	_ = sm.SmartFit(context.Background(), history)

	// 消息数太少不应该触发摘要
	if summarizer.calls > 0 {
		t.Error("summarizer should not be called when messages below threshold")
	}
}

func TestSmartFit_SummaryTruncation(t *testing.T) {
	// 摘要本身超长，需要截断
	longSummary := strings.Repeat("This is a very detailed summary point. ", 100)
	summarizer := &mockSummarizer{summary: longSummary}

	sm := NewSmartManager(SmartManagerConfig{
		Base: ManagerConfig{
			MaxInputTokens:      300,
			ProtectRecentRounds: 2,
			ToolResultMaxTokens: 100,
			Model: ModelProfile{
				MaxContextTokens: 600,
				MaxOutputTokens:  300,
				ReserveRatio:     0.5,
			},
		},
		Compaction: CompactionConfig{
			EnableSummary:          true,
			SummaryMaxTokens:       50, // 很小的摘要预算
			MinMessagesToSummarize: 2,
			SummaryFallback:        true,
		},
		Summarizer: summarizer,
	})

	history := buildLongHistory(8)

	result := sm.SmartFit(context.Background(), history)

	// 检查摘要被截断了
	for _, msg := range result.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "Earlier conversation summary") {
			summaryContent := msg.Content
			estimator := DefaultEstimator()
			tokens := estimator.EstimateText(summaryContent)
			// 摘要 token 数应该在合理范围内（允许一些开销）
			if tokens > 150 { // 50 token 限制 + 元数据开销
				t.Errorf("summary should be truncated, got %d tokens", tokens)
			}
		}
	}
}

// ---------- PreCheck / PostToolCheck 测试 ----------

func TestPreCheck(t *testing.T) {
	sm := newTestSmartManager(200, &mockSummarizer{})

	history := buildLongHistory(10)

	result := sm.PreCheck(context.Background(), history)

	// PreCheck 应该裁剪超长历史
	tokens := sm.EstimateHistory(result)
	if tokens > 200 {
		t.Errorf("PreCheck result should be within budget, got %d tokens", tokens)
	}
}

func TestPostToolCheck(t *testing.T) {
	sm := newTestSmartManager(200, &mockSummarizer{})

	history := buildLongHistory(10)

	result := sm.PostToolCheck(context.Background(), history)

	tokens := sm.EstimateHistory(result)
	if tokens > 200 {
		t.Errorf("PostToolCheck result should be within budget, got %d tokens", tokens)
	}
}

// ---------- fallbackSummary 测试 ----------

func TestFallbackSummary(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "What's the weather?"},
		{Role: "assistant", Content: "Let me check.", ToolCalls: []llm.ToolCall{
			{ID: "c1", Function: llm.FunctionCall{Name: "weather"}},
		}},
		{Role: "tool", Content: "25°C, sunny", ToolCallID: "c1"},
		{Role: "assistant", Content: "It's 25°C and sunny."},
		{Role: "user", Content: "Thanks!"},
	}

	summary := fallbackSummary(msgs)

	if !strings.Contains(summary, "User:") {
		t.Error("fallback summary should contain user messages")
	}
	if !strings.Contains(summary, "weather") {
		t.Error("fallback summary should contain tool names")
	}

	t.Logf("Fallback summary:\n%s", summary)
}

func TestFallbackSummaryLongMessages(t *testing.T) {
	// 超长消息应该被截断
	longContent := strings.Repeat("x", 200)
	msgs := []llm.Message{
		{Role: "user", Content: longContent},
	}

	summary := fallbackSummary(msgs)

	if len(summary) >= len(longContent) {
		t.Error("fallback summary should truncate long messages")
	}
	if !strings.Contains(summary, "...") {
		t.Error("truncated messages should have ellipsis")
	}
}

// ---------- LLMSummarizer 测试 ----------

type mockLLMClient struct {
	response string
}

func (m *mockLLMClient) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Message:      llm.Message{Role: "assistant", Content: m.response},
		FinishReason: "stop",
	}, nil
}

func TestLLMSummarizer(t *testing.T) {
	client := &mockLLMClient{
		response: "User discussed weather in Beijing (25°C) and requested code review for a Go project.",
	}

	summarizer := NewLLMSummarizer(client)

	msgs := []llm.Message{
		{Role: "user", Content: "What's the weather in Beijing?"},
		{Role: "assistant", Content: "It's 25°C and sunny."},
		{Role: "user", Content: "Please review my Go code."},
		{Role: "assistant", Content: "Sure, I'll take a look."},
	}

	summary, err := summarizer.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}

	if !strings.Contains(summary, "weather") {
		t.Error("summary should mention weather")
	}
	if !strings.Contains(summary, "code review") {
		t.Error("summary should mention code review")
	}
}

// ---------- 端到端场景测试 ----------

func TestSmartFit_EndToEnd_MultiRound(t *testing.T) {
	summarizer := &mockSummarizer{}
	sm := newTestSmartManager(300, summarizer)

	// 模拟真实的多轮对话场景
	history := []llm.Message{
		{Role: "system", Content: "You are a coding assistant."},
		// Round 1: 天气查询
		{Role: "user", Content: "What's the weather in Beijing?"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "weather", Arguments: `{"location":"Beijing"}`}},
		}},
		{Role: "tool", Content: "Beijing: 25°C, sunny, PM2.5: 35", ToolCallID: "c1"},
		{Role: "assistant", Content: "It's 25°C and sunny in Beijing."},
		// Round 2: 代码问题
		{Role: "user", Content: "Write a Go function to sort a slice."},
		{Role: "assistant", Content: "Here's a sort function:\n```go\nfunc sortSlice(s []int) { sort.Ints(s) }\n```"},
		// Round 3: 文件操作
		{Role: "user", Content: "Read the file main.go"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{ID: "c2", Type: "function", Function: llm.FunctionCall{Name: "read_file", Arguments: `{"path":"main.go"}`}},
		}},
		{Role: "tool", Content: strings.Repeat("package main\n", 50), ToolCallID: "c2"},
		{Role: "assistant", Content: "Here's the content of main.go..."},
		// Round 4: 最近的对话（应保护）
		{Role: "user", Content: "Now help me refactor the code."},
		{Role: "assistant", Content: "I'll help you refactor. What specific changes?"},
		// Round 5: 最新（应保护）
		{Role: "user", Content: "Extract the handler function into a separate file."},
	}

	result := sm.SmartFit(context.Background(), history)

	// 验证关键性质
	t.Logf("End-to-end: %d -> %d msgs, tokens: %d -> %d, strategy: %s",
		result.OriginalCount, result.FinalCount,
		result.TokensBefore, result.TokensAfter,
		result.Strategy)

	// 1. system prompt 必须保留
	hasSystem := false
	for _, msg := range result.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "coding assistant") {
			hasSystem = true
			break
		}
	}
	if !hasSystem {
		t.Error("system prompt must be preserved")
	}

	// 2. 最新的 user 消息必须保留
	hasLatest := false
	for _, msg := range result.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Extract the handler") {
			hasLatest = true
			break
		}
	}
	if !hasLatest {
		t.Error("latest user message must be preserved")
	}

	// 3. 结果在 token 预算内
	if result.TokensAfter > 300 {
		t.Errorf("result should be within budget (300), got %d tokens", result.TokensAfter)
	}
}

// ---------- 辅助函数 ----------

func buildLongHistory(rounds int) []llm.Message {
	history := []llm.Message{
		{Role: "system", Content: "You are a helpful assistant."},
	}

	for i := 1; i <= rounds; i++ {
		history = append(history,
			llm.Message{Role: "user", Content: fmt.Sprintf("Round %d: Please help me with task number %d, which involves some complex work.", i, i)},
			llm.Message{Role: "assistant", Content: fmt.Sprintf("Round %d: I'll help you with that task. Here's my detailed response about task %d.", i, i)},
		)
	}

	return history
}
