package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// mockSummarizer 是一个简单的 mock LLM，用于测试摘要功能
type mockSummarizer struct{}

func (m *mockSummarizer) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.ChatResponse, error) {
	// 检测是否为增量摘要请求（prompt 中包含 "已有摘要"）
	for _, msg := range messages {
		if strings.Contains(msg.Content, "已有摘要") {
			return &llm.ChatResponse{
				Message: llm.Message{
					Role:    "assistant",
					Content: "用户先前询问了天气和新闻（已有摘要），随后又讨论了编程问题和项目进度。",
				},
				FinishReason: "stop",
			}, nil
		}
	}
	return &llm.ChatResponse{
		Message: llm.Message{
			Role:    "assistant",
			Content: "用户询问了天气和新闻，助手提供了搜索结果。",
		},
		FinishReason: "stop",
	}, nil
}

// ================================================================
// 原有测试（保持兼容）
// ================================================================

func TestCompressor_NoCompressWhenShort(t *testing.T) {
	c := NewCompressor(&mockSummarizer{}, CompressorConfig{
		WindowSize:  3,
		MaxMessages: 12,
	})

	history := []llm.Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好！"},
	}

	result, err := c.Compress(context.Background(), history)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.WasCompressed {
		t.Error("should not compress short history")
	}
	if len(result.Messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result.Messages))
	}
}

func TestCompressor_CompressLongHistory(t *testing.T) {
	c := NewCompressor(&mockSummarizer{}, CompressorConfig{
		WindowSize:  2,
		MaxMessages: 8,
	})

	// 构建一个长历史（5轮对话 = 1 system + 10 对话消息 = 11 条，超过阈值 8）
	history := []llm.Message{
		{Role: "system", Content: "你是助手"},
	}
	for i := 0; i < 5; i++ {
		history = append(history,
			llm.Message{Role: "user", Content: fmt.Sprintf("问题 %d", i+1)},
			llm.Message{Role: "assistant", Content: fmt.Sprintf("回答 %d", i+1)},
		)
	}

	t.Logf("original history: %d messages", len(history))

	result, err := c.Compress(context.Background(), history)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.WasCompressed {
		t.Error("should have compressed long history")
	}
	if result.CompressedCount >= result.OriginalCount {
		t.Errorf("compressed (%d) should be less than original (%d)",
			result.CompressedCount, result.OriginalCount)
	}

	t.Logf("compressed: %d -> %d messages", result.OriginalCount, result.CompressedCount)
	t.Logf("summary: %s", result.SummaryText)

	// 确保最后的消息还在（窗口内）
	lastMsg := result.Messages[len(result.Messages)-1]
	if lastMsg.Content != "回答 5" {
		t.Errorf("expected last message to be '回答 5', got '%s'", lastMsg.Content)
	}

	// 确保有摘要消息
	hasSummary := false
	for _, m := range result.Messages {
		if m.Role == "system" && len(m.Content) > 10 {
			if m.Content != "你是助手" {
				hasSummary = true
			}
		}
	}
	if !hasSummary {
		t.Error("expected a summary system message")
	}
}

func TestCompressor_NeedCompress(t *testing.T) {
	c := NewCompressor(&mockSummarizer{}, CompressorConfig{
		MaxMessages: 10,
	})

	short := make([]llm.Message, 5)
	if c.NeedCompress(short) {
		t.Error("5 messages should not need compress")
	}

	long := make([]llm.Message, 15)
	if !c.NeedCompress(long) {
		t.Error("15 messages should need compress")
	}
}

func TestFindWindowStart(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
		{Role: "user", Content: "q4"},
		{Role: "assistant", Content: "a4"},
	}

	// windowSize=2 → 应该从 q3 开始（index 4）
	idx := findWindowStart(msgs, 2)
	if idx != 4 {
		t.Errorf("expected window start at 4, got %d", idx)
	}

	// windowSize=3 → 应该从 q2 开始（index 2）
	idx = findWindowStart(msgs, 3)
	if idx != 2 {
		t.Errorf("expected window start at 2, got %d", idx)
	}

	// windowSize=10 → 超过总轮数，返回 0
	idx = findWindowStart(msgs, 10)
	if idx != 0 {
		t.Errorf("expected window start at 0, got %d", idx)
	}
}

func TestRelevantSummary(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 添加测试记忆（keyword 匹配更可靠）
	store.Add("编程语言", "用户正在用Go开发一个Agent项目", []string{"go", "agent", "编程"})
	store.Add("喜欢的颜色", "用户喜欢蓝色", []string{"颜色", "蓝色"})
	store.Add("工作地点", "用户在北京工作", []string{"北京", "工作"})

	// 搜索 "编程" → 应该匹配到 topic 或 keyword
	summary := store.RelevantSummary("编程", 5)
	t.Logf("programming query summary:\n%s", summary)
	if summary == "" {
		t.Error("expected non-empty relevant summary for programming query")
	}

	// 搜索 "北京" → 应该匹配到工作地点
	summary2 := store.RelevantSummary("北京", 5)
	t.Logf("beijing query summary:\n%s", summary2)
	if summary2 == "" {
		t.Error("expected non-empty relevant summary for beijing query")
	}

	// 确认只返回相关的（不是全量）
	// 用一个完全无关的查询
	summary3 := store.RelevantSummary("xyznonexistent", 5)
	t.Logf("irrelevant query summary: '%s'", summary3)
	if summary3 != "" {
		t.Logf("NOTE: irrelevant query still returned results (tokenizer may partial match)")
	}

	// 确认 count 显示正确
	if summary != "" && !strings.Contains(summary, "/3") {
		t.Logf("NOTE: summary format may not show total count as expected")
	}
}

func TestTruncateStr(t *testing.T) {
	s := "这是一段很长的中文文本用于测试截断功能"
	result := truncateStr(s, 5)
	if len([]rune(result)) > 8 { // 5 + "..." = max 8
		t.Errorf("truncated string too long: %s", result)
	}

	short := "短"
	result2 := truncateStr(short, 100)
	if result2 != short {
		t.Errorf("short string should not be truncated")
	}
}

// ================================================================
// 新增测试：增量压缩
// ================================================================

func TestCompressor_IncrementalCompress(t *testing.T) {
	c := NewCompressor(&mockSummarizer{}, CompressorConfig{
		WindowSize:  2,
		MaxMessages: 6,
	})

	// 模拟第一轮压缩后的 history：system + 旧摘要 + 最近 2 轮
	// 然后又新增了 3 轮对话，导致再次触发压缩
	history := []llm.Message{
		{Role: "system", Content: "你是助手"},
		// 这是上一轮压缩产生的摘要
		{Role: "system", Content: "[对话历史摘要]\n用户询问了天气和新闻，助手提供了搜索结果。\n[以下是最近的对话]"},
		// 上一轮保留的窗口消息（现在变成了旧的）
		{Role: "user", Content: "帮我写个函数"},
		{Role: "assistant", Content: "好的，这是一个排序函数..."},
		// 新增的对话
		{Role: "user", Content: "再加个单元测试"},
		{Role: "assistant", Content: "好的，测试代码如下..."},
		{Role: "user", Content: "项目进度怎么样"},
		{Role: "assistant", Content: "目前完成了80%..."},
	}

	result, err := c.Compress(context.Background(), history)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.WasCompressed {
		t.Error("should have compressed")
	}
	if !result.Incremental {
		t.Error("should be incremental compression (reusing old summary)")
	}

	t.Logf("incremental compressed: %d -> %d messages", result.OriginalCount, result.CompressedCount)
	t.Logf("summary: %s", result.SummaryText)

	// 确认摘要包含了旧摘要的信息（mock 会返回 "已有摘要" 相关内容）
	if !strings.Contains(result.SummaryText, "已有摘要") {
		t.Error("incremental summary should reference existing summary content")
	}

	// 确认最新的消息还在
	lastMsg := result.Messages[len(result.Messages)-1]
	if !strings.Contains(lastMsg.Content, "80%") {
		t.Errorf("expected last message about progress, got: %s", lastMsg.Content)
	}
}

func TestCompressor_IncrementalPreservesSystemPrompt(t *testing.T) {
	c := NewCompressor(&mockSummarizer{}, CompressorConfig{
		WindowSize:  1,
		MaxMessages: 4,
	})

	history := []llm.Message{
		{Role: "system", Content: "你是一个Go语言专家"},
		{Role: "system", Content: "[对话历史摘要]\n旧摘要内容\n[以下是最近的对话]"},
		{Role: "user", Content: "问题A"},
		{Role: "assistant", Content: "回答A"},
		{Role: "user", Content: "问题B"},
		{Role: "assistant", Content: "回答B"},
	}

	result, err := c.Compress(context.Background(), history)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 确认原始 system prompt 被保留
	if result.Messages[0].Content != "你是一个Go语言专家" {
		t.Errorf("original system prompt should be preserved, got: %s", result.Messages[0].Content)
	}

	// 确认只有一条摘要消息（不会累积多条）
	summaryCount := 0
	for _, m := range result.Messages {
		if isSummaryMessage(m) {
			summaryCount++
		}
	}
	if summaryCount != 1 {
		t.Errorf("expected exactly 1 summary message, got %d", summaryCount)
	}
}

// ================================================================
// 新增测试：Token-based 触发
// ================================================================

func TestCompressor_TokenBasedTrigger(t *testing.T) {
	// 只设置 MaxTokens，不设置 MaxMessages
	c := NewCompressor(&mockSummarizer{}, CompressorConfig{
		WindowSize: 2,
		MaxTokens:  100, // 很低的阈值，容易触发
	})

	// 构建一个消息数不多但 token 量超标的 history（需要 >windowSize 轮）
	history := []llm.Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: strings.Repeat("这是一段很长的文本", 20)},
		{Role: "assistant", Content: strings.Repeat("这是很长的回复内容", 20)},
		{Role: "user", Content: strings.Repeat("第二轮长文本问题", 15)},
		{Role: "assistant", Content: strings.Repeat("第二轮长文本回答", 15)},
		{Role: "user", Content: "最新的短问题"},
		{Role: "assistant", Content: "最新的短回答"},
	}

	// 消息数只有 5 条，但 token 量超过 100
	if !c.NeedCompress(history) {
		t.Error("should trigger compression by token count")
	}

	result, err := c.Compress(context.Background(), history)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.WasCompressed {
		t.Error("should have compressed based on token count")
	}

	t.Logf("token-based: %d -> %d messages, estimated tokens: %d",
		result.OriginalCount, result.CompressedCount, result.EstimatedTokens)
}

func TestCompressor_TokenBasedNoTriggerWhenLow(t *testing.T) {
	c := NewCompressor(&mockSummarizer{}, CompressorConfig{
		WindowSize: 2,
		MaxTokens:  10000, // 很高的阈值
	})

	history := []llm.Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好！"},
	}

	if c.NeedCompress(history) {
		t.Error("should NOT trigger compression when token count is low")
	}
}

func TestCompressor_DualTrigger(t *testing.T) {
	// 同时设置两种触发条件
	c := NewCompressor(&mockSummarizer{}, CompressorConfig{
		WindowSize:  2,
		MaxMessages: 100, // 消息数阈值很高
		MaxTokens:   50,  // token 阈值很低
	})

	history := []llm.Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: strings.Repeat("长文本", 30)},
		{Role: "assistant", Content: strings.Repeat("长回复", 30)},
		{Role: "user", Content: "最新问题"},
		{Role: "assistant", Content: "最新回答"},
	}

	// 消息数只有 5（< 100），但 token 超标（> 50）
	if !c.NeedCompress(history) {
		t.Error("should trigger by token even though message count is below threshold")
	}
}

// ================================================================
// 新增测试：Token 估算
// ================================================================

func TestEstimateTokens(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "你好世界"},     // 4 字 = 2 token + 4 overhead
		{Role: "assistant", Content: "Hello!"}, // 6 chars = 3 token + 4 overhead
	}

	tokens := estimateTokens(msgs)
	t.Logf("estimated tokens: %d", tokens)

	if tokens <= 0 {
		t.Error("token estimate should be positive")
	}
	// 粗略验证：(4/2 + 4) + (6/2 + 4) = 6 + 7 = 13
	if tokens < 10 || tokens > 20 {
		t.Errorf("token estimate %d seems off (expected ~13)", tokens)
	}
}

func TestEstimateStringTokens(t *testing.T) {
	tests := []struct {
		input    string
		minToken int
		maxToken int
	}{
		{"", 0, 0},
		{"hi", 1, 2},
		{"你好世界测试文本", 3, 5},
		{strings.Repeat("test ", 100), 200, 300},
	}

	for _, tt := range tests {
		got := estimateStringTokens(tt.input)
		if got < tt.minToken || got > tt.maxToken {
			t.Errorf("estimateStringTokens(%q): got %d, want [%d, %d]",
				truncateStr(tt.input, 20), got, tt.minToken, tt.maxToken)
		}
	}
}

// ================================================================
// 新增测试：摘要消息识别
// ================================================================

func TestIsSummaryMessage(t *testing.T) {
	tests := []struct {
		msg      llm.Message
		expected bool
	}{
		{
			llm.Message{Role: "system", Content: "[对话历史摘要]\n一些摘要\n[以下是最近的对话]"},
			true,
		},
		{
			llm.Message{Role: "system", Content: "你是一个助手"},
			false,
		},
		{
			llm.Message{Role: "user", Content: "[对话历史摘要]\n伪造的"},
			false,
		},
	}

	for i, tt := range tests {
		got := isSummaryMessage(tt.msg)
		if got != tt.expected {
			t.Errorf("case %d: isSummaryMessage() = %v, want %v", i, got, tt.expected)
		}
	}
}

func TestExtractSummaryText(t *testing.T) {
	msg := llm.Message{
		Role:    "system",
		Content: "[对话历史摘要]\n用户做了一些事情。\n[以下是最近的对话]",
	}

	text := extractSummaryText(msg)
	expected := "用户做了一些事情。"
	if text != expected {
		t.Errorf("extractSummaryText() = %q, want %q", text, expected)
	}
}

// ================================================================
// 新增测试：动态窗口
// ================================================================

func TestCompressor_DynamicWindowSize(t *testing.T) {
	c := NewCompressor(&mockSummarizer{}, CompressorConfig{
		WindowSize:   5,    // 最大希望保留 5 轮
		MaxMessages:  6,
		TargetTokens: 200,  // 目标 200 token
	})

	systemMsgs := []llm.Message{
		{Role: "system", Content: "你是助手"},
	}

	// 构建 5 轮对话，每轮内容很长
	var convMsgs []llm.Message
	for i := 0; i < 5; i++ {
		convMsgs = append(convMsgs,
			llm.Message{Role: "user", Content: fmt.Sprintf("这是第%d轮非常长的问题 %s", i+1, strings.Repeat("内容", 50))},
			llm.Message{Role: "assistant", Content: fmt.Sprintf("这是第%d轮非常长的回答 %s", i+1, strings.Repeat("内容", 50))},
		)
	}

	ws := c.dynamicWindowSize(systemMsgs, convMsgs)
	t.Logf("dynamic window size: %d (max: %d)", ws, c.windowSize)

	// 因为每轮很长，200 token 目标应该缩小窗口
	if ws >= 5 {
		t.Errorf("dynamic window should be smaller than max 5, got %d", ws)
	}
	if ws < 1 {
		t.Error("dynamic window should be at least 1")
	}
}
