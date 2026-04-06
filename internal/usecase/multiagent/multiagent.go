// Package multiagent 实现多 Agent 编排用例。
// 对应 DDD Usecase 层：编排多个 LoopAgent 协作完成复杂任务。
package multiagent

import (
	"context"

	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
	ma "github.com/SOULOFCINDERS/agent/internal/multiagent"
)

// Service 多 Agent 用例服务
type Service struct {
	orch *ma.Orchestrator
}

// NewService 创建多 Agent 服务
func NewService(orch *ma.Orchestrator) *Service {
	return &Service{orch: orch}
}

// Chat 执行多 Agent 对话
func (s *Service) Chat(ctx context.Context, input string, history []conv.Message) (string, []conv.Message, error) {
	return s.orch.Chat(ctx, input, history)
}
