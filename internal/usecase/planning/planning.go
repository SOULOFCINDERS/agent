// Package planning 实现多步规划执行用例。
// 对应 DDD Usecase 层：编排 Planner → Executor 完成复杂任务。
package planning

import (
	"context"
	"io"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	agloop "github.com/SOULOFCINDERS/agent/internal/domain/agentloop"
)

// ---------- 类型别名：从 domain/agentloop 引入 ----------

type StepKind = agloop.StepKind
type Step = agloop.Step
type Plan = agloop.Plan
type TraceEvent = agloop.TraceEvent
type Planner = agloop.Planner
type Executor = agloop.Executor

// Service 规划执行用例服务
type Service struct {
	agent *agent.Agent
}

// NewService 创建规划服务
func NewService(planner Planner, executor Executor) *Service {
	return &Service{
		agent: agent.New(planner, executor),
	}
}

// Run 执行规划任务
func (s *Service) Run(ctx context.Context, input string, trace io.Writer) (string, []TraceEvent, error) {
	return s.agent.Run(ctx, input, trace)
}
