package conversation

import "context"

// Client LLM 客户端接口（Domain 层定义）
type Client interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDef) (*ChatResponse, error)
}

// StreamingClient 支持流式的 LLM 客户端（可选能力）
type StreamingClient interface {
	Client
	StreamChat(ctx context.Context, messages []Message, tools []ToolDef) (StreamReader, error)
}

// StreamReader 流式读取器接口
type StreamReader interface {
	Recv() (*StreamDelta, error)
	Close() error
	Collect() (*ChatResponse, error)
}

// FormatClient 支持 response_format 的 LLM 客户端（可选能力）
type FormatClient interface {
	Client
	ChatWithFormat(ctx context.Context, messages []Message, tools []ToolDef, format *ResponseFormat) (*ChatResponse, error)
}
