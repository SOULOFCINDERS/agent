package agent

import (
	"fmt"
)

// ToolErrorKind 工具错误类型分类
type ToolErrorKind int

const (
	// ErrRetryable 可重试错误：参数格式不对、JSON 解析失败等
	ErrRetryable ToolErrorKind = iota
	// ErrNotRetryable 不可重试错误：权限不足、资源不存在等
	ErrNotRetryable
	// ErrTimeout 超时错误
	ErrTimeout
	// ErrPanic 工具内部 panic
	ErrPanic
	// ErrUnknownTool 工具不存在
	ErrUnknownTool
)

func (k ToolErrorKind) String() string {
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

// ToolError 结构化工具错误
type ToolError struct {
	Kind    ToolErrorKind
	Tool    string // 工具名
	Message string // 错误信息
	Cause   error  // 原始错误
}

func (e *ToolError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %s (cause: %v)", e.Kind, e.Tool, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Kind, e.Tool, e.Message)
}

func (e *ToolError) Unwrap() error {
	return e.Cause
}

// classifyError 将原始 error 分类为 ToolError
// 通过错误信息中的关键词判断类别
func classifyError(toolName string, err error) *ToolError {
	if err == nil {
		return nil
	}

	msg := err.Error()

	// panic
	if containsAny(msg, "panicked", "panic:", "runtime error") {
		return &ToolError{Kind: ErrPanic, Tool: toolName, Message: msg, Cause: err}
	}

	// 超时
	if containsAny(msg, "timeout", "deadline exceeded", "context canceled") {
		return &ToolError{Kind: ErrTimeout, Tool: toolName, Message: msg, Cause: err}
	}

	// 可重试：参数/格式问题
	if containsAny(msg, "invalid", "unmarshal", "parse", "missing", "required",
		"invalid tool arguments", "json", "syntax error", "unexpected",
		"type mismatch", "cannot convert") {
		return &ToolError{Kind: ErrRetryable, Tool: toolName, Message: msg, Cause: err}
	}

	// 不可重试：权限/资源不存在
	if containsAny(msg, "permission denied", "not found", "no such file",
		"access denied", "forbidden", "unauthorized", "404", "401", "403") {
		return &ToolError{Kind: ErrNotRetryable, Tool: toolName, Message: msg, Cause: err}
	}

	// 默认：可重试（给 LLM 一次机会修正）
	return &ToolError{Kind: ErrRetryable, Tool: toolName, Message: msg, Cause: err}
}

// containsAny 检查 s 是否包含 substrs 中的任意一个（不区分大小写）
func containsAny(s string, substrs ...string) bool {
	lower := toLower(s)
	for _, sub := range substrs {
		if contains(lower, toLower(sub)) {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// formatErrorForLLM 生成结构化错误信息，帮助 LLM 理解错误并修正
func formatErrorForLLM(te *ToolError, retryCount, maxRetries int) string {
	var hint string

	switch te.Kind {
	case ErrRetryable:
		hint = fmt.Sprintf(
			"[TOOL_ERROR type=retryable retries=%d/%d]\n"+
				"工具 %q 执行失败: %s\n"+
				"请检查参数格式是否正确，然后用修正后的参数重新调用。",
			retryCount, maxRetries, te.Tool, te.Message)

	case ErrNotRetryable:
		hint = fmt.Sprintf(
			"[TOOL_ERROR type=permanent]\n"+
				"工具 %q 执行失败（不可重试）: %s\n"+
				"请不要再次调用此工具执行相同操作，改用其他方式完成任务或告知用户。",
			te.Tool, te.Message)

	case ErrTimeout:
		hint = fmt.Sprintf(
			"[TOOL_ERROR type=timeout retries=%d/%d]\n"+
				"工具 %q 执行超时: %s\n"+
				"可以稍后重试，或换用其他工具。",
			retryCount, maxRetries, te.Tool, te.Message)

	case ErrPanic:
		hint = fmt.Sprintf(
			"[TOOL_ERROR type=internal_error]\n"+
				"工具 %q 发生内部错误: %s\n"+
				"请不要再次调用此工具，改用其他方式或告知用户。",
			te.Tool, te.Message)

	case ErrUnknownTool:
		hint = fmt.Sprintf(
			"[TOOL_ERROR type=unknown_tool]\n"+
				"工具 %q 不存在。可用的工具请参考 system prompt 中的定义。\n"+
				"请选择一个存在的工具。",
			te.Tool)

	default:
		hint = fmt.Sprintf("[TOOL_ERROR] %s: %s", te.Tool, te.Message)
	}

	return hint
}
