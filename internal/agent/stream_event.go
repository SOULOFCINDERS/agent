package agent

import "encoding/json"

// StreamEventType 流式事件类型
type StreamEventType string

const (
	// EventDelta 文本增量（逐 token 输出）
	EventDelta StreamEventType = "delta"
	// EventToolStart 工具调用开始
	EventToolStart StreamEventType = "tool_start"
	// EventToolEnd 工具调用结束
	EventToolEnd StreamEventType = "tool_end"
	// EventIteration Agent 循环迭代开始
	EventIteration StreamEventType = "iteration"
	// EventThinking 模型思考过程（reasoning tokens）
	EventThinking StreamEventType = "thinking"
	// EventStatus Agent 状态变更
	EventStatus StreamEventType = "status"
)

// StreamEvent 统一的流式事件
type StreamEvent struct {
	Type StreamEventType `json:"type"`

	// EventDelta: 文本内容增量
	Content string `json:"content,omitempty"`

	// EventToolStart / EventToolEnd: 工具调用相关
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolArgs   string `json:"tool_args,omitempty"`   // JSON string
	ToolResult string `json:"tool_result,omitempty"` // 执行结果（仅 EventToolEnd）
	ToolError  string `json:"tool_error,omitempty"`  // 错误信息（仅 EventToolEnd）
	Duration   int64  `json:"duration,omitempty"`     // 执行耗时毫秒（仅 EventToolEnd）

	// EventIteration: 循环迭代
	Iteration int `json:"iteration,omitempty"`
	MaxIter   int `json:"max_iter,omitempty"`

	// EventThinking: 思考内容
	Thinking string `json:"thinking,omitempty"`

	// EventStatus: 状态描述
	Status string `json:"status,omitempty"`
}

// JSON 序列化辅助
func (e StreamEvent) JSON() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// StreamEventWriter 流式事件回调（升级版 StreamWriter）
type StreamEventWriter func(event StreamEvent)

// AsEventWriter 将旧版 StreamWriter 包装为 StreamEventWriter
// 只转发 delta 事件的文本内容，忽略其他事件类型
func AsEventWriter(legacy StreamWriter) StreamEventWriter {
	if legacy == nil {
		return nil
	}
	return func(event StreamEvent) {
		if event.Type == EventDelta && event.Content != "" {
			legacy(event.Content)
		}
	}
}
