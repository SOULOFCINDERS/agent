package agent

import (
	"fmt"

	dtool "github.com/SOULOFCINDERS/agent/internal/domain/tool"
)

// ---------- 类型别名：从 domain/tool 引入 ----------

type ToolErrorKind = dtool.ErrorKind
type ToolError = dtool.Error

// 常量别名
var (
	ErrRetryable    = dtool.ErrRetryable
	ErrNotRetryable = dtool.ErrNotRetryable
	ErrTimeout      = dtool.ErrTimeout
	ErrPanic        = dtool.ErrPanic
	ErrUnknownTool  = dtool.ErrUnknownTool
)

// classifyError 将原始 error 分类为 ToolError
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

	// 默认：可重试
	return &ToolError{Kind: ErrRetryable, Tool: toolName, Message: msg, Cause: err}
}

// containsAny 检查 s 是否包含 substrs 中的任意一个
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

// formatErrorForLLM 生成结构化错误信息
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
