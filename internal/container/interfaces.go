package container

import (
	"context"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
)

// ChatAgent 抽象了 LoopAgent 和 Orchestrator 的对话接口
type ChatAgent interface {
	Chat(ctx context.Context, userMessage string, history []conv.Message) (string, []conv.Message, error)
	ChatStream(ctx context.Context, userMessage string, history []conv.Message, onDelta agent.StreamWriter) (string, []conv.Message, error)
	ChatStreamV2(ctx context.Context, userMessage string, history []conv.Message, onEvent agent.StreamEventWriter) (string, []conv.Message, error)
}
