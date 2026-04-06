// Package structuredoutput 实现结构化输出用例。
// 对应 DDD Usecase 层：编排 LLM Client + Schema 验证完成结构化输出提取。
package structuredoutput

import (
	"context"

	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
	"github.com/SOULOFCINDERS/agent/internal/structured"
)

// Service 结构化输出用例服务
type Service struct {
	engine *structured.Engine
}

// NewService 创建结构化输出服务
func NewService(cfg structured.Config) *Service {
	return &Service{
		engine: structured.NewEngine(cfg),
	}
}

// Chat 执行结构化输出对话
func (s *Service) Chat(ctx context.Context, client conv.Client, messages []conv.Message, tools []conv.ToolDef) (*structured.StructuredChatResult, error) {
	return s.engine.Chat(ctx, client, messages, tools)
}
