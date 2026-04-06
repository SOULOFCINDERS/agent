package tool

import (
	"context"
	"encoding/json"
)

// Tool 工具接口（Domain 层定义）
type Tool interface {
	Name() string
	Execute(ctx context.Context, args map[string]any) (any, error)
}

// ToolWithSchema 带 schema 描述的工具，可暴露给 LLM function calling
type ToolWithSchema interface {
	Tool
	Description() string
	ParameterSchema() json.RawMessage
}

// Registry 工具注册表接口（Domain 层定义）
// 具体实现在 infrastructure 层
type Registry interface {
	Register(t Tool)
	Get(name string) Tool
}

// ---- 工具错误分类 ----

// ErrorKind 工具错误类型分类
type ErrorKind int

const (
	ErrRetryable    ErrorKind = iota // 可重试错误：参数格式不对、JSON 解析失败等
	ErrNotRetryable                  // 不可重试错误：权限不足、资源不存在等
	ErrTimeout                       // 超时错误
	ErrPanic                         // 工具内部 panic
	ErrUnknownTool                   // 工具不存在
)

func (k ErrorKind) String() string {
	switch k {
	case ErrRetryable:
		return "retryable"
	case ErrNotRetryable:
		return "not_retryable"
	case ErrTimeout:
		return "timeout"
	case ErrPanic:
		return "panic"
	case ErrUnknownTool:
		return "unknown_tool"
	default:
		return "unknown"
	}
}

// Error 结构化工具错误
type Error struct {
	Kind    ErrorKind
	Tool    string // 工具名
	Message string // 错误信息
	Cause   error  // 原始错误
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return "[" + e.Kind.String() + "] " + e.Tool + ": " + e.Message + " (cause: " + e.Cause.Error() + ")"
	}
	return "[" + e.Kind.String() + "] " + e.Tool + ": " + e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}
