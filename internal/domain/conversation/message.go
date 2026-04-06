// Package conversation 定义对话领域的核心值对象。
// 这是整个 Agent 框架最基础的领域模型，被所有其他领域依赖。
package conversation

import "encoding/json"

// Role 消息角色
type Role = string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 对话消息（值对象）
type Message struct {
	Role       string     `json:"role"`                   // system / user / assistant / tool
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant 返回的工具调用
	ToolCallID string     `json:"tool_call_id,omitempty"` // role=tool 时必填
}

// IsToolResponse 充血行为：判断是否为工具响应消息
func (m Message) IsToolResponse() bool {
	return m.Role == RoleTool && m.ToolCallID != ""
}

// HasToolCalls 充血行为：判断是否包含工具调用
func (m Message) HasToolCalls() bool {
	return len(m.ToolCalls) > 0
}

// IsSystemMessage 充血行为
func (m Message) IsSystemMessage() bool {
	return m.Role == RoleSystem
}

// ToolCall 工具调用（值对象）
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall 函数调用（值对象）
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ToolDef 描述一个工具，供 LLM function calling 使用（值对象）
type ToolDef struct {
	Type     string  `json:"type"` // "function"
	Function FuncDef `json:"function"`
}

// FuncDef 函数定义（值对象）
type FuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Usage 单次 LLM 调用的 token 用量（值对象）
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse 表示 LLM 返回（值对象）
type ChatResponse struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"` // "stop" / "tool_calls"
	Usage        Usage   `json:"usage"`
}

// StreamDelta 流式增量（值对象）
type StreamDelta struct {
	Content      string          // 文本增量
	ToolCalls    []ToolCallDelta // 工具调用增量
	Done         bool            // 是否结束
	FinishReason string          // stop / tool_calls
	Usage        *Usage          // 最后一个 chunk 可能包含 usage
}

// ToolCallDelta 工具调用的增量（值对象）
type ToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

// ResponseFormat OpenAI 兼容的 response_format（值对象）
type ResponseFormat struct {
	Type       string              `json:"type"`                  // "json_schema" | "json_object" | "text"
	JSONSchema *ResponseJSONSchema `json:"json_schema,omitempty"` // type=json_schema 时必填
}

// ResponseJSONSchema response_format.json_schema 部分
type ResponseJSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Strict      bool            `json:"strict"`
	Schema      json.RawMessage `json:"schema"`
}
