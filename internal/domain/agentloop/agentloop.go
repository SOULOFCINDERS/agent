// Package agentloop defines domain models for the agent execution loop including Plan, Step, Planner and Executor interfaces.
package agentloop

import (
	"context"
	"io"
	"time"
)

// ---- 值对象 ----

// StepKind 步骤类型
type StepKind string

const (
	StepToolCall StepKind = "tool_call"
	StepFinal    StepKind = "final"
)

// Step 表示计划中的一个步骤
type Step struct {
	Kind StepKind       `json:"kind"`
	Tool string         `json:"tool,omitempty"`
	Args map[string]any `json:"args,omitempty"`
	Text string         `json:"text,omitempty"`
}

// Plan 表示一组有序步骤
type Plan struct {
	Steps []Step `json:"steps"`
}

// TraceEvent 跟踪事件
type TraceEvent struct {
	At       time.Time `json:"at"`
	Step     Step      `json:"step"`
	Duration string    `json:"duration"`
	OK       bool      `json:"ok"`
	Result   any       `json:"result,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// ---- 接口 ----

// Planner 规划器接口：根据输入生成执行计划
type Planner interface {
	Plan(ctx context.Context, input string) (Plan, error)
}

// Executor 执行器接口：执行计划并返回结果
type Executor interface {
	Execute(ctx context.Context, plan Plan, trace io.Writer) (string, []TraceEvent, error)
}
