package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// StreamWriter 定义流式输出回调
type StreamWriter func(delta string)

// ChatStream 流式版本的 Chat
// 当 LLM 返回最终文本时，通过 onDelta 逐 token 回调
// 工具调用轮次仍使用 Collect 方式等待完整参数
func (a *LoopAgent) ChatStream(ctx context.Context, userMessage string, history []llm.Message, onDelta StreamWriter) (string, []llm.Message, error) {
	if len(history) == 0 && a.systemPrompt != "" {
		sysContent := a.systemPrompt
		history = append(history, llm.Message{
			Role:    "system",
			Content: sysContent,
		})
	}

	history = append(history, llm.Message{
		Role:    "user",
		Content: userMessage,
	})

	// 注入相关记忆（基于当前查询，而非全量注入）
	if a.memStore != nil && a.memStore.Count() > 0 {
		memSummary := a.memStore.RelevantSummary(userMessage, 5)
		if memSummary != "" {
			memMsg := llm.Message{
				Role:    "system",
				Content: "## 相关记忆\n" + memSummary,
			}
			insertIdx := 1
			if insertIdx > len(history) {
				insertIdx = len(history)
			}
			history = append(history[:insertIdx], append([]llm.Message{memMsg}, history[insertIdx:]...)...)
		}
	}

	// 短期记忆压缩
	if a.compressor != nil && a.compressor.NeedCompress(history) {
		cr, err := a.compressor.Compress(ctx, history)
		if err == nil && cr.WasCompressed {
			a.traceLog("history_compressed", map[string]any{
				"original":   cr.OriginalCount,
				"compressed": cr.CompressedCount,
			})
			history = cr.Messages
		}
	}

	// 上下文窗口管理：确保 history 不超出模型窗口
	history = a.fitContextWindow(history)

	// 检查 LLM 客户端是否支持流式
	streamer, canStream := a.llmClient.(interface {
		StreamChat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.StreamReader, error)
	})

	for i := 0; i < maxIterations; i++ {
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
			sr, err := streamer.StreamChat(ctx, history, a.toolDefs)
			if err != nil {
				return "", history, fmt.Errorf("LLM stream call failed: %w", err)
			}

			// 先 peek: 看第一个 delta 是文本还是 tool_call
			firstDelta, err := sr.Recv()
			if err != nil {
				sr.Close()
				if err == io.EOF {
					return "", history, fmt.Errorf("LLM returned empty stream")
				}
				return "", history, fmt.Errorf("LLM stream recv: %w", err)
			}

			if len(firstDelta.ToolCalls) > 0 {
				// 是工具调用，收集完整响应
				resp, err := collectWithFirst(sr, firstDelta)
				sr.Close()
				if err != nil {
					return "", history, fmt.Errorf("collect tool calls: %w", err)
				}
				if a.usageTracker != nil && resp.Usage.TotalTokens > 0 {
					a.usageTracker.Record(resp.Usage)
					a.traceLog("token_usage", map[string]any{"total": resp.Usage.TotalTokens, "cumulative": a.usageTracker.TotalTokens()})
				}
				history = append(history, resp.Message)
				history = a.executeToolCallsParallel(ctx, resp.Message.ToolCalls, history)
				// 每轮工具调用后检查上下文窗口
				history = a.fitContextWindow(history)
				continue
			}

			// 是文本回复，流式输出
			var fullContent string
			if firstDelta.Content != "" {
				onDelta(firstDelta.Content)
				fullContent = firstDelta.Content
			}

			if firstDelta.Done {
				sr.Close()
				msg := llm.Message{Role: "assistant", Content: fullContent}
				history = append(history, msg)
				a.traceLog("final_answer_stream", map[string]any{"content_len": len(fullContent)})
				return fullContent, history, nil
			}

			// 继续读取
			for {
				delta, err := sr.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					sr.Close()
					return "", history, fmt.Errorf("stream recv: %w", err)
				}

				if delta.Content != "" {
					onDelta(delta.Content)
					fullContent += delta.Content
				}

				// 如果中途出现 tool_calls（少见但可能），收集剩余并切换到工具调用
				if len(delta.ToolCalls) > 0 {
					resp, err := collectWithFirst(sr, delta)
					sr.Close()
					if err != nil {
						return "", history, fmt.Errorf("collect mid-stream tool calls: %w", err)
					}
					// 合并已有的 content
					resp.Message.Content = fullContent + resp.Message.Content
					history = append(history, resp.Message)
					history = a.executeToolCallsParallel(ctx, resp.Message.ToolCalls, history)
					// 每轮工具调用后检查上下文窗口
					history = a.fitContextWindow(history)
					goto continueLoop
				}

				if delta.Done {
					break
				}
			}
			sr.Close()

			msg := llm.Message{Role: "assistant", Content: fullContent}
			history = append(history, msg)
			a.traceLog("final_answer_stream", map[string]any{"content_len": len(fullContent)})
			if a.usageTracker != nil {
				a.traceLog("token_usage_summary", map[string]any{"cumulative": a.usageTracker.TotalTokens()})
			}
			return fullContent, history, nil

		} else {
			// 不支持流式，降级为普通模式
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
				// 模拟流式：一次性输出
				if onDelta != nil {
					onDelta(resp.Message.Content)
				}
				a.traceLog("final_answer", map[string]any{"content_len": len(resp.Message.Content)})
				return resp.Message.Content, history, nil
			}

			history = a.executeToolCallsParallel(ctx, resp.Message.ToolCalls, history)
			// 每轮工具调用后检查上下文窗口
			history = a.fitContextWindow(history)
		}

	continueLoop:
	}

	return "", history, fmt.Errorf("reached maximum iterations (%d)", maxIterations)
}



// collectWithFirst 从一个已经读取了 firstDelta 的 StreamReader 中收集完整响应
func collectWithFirst(sr *llm.StreamReader, first *llm.StreamDelta) (*llm.ChatResponse, error) {
	// 如果 first 就是 Done，直接构建
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

	// 用 Collect 收集剩余，然后合并 first
	resp, err := sr.Collect()
	if err != nil {
		return nil, err
	}

	// 把 first 的内容 merge 进去
	resp.Message.Content = first.Content + resp.Message.Content

	// 合并 tool calls（first 中的 delta 对应 index 可能已经在 Collect 中了）
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
