package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// mockHierarchicalLLM 用于分层摘要的 mock
type mockHierarchicalLLM struct{}

func (m *mockHierarchicalLLM) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.ChatResponse, error) {
	// 根据 prompt 内容判断是 L1 还是 L2 请求
	for _, msg := range messages {
		if strings.Contains(msg.Content, "分段摘要") || strings.Contains(msg.Content, "全局对话摘要") {
			return &llm.ChatResponse{
				Message: llm.Message{
					Role:    "assistant",
					Content: "用户在多轮对话中讨论了编程问题和项目架构设计，助手提供了代码实现和建议。",
				},
			}, nil
		}
	}
	return &llm.ChatResponse{
		Message: llm.Message{
			Role:    "assistant",
			Content: `{"summary": "用户讨论了编程问题，助手提供了解决方案。", "key_entities": ["编程", "解决方案"]}`,
		},
	}, nil
}

func TestSplitIntoRounds(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "问题1"},
		{Role: "assistant", Content: "回答1"},
		{Role: "user", Content: "问题2"},
		{Role: "assistant", Content: "回答2"},
		{Role: "tool", Content: "工具结果"},
		{Role: "user", Content: "问题3"},
		{Role: "assistant", Content: "回答3"},
	}

	rounds := SplitIntoRounds(msgs)

	if len(rounds) != 3 {
		t.Fatalf("expected 3 rounds, got %d", len(rounds))
	}

	// 第一轮: user+assistant
	if rounds[0][0].Content != "问题1" {
		t.Errorf("round 1 should start with 问题1, got %s", rounds[0][0].Content)
	}

	// 第二轮: user+assistant+tool
	if len(rounds[1]) != 3 {
		t.Errorf("round 2 should have 3 messages, got %d", len(rounds[1]))
	}

	// 第三轮: user+assistant
	if rounds[2][0].Content != "问题3" {
		t.Errorf("round 3 should start with 问题3, got %s", rounds[2][0].Content)
	}
}

func TestHierarchicalCompressor_ProcessNewRounds(t *testing.T) {
	dir := t.TempDir()
	hc, err := NewHierarchicalCompressor(&mockHierarchicalLLM{}, dir, HierarchicalConfig{
		ChunkRounds:  2, // 每 2 轮生成一个 L1 chunk
		L2MergeCount: 3, // 每 3 个 L1 chunk 触发 L2
		MaxL1Display: 2,
	})
	if err != nil {
		t.Fatalf("NewHierarchicalCompressor: %v", err)
	}

	// 构建 6 轮对话
	var rounds [][]llm.Message
	for i := 0; i < 6; i++ {
		rounds = append(rounds, []llm.Message{
			{Role: "user", Content: fmt.Sprintf("问题 %d", i+1)},
			{Role: "assistant", Content: fmt.Sprintf("回答 %d", i+1)},
		})
	}

	// 处理
	err = hc.ProcessNewRounds(context.Background(), rounds)
	if err != nil {
		t.Fatalf("ProcessNewRounds: %v", err)
	}

	state := hc.GetState()

	// 6 轮 / 2 轮每 chunk = 3 个 L1 chunk
	if len(state.ChunkSummaries) != 3 {
		t.Errorf("expected 3 L1 chunks, got %d", len(state.ChunkSummaries))
	}

	// 3 个 L1 chunk >= L2MergeCount=3，应该触发 L2
	if state.SessionSummary == "" {
		t.Error("expected L2 session summary to be generated")
	}

	t.Logf("L2 session summary: %s", state.SessionSummary)
	for i, cs := range state.ChunkSummaries {
		t.Logf("L1 chunk %d (round %d~%d): %s", i, cs.RoundStart, cs.RoundEnd, cs.Summary)
	}
}

func TestHierarchicalCompressor_GetContextInjection(t *testing.T) {
	dir := t.TempDir()
	hc, err := NewHierarchicalCompressor(&mockHierarchicalLLM{}, dir, HierarchicalConfig{
		ChunkRounds:  2,
		L2MergeCount: 10, // 设很高，不触发 L2
		MaxL1Display: 2,
	})
	if err != nil {
		t.Fatalf("NewHierarchicalCompressor: %v", err)
	}

	// 处理 4 轮 → 生成 2 个 L1 chunk
	var rounds [][]llm.Message
	for i := 0; i < 4; i++ {
		rounds = append(rounds, []llm.Message{
			{Role: "user", Content: fmt.Sprintf("问题 %d", i+1)},
			{Role: "assistant", Content: fmt.Sprintf("回答 %d", i+1)},
		})
	}

	err = hc.ProcessNewRounds(context.Background(), rounds)
	if err != nil {
		t.Fatalf("ProcessNewRounds: %v", err)
	}

	injection := hc.GetContextInjection()
	t.Logf("context injection:\n%s", injection)

	if injection == "" {
		t.Error("expected non-empty context injection")
	}
	if !strings.Contains(injection, "近期对话细节") {
		t.Error("expected L1 chunk details in injection")
	}
}

func TestHierarchicalCompressor_Persistence(t *testing.T) {
	dir := t.TempDir()

	// 创建并处理
	hc1, err := NewHierarchicalCompressor(&mockHierarchicalLLM{}, dir, HierarchicalConfig{
		ChunkRounds: 2,
	})
	if err != nil {
		t.Fatalf("NewHierarchicalCompressor: %v", err)
	}

	rounds := [][]llm.Message{
		{{Role: "user", Content: "问题1"}, {Role: "assistant", Content: "回答1"}},
		{{Role: "user", Content: "问题2"}, {Role: "assistant", Content: "回答2"}},
	}
	err = hc1.ProcessNewRounds(context.Background(), rounds)
	if err != nil {
		t.Fatalf("ProcessNewRounds: %v", err)
	}

	// 重新加载
	hc2, err := NewHierarchicalCompressor(&mockHierarchicalLLM{}, dir, HierarchicalConfig{
		ChunkRounds: 2,
	})
	if err != nil {
		t.Fatalf("reload NewHierarchicalCompressor: %v", err)
	}

	state := hc2.GetState()
	if len(state.ChunkSummaries) != 1 {
		t.Errorf("expected 1 chunk after reload, got %d", len(state.ChunkSummaries))
	}
}

func TestHierarchicalCompressor_PartialRounds(t *testing.T) {
	dir := t.TempDir()
	hc, err := NewHierarchicalCompressor(&mockHierarchicalLLM{}, dir, HierarchicalConfig{
		ChunkRounds: 3, // 每 3 轮一个 chunk
	})
	if err != nil {
		t.Fatalf("NewHierarchicalCompressor: %v", err)
	}

	// 只处理 2 轮（不满一个 chunk）
	rounds := [][]llm.Message{
		{{Role: "user", Content: "问题1"}, {Role: "assistant", Content: "回答1"}},
		{{Role: "user", Content: "问题2"}, {Role: "assistant", Content: "回答2"}},
	}
	err = hc.ProcessNewRounds(context.Background(), rounds)
	if err != nil {
		t.Fatalf("ProcessNewRounds: %v", err)
	}

	state := hc.GetState()
	if len(state.ChunkSummaries) != 0 {
		t.Errorf("expected 0 chunks (not enough rounds), got %d", len(state.ChunkSummaries))
	}
	if state.TotalRounds != 2 {
		t.Errorf("expected totalRounds=2, got %d", state.TotalRounds)
	}
}
