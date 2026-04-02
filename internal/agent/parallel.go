package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

const defaultToolTimeout = 30 * time.Second

// toolResult 保存单个工具调用的结果
type toolResult struct {
	callID  string
	content string
}

// executeToolCallsParallel 并发执行多个工具调用
// 结果按 toolCalls 的原始顺序追加到 history
func (a *LoopAgent) executeToolCallsParallel(ctx context.Context, toolCalls []llm.ToolCall, history []llm.Message) []llm.Message {
	n := len(toolCalls)
	if n == 0 {
		return history
	}

	// 单个工具调用直接串行，无需 goroutine 开销
	if n == 1 {
		return a.executeToolCallsSingle(ctx, toolCalls, history)
	}

	a.traceLog("parallel_exec", map[string]any{"count": n})
	start := time.Now()

	// 预分配结果槽位，保证顺序
	results := make([]toolResult, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i, tc := range toolCalls {
		go func(idx int, tc llm.ToolCall) {
			defer wg.Done()
			results[idx] = a.executeOneToolSafe(ctx, tc)
		}(i, tc)
	}

	wg.Wait()

	elapsed := time.Since(start)
	a.traceLog("parallel_done", map[string]any{
		"count":   n,
		"elapsed": elapsed.String(),
	})

	// 按原始顺序追加到 history
	for _, r := range results {
		history = append(history, llm.Message{
			Role:       "tool",
			Content:    r.content,
			ToolCallID: r.callID,
		})
	}

	return history
}

// executeToolCallsSingle 串行执行（单个工具或降级场景）
func (a *LoopAgent) executeToolCallsSingle(ctx context.Context, toolCalls []llm.ToolCall, history []llm.Message) []llm.Message {
	for _, tc := range toolCalls {
		r := a.executeOneToolSafe(ctx, tc)
		history = append(history, llm.Message{
			Role:       "tool",
			Content:    r.content,
			ToolCallID: r.callID,
		})
	}
	return history
}

// executeOneToolSafe 执行单个工具，包含：
//  1. panic recovery — 工具 panic 不会崩进程
//  2. 超时控制 — 每个工具最多 30 秒
//  3. 错误分类 — 结构化错误信息帮助 LLM 修正
//  4. 重试感知 — 记录失败次数，超限后拒绝重试
func (a *LoopAgent) executeOneToolSafe(ctx context.Context, tc llm.ToolCall) toolResult {
	toolName := tc.Function.Name
	toolArgs := tc.Function.Arguments

	a.traceLog("tool_call", map[string]any{
		"tool": toolName,
		"args": toolArgs,
	})

	// 1. 检查工具是否存在
	t := a.registry.Get(toolName)
	if t == nil {
		te := &ToolError{Kind: ErrUnknownTool, Tool: toolName, Message: "tool not found"}
		errMsg := formatErrorForLLM(te, 0, 0)
		a.traceLog("tool_error", map[string]any{"tool": toolName, "kind": te.Kind.String(), "error": te.Message})
		return toolResult{callID: tc.ID, content: errMsg}
	}

	// 2. 检查重试次数
	if a.retryTracker != nil && !a.retryTracker.canRetry(toolName, toolArgs) {
		count := a.retryTracker.getCount(toolName, toolArgs)
		errMsg := fmt.Sprintf(
			"[TOOL_ERROR type=max_retries_exceeded]\n"+
				"工具 %q 已失败 %d 次（相同参数），不再重试。\n"+
				"请更换参数、使用其他工具，或直接回复用户说明情况。",
			toolName, count)
		a.traceLog("tool_max_retries", map[string]any{"tool": toolName, "count": count})
		return toolResult{callID: tc.ID, content: errMsg}
	}

	// 3. 解析参数
	var args map[string]any
	if err := json.Unmarshal([]byte(toolArgs), &args); err != nil {
		te := classifyError(toolName, fmt.Errorf("invalid tool arguments: %w", err))
		retryCount := 0
		if a.retryTracker != nil {
			retryCount = a.retryTracker.record(toolName, toolArgs)
		}
		errMsg := formatErrorForLLM(te, retryCount, defaultMaxRetries)
		a.traceLog("tool_error", map[string]any{"tool": toolName, "kind": te.Kind.String(), "error": te.Message, "retry": retryCount})
		return toolResult{callID: tc.ID, content: errMsg}
	}

	// 4. 带超时 + panic recovery 执行工具
	content, err := a.executeWithRecovery(ctx, t, toolName, args)

	if err != nil {
		// 错误分类
		te := classifyError(toolName, err)

		retryCount := 0
		if a.retryTracker != nil {
			if te.Kind == ErrRetryable || te.Kind == ErrTimeout {
				retryCount = a.retryTracker.record(toolName, toolArgs)
			} else {
				// 不可重试的错误，直接设为 max 防止后续重试
				for i := 0; i < defaultMaxRetries; i++ {
					a.retryTracker.record(toolName, toolArgs)
				}
				retryCount = defaultMaxRetries
			}
		}

		errMsg := formatErrorForLLM(te, retryCount, defaultMaxRetries)
		a.traceLog("tool_error", map[string]any{
			"tool":  toolName,
			"kind":  te.Kind.String(),
			"error": te.Message,
			"retry": retryCount,
		})
		return toolResult{callID: tc.ID, content: errMsg}
	}

	// 成功：重置重试计数
	if a.retryTracker != nil {
		a.retryTracker.reset(toolName, toolArgs)
	}

	// 结果截断
	if len(content) > 8000 {
		content = content[:8000] + "\n... [truncated]"
	}

	a.traceLog("tool_result", map[string]any{
		"tool":       toolName,
		"result_len": len(content),
	})

	return toolResult{callID: tc.ID, content: content}
}

// executeWithRecovery 带 panic recovery 和超时控制执行工具
func (a *LoopAgent) executeWithRecovery(ctx context.Context, t tools.Tool, toolName string, args map[string]any) (content string, err error) {
	// 超时控制
	toolCtx, cancel := context.WithTimeout(ctx, defaultToolTimeout)
	defer cancel()

	// 用 channel + goroutine 实现 panic recovery
	type execResult struct {
		result any
		err    error
	}
	ch := make(chan execResult, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- execResult{err: fmt.Errorf("tool panicked: %v", r)}
			}
		}()
		result, execErr := t.Execute(toolCtx, args)
		ch <- execResult{result: result, err: execErr}
	}()

	// 等待结果或超时
	select {
	case res := <-ch:
		if res.err != nil {
			return "", res.err
		}
		return tools.FormatResult(res.result), nil

	case <-toolCtx.Done():
		return "", fmt.Errorf("tool execution timeout after %s", defaultToolTimeout)
	}
}
