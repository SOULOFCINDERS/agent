package executor

import (
	"context"
	"io"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

func TestExecutor_PassesLastOutputAsInput(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())
	reg.Register(tools.NewSummarizeTool())

	ex := NewExecutor(reg)
	plan := agent.Plan{
		Steps: []agent.Step{
			{Kind: agent.StepToolCall, Tool: "echo", Args: map[string]any{"text": "hello"}},
			{Kind: agent.StepToolCall, Tool: "summarize", Args: map[string]any{}},
		},
	}

	out, _, err := ex.Execute(context.Background(), plan, io.Discard)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if out == "" || out == "hello" {
		t.Fatalf("expected summary output, got %q", out)
	}
}
