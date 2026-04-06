// Package executor implements the plan executor that runs tool calls and manages step-by-step execution.
package executor

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

type Executor struct {
	reg *tools.Registry
}

func NewExecutor(reg *tools.Registry) *Executor {
	return &Executor{reg: reg}
}

func (e *Executor) Execute(ctx context.Context, plan agent.Plan, trace io.Writer) (string, []agent.TraceEvent, error) {
	var events []agent.TraceEvent
	var last any

	for _, step := range plan.Steps {
		switch step.Kind {
		case agent.StepToolCall:
			start := time.Now()
			res, err := e.execTool(ctx, step, last)
			ev := agent.TraceEvent{
				At:       time.Now(),
				Step:     step,
				Duration: time.Since(start).String(),
				OK:       err == nil,
			}
			if err != nil {
				ev.Error = err.Error()
			} else {
				ev.Result = res
				last = res
			}
			events = append(events, ev)
			if trace != nil {
				agent.WriteTraceEvent(trace, ev)
			}
			if err != nil {
				return "", events, err
			}
		case agent.StepFinal:
			return step.Text, events, nil
		default:
			return "", events, fmt.Errorf("unknown step kind: %q", step.Kind)
		}
	}

	if last == nil {
		return "", events, nil
	}
	return tools.FormatResult(last), events, nil
}

func (e *Executor) execTool(ctx context.Context, step agent.Step, last any) (any, error) {
	t := e.reg.Get(step.Tool)
	if t == nil {
		return nil, fmt.Errorf("unknown tool: %s", step.Tool)
	}
	args := map[string]any{}
	for k, v := range step.Args {
		args[k] = v
	}
	if _, ok := args["input"]; !ok && last != nil {
		args["input"] = tools.FormatResult(last)
	}
	return t.Execute(ctx, args)
}
