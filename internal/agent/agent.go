package agent

import (
	"context"
	"encoding/json"
	"io"

	al "github.com/SOULOFCINDERS/agent/internal/domain/agentloop"
)

// ---------- 类型别名：从 domain/agentloop 引入 ----------

type Planner = al.Planner
type Executor = al.Executor
type TraceEvent = al.TraceEvent

// ---------- Agent 实现 ----------

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

func WriteTraceEvent(w io.Writer, ev TraceEvent) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = w.Write(append(b, '\n'))
}
