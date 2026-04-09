package agent

import (
	"context"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// StreamWriter 定义流式输出回调（兼容旧接口）
type StreamWriter func(delta string)

// ChatStream 流式版本的 Chat（兼容层）
// 内部委托给 ChatStreamV2，只转发文本 delta 事件。
// 新代码请直接使用 ChatStreamV2 获得完整的事件流。
func (a *LoopAgent) ChatStream(ctx context.Context, userMessage string, history []llm.Message, onDelta StreamWriter) (string, []llm.Message, error) {
	return a.ChatStreamV2(ctx, userMessage, history, AsEventWriter(onDelta))
}
