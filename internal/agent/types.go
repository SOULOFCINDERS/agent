package agent

import (
	al "github.com/SOULOFCINDERS/agent/internal/domain/agentloop"
)

// ---------- 类型别名：从 domain/agentloop 引入 ----------

type StepKind = al.StepKind

const (
	StepToolCall = al.StepToolCall
	StepFinal    = al.StepFinal
)

type Step = al.Step
type Plan = al.Plan
