package agent

import (
	"context"
	"io"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// --- test helpers ---

type panicTool struct{}

func (t *panicTool) Name() string { return "panic_tool" }
func (t *panicTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	panic("something went terribly wrong")
}

type hangingTool struct{ delay time.Duration }

func (t *hangingTool) Name() string { return "slow_tool" }
func (t *hangingTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	select {
	case <-time.After(t.delay):
		return "done", nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type failTool struct{ err error }

func (t *failTool) Name() string { return "fail_tool" }
func (t *failTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	return nil, t.err
}

type succeedTool struct{}

func (t *succeedTool) Name() string { return "succeed_tool" }
func (t *succeedTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	return "success!", nil
}

func makeTestAgent(ts ...tools.Tool) *LoopAgent {
	reg := tools.NewRegistry()
	for _, t := range ts {
		reg.Register(t)
	}
	return &LoopAgent{
		registry:     reg,
		retryTracker: newRetryTracker(3),
		trace:        io.Discard,
	}
}

// --- Tests ---

func TestPanicRecovery(t *testing.T) {
	agent := makeTestAgent(&panicTool{})

	tc := llm.ToolCall{
		ID:       "call_1",
		Type:     "function",
		Function: llm.FunctionCall{Name: "panic_tool", Arguments: `{}`},
	}

	result := agent.executeOneToolSafe(context.Background(), tc)

	if result.callID != "call_1" {
		t.Errorf("expected callID 'call_1', got '%s'", result.callID)
	}

	// should catch panic, not crash
	if !strings.Contains(result.content, "TOOL_ERROR") {
		t.Errorf("expected error message, got: %s", result.content)
	}
	if !strings.Contains(result.content, "panic") || !strings.Contains(result.content, "internal_error") {
		t.Errorf("expected panic classification, got: %s", result.content)
	}
	t.Logf("panic result: %s", result.content)
}

func TestUnknownTool(t *testing.T) {
	agent := makeTestAgent()

	tc := llm.ToolCall{
		ID:       "call_2",
		Type:     "function",
		Function: llm.FunctionCall{Name: "nonexistent", Arguments: `{}`},
	}

	result := agent.executeOneToolSafe(context.Background(), tc)

	if !strings.Contains(result.content, "unknown_tool") {
		t.Errorf("expected unknown_tool error, got: %s", result.content)
	}
	t.Logf("unknown tool result: %s", result.content)
}

func TestRetryableError(t *testing.T) {
	agent := makeTestAgent(&failTool{err: fmt.Errorf("invalid JSON: unexpected token")})

	tc := llm.ToolCall{
		ID:       "call_3",
		Type:     "function",
		Function: llm.FunctionCall{Name: "fail_tool", Arguments: `{}`},
	}

	// Fail 1
	r1 := agent.executeOneToolSafe(context.Background(), tc)
	if !strings.Contains(r1.content, "retryable") {
		t.Errorf("expected retryable error, got: %s", r1.content)
	}
	if !strings.Contains(r1.content, "1/3") {
		t.Errorf("expected retry count 1/3, got: %s", r1.content)
	}

	// Fail 2
	r2 := agent.executeOneToolSafe(context.Background(), tc)
	if !strings.Contains(r2.content, "2/3") {
		t.Errorf("expected retry count 2/3, got: %s", r2.content)
	}

	// Fail 3
	r3 := agent.executeOneToolSafe(context.Background(), tc)
	if !strings.Contains(r3.content, "3/3") {
		t.Errorf("expected retry count 3/3, got: %s", r3.content)
	}

	// Fail 4 -> should be rejected
	r4 := agent.executeOneToolSafe(context.Background(), tc)
	if !strings.Contains(r4.content, "max_retries_exceeded") {
		t.Errorf("expected max_retries_exceeded, got: %s", r4.content)
	}

	t.Logf("retry sequence:\n  1: %s\n  2: %s\n  3: %s\n  4: %s",
		firstLine(r1.content), firstLine(r2.content), firstLine(r3.content), firstLine(r4.content))
}

func TestNotRetryableError(t *testing.T) {
	agent := makeTestAgent(&failTool{err: fmt.Errorf("permission denied: /restricted/path")})

	tc := llm.ToolCall{
		ID:       "call_4",
		Type:     "function",
		Function: llm.FunctionCall{Name: "fail_tool", Arguments: `{}`},
	}

	// first call should be permanent
	r1 := agent.executeOneToolSafe(context.Background(), tc)
	if !strings.Contains(r1.content, "permanent") {
		t.Errorf("expected permanent error, got: %s", r1.content)
	}

	// second call should be blocked
	r2 := agent.executeOneToolSafe(context.Background(), tc)
	if !strings.Contains(r2.content, "max_retries_exceeded") {
		t.Errorf("expected max_retries_exceeded after permanent error, got: %s", r2.content)
	}

	t.Logf("permanent error:\n  1: %s\n  2: %s", firstLine(r1.content), firstLine(r2.content))
}

func TestBadArguments(t *testing.T) {
	agent := makeTestAgent(&succeedTool{})

	tc := llm.ToolCall{
		ID:       "call_5",
		Type:     "function",
		Function: llm.FunctionCall{Name: "succeed_tool", Arguments: `not valid json`},
	}

	result := agent.executeOneToolSafe(context.Background(), tc)
	if !strings.Contains(result.content, "retryable") {
		t.Errorf("expected retryable error for bad JSON, got: %s", result.content)
	}
	t.Logf("bad args result: %s", firstLine(result.content))
}

func TestSuccessResetsRetry(t *testing.T) {
	agent := makeTestAgent(&succeedTool{})

	tc := llm.ToolCall{
		ID:       "call_6",
		Type:     "function",
		Function: llm.FunctionCall{Name: "succeed_tool", Arguments: `{}`},
	}

	agent.retryTracker.record("succeed_tool", `{}`)
	agent.retryTracker.record("succeed_tool", `{}`)
	if agent.retryTracker.getCount("succeed_tool", `{}`) != 2 {
		t.Fatal("expected count 2 after 2 records")
	}

	result := agent.executeOneToolSafe(context.Background(), tc)
	if strings.Contains(result.content, "TOOL_ERROR") {
		t.Errorf("expected success, got error: %s", result.content)
	}
	if agent.retryTracker.getCount("succeed_tool", `{}`) != 0 {
		t.Errorf("expected retry count reset to 0, got %d",
			agent.retryTracker.getCount("succeed_tool", `{}`))
	}
	t.Logf("success result: %s", result.content)
}

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ToolErrorKind
	}{
		{"timeout", fmt.Errorf("context deadline exceeded"), ErrTimeout},
		{"invalid_args", fmt.Errorf("invalid parameter: missing 'path'"), ErrRetryable},
		{"json_parse", fmt.Errorf("json: cannot unmarshal string into int"), ErrRetryable},
		{"not_found", fmt.Errorf("file not found: /foo/bar"), ErrNotRetryable},
		{"permission", fmt.Errorf("permission denied"), ErrNotRetryable},
		{"generic", fmt.Errorf("something went wrong"), ErrRetryable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			te := classifyError("test_tool", tt.err)
			if te.Kind != tt.expected {
				t.Errorf("expected %s, got %s for error: %s", tt.expected, te.Kind, tt.err)
			}
		})
	}
}

func TestRetryTracker(t *testing.T) {
	rt := newRetryTracker(3)

	if !rt.canRetry("tool", "args") {
		t.Error("should be retryable initially")
	}

	rt.record("tool", "args")
	rt.record("tool", "args")
	rt.record("tool", "args")

	if rt.canRetry("tool", "args") {
		t.Error("should NOT be retryable after 3 failures")
	}

	rt.reset("tool", "args")
	if !rt.canRetry("tool", "args") {
		t.Error("should be retryable after reset")
	}

	// different args are independent
	rt.record("tool", "args_A")
	rt.record("tool", "args_A")
	rt.record("tool", "args_A")
	if rt.canRetry("tool", "args_A") {
		t.Error("args_A should be exhausted")
	}
	if !rt.canRetry("tool", "args_B") {
		t.Error("args_B should still be retryable")
	}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}
