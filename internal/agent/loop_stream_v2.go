package agent

import (
	"context"
	"fmt"
	"io"
	"time"

	
	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ChatStreamV2 增强版流式对话，通过 StreamEventWriter 发射结构化事件
// 支持：文本增量、工具调用开始/结束、迭代进度、思考过程
// ChatStreamV2 增强版流式对话，通过 StreamEventWriter 发射结构化事件
// 支持：文本增量、工具调用开始/结束、迭代进度、思考过程
func (a *LoopAgent) ChatStreamV2(ctx context.Context, userMessage string, history []llm.Message, onEvent StreamEventWriter) (string, []llm.Message, error) {
	if onEvent == nil {
		onEvent = func(event StreamEvent) {} // no-op
	}

	prep := a.prepareHistory(ctx, userMessage, history)
	history = prep.history
	if prep.blocked != "" {
		onEvent(StreamEvent{Type: EventDelta, Content: prep.blocked})
		return prep.blocked, history, nil
	}

	// 上下文窗口管理
	history = a.fitContextWindow(history)

	// 检查 LLM 客户端是否支持流式
	streamer, canStream := a.llmClient.(interface {
		StreamChat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.StreamReader, error)
	})

	for i := 0; i < maxIterations; i++ {
		onEvent(StreamEvent{
			Type:      EventIteration,
			Iteration: i + 1,
			MaxIter:   maxIterations,
		})

		// 预算检查
		if a.usageTracker != nil {
			if err := a.usageTracker.CheckBudget(); err != nil {
				a.traceLog("budget_exceeded", map[string]any{"total": a.usageTracker.TotalTokens(), "budget": a.usageTracker.Budget()})
				return "", history, fmt.Errorf("token 预算已耗尽 (已用 %d / 预算 %d)",
					a.usageTracker.TotalTokens(), a.usageTracker.Budget())
			}
		}

		a.traceLog("iteration", map[string]any{"i": i, "messages": len(history), "stream": canStream})

		if canStream {
			finalContent, newHistory, done, err := a.streamIteration(ctx, streamer, userMessage, history, i, onEvent)
			if err != nil {
				return "", history, err
			}
			history = newHistory
			if done {
				return finalContent, history, nil
			}
			// not done = tool calls executed, continue loop
		} else {
			// 不支持流式，降级为普通模式
			onEvent(StreamEvent{Type: EventStatus, Status: "waiting_llm"})
			resp, err := a.llmClient.Chat(ctx, history, a.toolDefs)
			if err != nil {
				return "", history, fmt.Errorf("LLM call failed: %w", err)
			}
			if a.usageTracker != nil && resp.Usage.TotalTokens > 0 {
				a.usageTracker.Record(resp.Usage)
				a.traceLog("token_usage", map[string]any{"total": resp.Usage.TotalTokens, "cumulative": a.usageTracker.TotalTokens()})
			}
			history = append(history, resp.Message)

			if len(resp.Message.ToolCalls) == 0 {
				pc := a.postCheckResponse(ctx, userMessage, resp.Message.Content, history, i)
				if pc.retry {
					onEvent(StreamEvent{Type: EventStatus, Status: "retrying"})
					history = append(history, pc.retryMsgs...)
					continue
				}
				onEvent(StreamEvent{Type: EventDelta, Content: pc.content})
				a.traceLog("final_answer", map[string]any{"content_len": len(pc.content)})
				return pc.content, history, nil
			}

			history = a.executeToolCallsWithEvents(ctx, resp.Message.ToolCalls, history, onEvent)
			history = a.fitContextWindow(history)
		}
	}

	return "", history, fmt.Errorf("reached maximum iterations (%d)", maxIterations)
}

// streamIteration 处理一次流式 LLM 调用迭代
// 返回 (finalContent, history, done, error)
// done=true 表示对话完成（收到文本回复）; done=false 表示执行了工具调用需继续循环
func (a *LoopAgent) streamIteration(ctx context.Context, streamer interface {
	StreamChat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.StreamReader, error)
}, userMessage string, history []llm.Message, iteration int, onEvent StreamEventWriter) (string, []llm.Message, bool, error) {

	sr, err := streamer.StreamChat(ctx, history, a.toolDefs)
	if err != nil {
		return "", history, false, fmt.Errorf("LLM stream call failed: %w", err)
	}

	firstDelta, err := sr.Recv()
	if err != nil {
		sr.Close()
		if err == io.EOF {
			return "", history, false, fmt.Errorf("LLM returned empty stream")
		}
		return "", history, false, fmt.Errorf("LLM stream recv: %w", err)
	}

	// 工具调用分支
	if len(firstDelta.ToolCalls) > 0 {
		onEvent(StreamEvent{Type: EventStatus, Status: "calling_tools"})
		resp, err := collectWithFirst(sr, firstDelta)
		sr.Close()
		if err != nil {
			return "", history, false, fmt.Errorf("collect tool calls: %w", err)
		}
		if a.usageTracker != nil && resp.Usage.TotalTokens > 0 {
			a.usageTracker.Record(resp.Usage)
			a.traceLog("token_usage", map[string]any{"total": resp.Usage.TotalTokens, "cumulative": a.usageTracker.TotalTokens()})
		}
		history = append(history, resp.Message)
		history = a.executeToolCallsWithEvents(ctx, resp.Message.ToolCalls, history, onEvent)
		history = a.fitContextWindow(history)
		return "", history, false, nil
	}

	// 文本回复分支：流式输出
	var fullContent string
	if firstDelta.Content != "" {
		onEvent(StreamEvent{Type: EventDelta, Content: firstDelta.Content})
		fullContent = firstDelta.Content
	}

	if firstDelta.Done {
		sr.Close()
		a.recordStreamUsage(firstDelta.Usage)
		msg := llm.Message{Role: "assistant", Content: fullContent}
		history = append(history, msg)
		a.traceLog("final_answer_stream", map[string]any{"content_len": len(fullContent)})
		return fullContent, history, true, nil
	}

	// 继续读取流
	for {
		delta, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			sr.Close()
			return "", history, false, fmt.Errorf("stream recv: %w", err)
		}

		if delta.Content != "" {
			onEvent(StreamEvent{Type: EventDelta, Content: delta.Content})
			fullContent += delta.Content
		}

		// 中途出现 tool_calls（少见但可能）
		if len(delta.ToolCalls) > 0 {
			resp, err := collectWithFirst(sr, delta)
			sr.Close()
			if err != nil {
				return "", history, false, fmt.Errorf("collect mid-stream tool calls: %w", err)
			}
			resp.Message.Content = fullContent + resp.Message.Content
			history = append(history, resp.Message)
			history = a.executeToolCallsWithEvents(ctx, resp.Message.ToolCalls, history, onEvent)
			history = a.fitContextWindow(history)
			return "", history, false, nil
		}

		a.recordStreamUsage(delta.Usage)
		if delta.Done {
			a.recordStreamUsage(delta.Usage)
			break
		}
	}
	sr.Close()

	// 后置检查
	pc := a.postCheckResponse(ctx, userMessage, fullContent, history, iteration)
	if pc.retry {
		history = append(history, llm.Message{Role: "assistant", Content: fullContent})
		history = append(history, pc.retryMsgs...)
		return "", history, false, nil
	}

	msg := llm.Message{Role: "assistant", Content: pc.content}
	history = append(history, msg)
	a.traceLog("final_answer_stream", map[string]any{"content_len": len(pc.content)})
	if a.usageTracker != nil {
		a.traceLog("token_usage_summary", map[string]any{"cumulative": a.usageTracker.TotalTokens()})
	}
	return pc.content, history, true, nil
}

// recordStreamUsage 记录流式 chunk 中的 usage 信息
func (a *LoopAgent) recordStreamUsage(usage *llm.Usage) {
	if usage == nil || a.usageTracker == nil {
		return
	}
	a.usageTracker.Record(llm.Usage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	})
}


func (a *LoopAgent) executeToolCallsWithEvents(ctx context.Context, toolCalls []llm.ToolCall, history []llm.Message, onEvent StreamEventWriter) []llm.Message {
	for _, tc := range toolCalls {
		// 发射 tool_start 事件
		onEvent(StreamEvent{
			Type:       EventToolStart,
			ToolCallID: tc.ID,
			ToolName:   tc.Function.Name,
			ToolArgs:   tc.Function.Arguments,
		})

		start := time.Now()

		// 复用 executeOneToolSafe（包含 panic recovery、超时、重试、错误分类）
		result := a.executeOneToolSafe(ctx, tc)
		elapsed := time.Since(start).Milliseconds()

		// 判断是否出错（error 前缀检测）
		var toolErr string
		if len(result.content) > 12 && result.content[:12] == "[TOOL_ERROR " {
			toolErr = result.content
		}

		// 发射 tool_end 事件
		onEvent(StreamEvent{
			Type:       EventToolEnd,
			ToolCallID: tc.ID,
			ToolName:   tc.Function.Name,
			ToolResult: truncateForEvent(result.content, 500),
			ToolError:  toolErr,
			Duration:   elapsed,
		})

		history = append(history, llm.Message{
			Role:       "tool",
			Content:    result.content,
			ToolCallID: result.callID,
		})
	}
	return history
}

// truncateForEvent 截断字符串用于事件传输（避免过大的 SSE payload）
func truncateForEvent(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// collectWithFirst 从一个已经读取了 firstDelta 的 StreamReader 中收集完整响应
func collectWithFirst(sr *llm.StreamReader, first *llm.StreamDelta) (*llm.ChatResponse, error) {
	if first.Done {
		msg := llm.Message{Role: "assistant", Content: first.Content}
		if len(first.ToolCalls) > 0 {
			for _, tcd := range first.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
					ID:   tcd.ID,
					Type: tcd.Type,
					Function: llm.FunctionCall{
						Name:      tcd.Function.Name,
						Arguments: tcd.Function.Arguments,
					},
				})
			}
		}
		return &llm.ChatResponse{
			Message:      msg,
			FinishReason: first.FinishReason,
		}, nil
	}

	resp, err := sr.Collect()
	if err != nil {
		return nil, err
	}

	resp.Message.Content = first.Content + resp.Message.Content

	if len(first.ToolCalls) > 0 && len(resp.Message.ToolCalls) > 0 {
		for _, ftc := range first.ToolCalls {
			if ftc.Index < len(resp.Message.ToolCalls) {
				tc := &resp.Message.ToolCalls[ftc.Index]
				if ftc.ID != "" && tc.ID == "" {
					tc.ID = ftc.ID
				}
				if ftc.Type != "" && tc.Type == "" {
					tc.Type = ftc.Type
				}
				if ftc.Function.Name != "" && tc.Function.Name == "" {
					tc.Function.Name = ftc.Function.Name
				}
				tc.Function.Arguments = ftc.Function.Arguments + tc.Function.Arguments
			}
		}
	} else if len(first.ToolCalls) > 0 {
		for _, tcd := range first.ToolCalls {
			resp.Message.ToolCalls = append(resp.Message.ToolCalls, llm.ToolCall{
				ID:   tcd.ID,
				Type: tcd.Type,
				Function: llm.FunctionCall{
					Name:      tcd.Function.Name,
					Arguments: tcd.Function.Arguments,
				},
			})
		}
	}

	return resp, nil
}
