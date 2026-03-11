package agent

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

type Planner interface {
	Plan(ctx context.Context, input string) (Plan, error)
}

type Executor interface {
	Execute(ctx context.Context, plan Plan, trace io.Writer) (string, []TraceEvent, error)
}

type Agent struct {
	planner  Planner
	executor Executor
}

func New(planner Planner, executor Executor) *Agent {
	return &Agent{
		planner:  planner,
		executor: executor,
	}
}

func (a *Agent) Run(ctx context.Context, input string, trace io.Writer) (string, []TraceEvent, error) {
	plan, err := a.planner.Plan(ctx, input)
	if err != nil {
		return "", nil, err
	}
	return a.executor.Execute(ctx, plan, trace)
}

type TraceEvent struct {
	At       time.Time `json:"at"`
	Step     Step      `json:"step"`
	Duration string    `json:"duration"`
	OK       bool      `json:"ok"`
	Result   any       `json:"result,omitempty"`
	Error    string    `json:"error,omitempty"`
}

func WriteTraceEvent(w io.Writer, ev TraceEvent) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = w.Write(append(b, '\n'))
}
