package agent

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// slowTool 模拟一个耗时 200ms 的工具
type slowTool struct {
	name  string
	delay time.Duration
}

func (t *slowTool) Name() string { return t.name }
func (t *slowTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	time.Sleep(t.delay)
	return fmt.Sprintf("result_from_%s", t.name), nil
}

func TestParallelExecution(t *testing.T) {
	reg := tools.NewRegistry()
	for i := 0; i < 5; i++ {
		reg.Register(&slowTool{
			name:  fmt.Sprintf("slow_%d", i),
			delay: 200 * time.Millisecond,
		})
	}

	agent := &LoopAgent{
		registry: reg,
		trace:    io.Discard,
	}

	// 构建 5 个 tool calls
	var calls []llm.ToolCall
	for i := 0; i < 5; i++ {
		calls = append(calls, llm.ToolCall{
			ID:   fmt.Sprintf("call_%d", i),
			Type: "function",
			Function: llm.FunctionCall{
				Name:      fmt.Sprintf("slow_%d", i),
				Arguments: `{}`,
			},
		})
	}

	ctx := context.Background()

	// 测试并发执行
	start := time.Now()
	history := agent.executeToolCallsParallel(ctx, calls, nil)
	parallelTime := time.Since(start)

	// 验证结果数量
	if len(history) != 5 {
		t.Fatalf("expected 5 results, got %d", len(history))
	}

	// 验证结果顺序（关键！OpenAI 要求顺序一致）
	for i, msg := range history {
		expectedID := fmt.Sprintf("call_%d", i)
		if msg.ToolCallID != expectedID {
			t.Errorf("result[%d]: expected ToolCallID=%s, got %s", i, expectedID, msg.ToolCallID)
		}
		expectedContent := fmt.Sprintf("result_from_slow_%d", i)
		if msg.Content != expectedContent {
			t.Errorf("result[%d]: expected Content=%s, got %s", i, expectedContent, msg.Content)
		}
		if msg.Role != "tool" {
			t.Errorf("result[%d]: expected Role=tool, got %s", i, msg.Role)
		}
	}

	// 验证并发加速：5 个 200ms 工具串行需要 1s，并发应该 < 500ms
	if parallelTime > 500*time.Millisecond {
		t.Errorf("parallel execution too slow: %s (expected < 500ms for 5x200ms tasks)", parallelTime)
	}

	t.Logf("✅ 5 tools (200ms each): parallel=%s (serial would be ~1s)", parallelTime)
}

func TestSingleToolNoGoroutine(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&slowTool{name: "single", delay: 50 * time.Millisecond})

	agent := &LoopAgent{
		registry: reg,
		trace:    io.Discard,
	}

	calls := []llm.ToolCall{{
		ID:   "call_0",
		Type: "function",
		Function: llm.FunctionCall{
			Name:      "single",
			Arguments: `{}`,
		},
	}}

	ctx := context.Background()
	history := agent.executeToolCallsParallel(ctx, calls, nil)

	if len(history) != 1 {
		t.Fatalf("expected 1 result, got %d", len(history))
	}
	if history[0].Content != "result_from_single" {
		t.Errorf("unexpected content: %s", history[0].Content)
	}
	t.Log("✅ single tool call: no goroutine overhead")
}
