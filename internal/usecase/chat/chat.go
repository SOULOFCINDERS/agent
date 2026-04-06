// Package chat 实现核心 Agent 对话循环。
// 对应 DDD Usecase 层：编排 Domain 对象完成用户会话。
package chat

import (
	"context"
	"io"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
	"github.com/SOULOFCINDERS/agent/internal/memory"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// StreamWriter 流式输出回调
type StreamWriter = agent.StreamWriter

// Service 对话用例服务
type Service struct {
	loop *agent.LoopAgent
}

// NewService 创建对话服务
func NewService(
	client conv.Client,
	reg *tools.Registry,
	systemPrompt string,
	trace io.Writer,
	memStore *memory.Store,
	compressor *memory.Compressor,
) *Service {
	return &Service{
		loop: agent.NewLoopAgent(client, reg, systemPrompt, trace, memStore, compressor),
	}
}

// Chat 执行一次同步对话
func (s *Service) Chat(ctx context.Context, input string, history []conv.Message) (string, []conv.Message, error) {
	return s.loop.Chat(ctx, input, history)
}

// ChatStream 执行一次流式对话
func (s *Service) ChatStream(ctx context.Context, input string, history []conv.Message, onDelta StreamWriter) (string, []conv.Message, error) {
	return s.loop.ChatStream(ctx, input, history, onDelta)
}
