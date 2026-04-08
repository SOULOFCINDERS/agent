package agent

import (
	"context"
	"fmt"
	"io"
	"time"

	gd "github.com/SOULOFCINDERS/agent/internal/domain/guardrail"
	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ChatStreamV2 增强版流式对话，通过 StreamEventWriter 发射结构化事件
// 支持：文本增量、工具调用开始/结束、迭代进度、思考过程
func (a *LoopAgent) ChatStreamV2(ctx context.Context, userMessage string, history []llm.Message, onEvent StreamEventWriter) (string, []llm.Message, error) {
	if onEvent == nil {
		onEvent = func(event StreamEvent) {} // no-op
	}

	if len(history) == 0 && a.systemPrompt != "" {
		history = append(history, llm.Message{
			Role:    "system",
			Content: a.systemPrompt,
		})
	}

	history = append(history, llm.Message{
		Role:    "user",
		Content: userMessage,
	})

	// Guardrail: 输入安全检查
	if a.guardrails != nil {
		gr := a.guardrails.Run(ctx, gd.PhaseInput, userMessage)
		switch gr.Action {
		case gd.ActionBlock:
			a.traceLog("guardrail_block", map[string]any{"guard": gr.GuardName, "phase": "input", "reason": gr.BlockReason})
			onEvent(StreamEvent{Type: EventDelta, Content: gr.BlockReason})
			return gr.BlockReason, history, nil
		case gd.ActionRedact:
			a.traceLog("guardrail_redact", map[string]any{"guard": gr.GuardName, "phase": "input", "violations": len(gr.Violations)})
			history[len(history)-1] = llm.Message{Role: "user", Content: gr.RedactedContent}
		}
	}

	// 注入相关记忆
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

	// 上下文窗口管理
	history = a.fitContextWindow(history)

	// 检查 LLM 客户端是否支持流式
	streamer, canStream := a.llmClient.(interface {
		StreamChat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.StreamReader, error)
	})

	for i := 0; i < maxIterations; i++ {
		// 发射迭代事件
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
			sr, err := streamer.StreamChat(ctx, history, a.toolDefs)
			if err != nil {
				return "", history, fmt.Errorf("LLM stream call failed: %w", err)
			}

			// peek 第一个 delta：判断是文本还是 tool_call
			firstDelta, err := sr.Recv()
			if err != nil {
				sr.Close()
				if err == io.EOF {
					return "", history, fmt.Errorf("LLM returned empty stream")
				}
				return "", history, fmt.Errorf("LLM stream recv: %w", err)
			}

			if len(firstDelta.ToolCalls) > 0 {
				// 工具调用：收集完整响应
				onEvent(StreamEvent{Type: EventStatus, Status: "calling_tools"})
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
				history = a.executeToolCallsWithEvents(ctx, resp.Message.ToolCalls, history, onEvent)
				history = a.fitContextWindow(history)
				continue
			}

			// 文本回复：流式输出
			var fullContent string
			if firstDelta.Content != "" {
				onEvent(StreamEvent{Type: EventDelta, Content: firstDelta.Content})
				fullContent = firstDelta.Content
			}

			if firstDelta.Done {
				sr.Close()
				if firstDelta.Usage != nil && a.usageTracker != nil {
					a.usageTracker.Record(llm.Usage{
						PromptTokens:     firstDelta.Usage.PromptTokens,
						CompletionTokens: firstDelta.Usage.CompletionTokens,
						TotalTokens:      firstDelta.Usage.TotalTokens,
					})
				}
				msg := llm.Message{Role: "assistant", Content: fullContent}
				history = append(history, msg)
				a.traceLog("final_answer_stream", map[string]any{"content_len": len(fullContent)})
				return fullContent, history, nil
			}

			// 继续读取流
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
					onEvent(StreamEvent{Type: EventDelta, Content: delta.Content})
					fullContent += delta.Content
				}

				// 中途出现 tool_calls（少见但可能）
				if len(delta.ToolCalls) > 0 {
					resp, err := collectWithFirst(sr, delta)
					sr.Close()
					if err != nil {
						return "", history, fmt.Errorf("collect mid-stream tool calls: %w", err)
					}
					resp.Message.Content = fullContent + resp.Message.Content
					history = append(history, resp.Message)
					history = a.executeToolCallsWithEvents(ctx, resp.Message.ToolCalls, history, onEvent)
					history = a.fitContextWindow(history)
					goto continueLoop
				}

				if delta.Done {
					if delta.Usage != nil && a.usageTracker != nil {
						a.usageTracker.Record(llm.Usage{
							PromptTokens:     delta.Usage.PromptTokens,
							CompletionTokens: delta.Usage.CompletionTokens,
							TotalTokens:      delta.Usage.TotalTokens,
						})
					}
					break
				}
				// 流式最后一个 chunk 可能包含 usage
				if delta.Usage != nil && a.usageTracker != nil {
					a.usageTracker.Record(llm.Usage{
						PromptTokens:     delta.Usage.PromptTokens,
						CompletionTokens: delta.Usage.CompletionTokens,
						TotalTokens:      delta.Usage.TotalTokens,
					})
				}
			}
			sr.Close()

			finalContent := fullContent

			// 知识盲区检测：如果 LLM 用"训练截止"否定事实，且有搜索工具，自动触发搜索
			if i == 0 && hasWebSearchTool(a.toolDefs) {
				if gap, pattern := detectKnowledgeGap(finalContent); gap {
					a.traceLog("knowledge_gap_detected", map[string]any{"pattern": pattern, "retry": true})
					// 注入搜索提醒，让 LLM 重新回答（流式输出已经发了部分内容，追加提示）
					onEvent(StreamEvent{Type: EventStatus, Status: "searching_for_latest_info"})
					nudge := buildSearchNudge(userMessage)
					// 不保留否定性回复，直接注入搜索提醒
					history = append(history, llm.Message{Role: "assistant", Content: finalContent})
					history = append(history, nudge)
					continue
				}
			}

			// 推理一致性检查
			if i == 0 {
				rCheck := detectReasoningContradiction(finalContent)
				if rCheck.HasContradiction {
					a.traceLog("reasoning_contradiction", map[string]any{
						"reasoning_claim":  rCheck.ReasoningClaim,
						"conclusion_claim": rCheck.ConclusionClaim,
					})
					onEvent(StreamEvent{Type: EventStatus, Status: "fixing_reasoning_contradiction"})
					history = append(history, llm.Message{Role: "assistant", Content: finalContent})
					nudge := buildReasoningFixNudge(userMessage, rCheck)
					history = append(history, nudge)
					continue
				}
			}

			// P0: 数值幻觉检测
			if i == 0 && hasCalcToolDef(a.toolDefs) {
				numCheck := detectNumericRisk(userMessage, finalContent, history)
				if numCheck.HasRisk {
					a.traceLog("numeric_risk_detected", map[string]any{"numbers": numCheck.RiskNumbers, "retry": true})
					onEvent(StreamEvent{Type: EventStatus, Status: "recalculating_with_tool"})
					// 流式路径：保留当前回复在 history 中，追加计算提醒
					history = append(history, llm.Message{Role: "assistant", Content: finalContent})
					nudge := buildCalcNudge(userMessage)
					history = append(history, nudge)
					continue
				}
			}

			// P0: 虚构链接检测
			if fabricated := detectFabricatedURLs(finalContent, history); len(fabricated) > 0 {
				a.traceLog("fabricated_urls_detected", map[string]any{"count": len(fabricated), "urls": fabricated})
				finalContent = cleanFabricatedURLs(finalContent, fabricated)
			}

			// P0: 引用幻觉检测
			{
				citationCheck := detectUnverifiedCitations(finalContent, history)
				if citationCheck.HasUnverifiedQuotes || citationCheck.HasUnverifiedBooks {
					a.traceLog("unverified_citations_detected", map[string]any{
						"quotes": citationCheck.SuspiciousQuotes,
						"books":  citationCheck.SuspiciousBooks,
					})
					finalContent = cleanUnverifiedCitations(finalContent, citationCheck)
				}
			}

			// Verification Agent: 独立对抗性验证
			if a.verifier != nil && a.verifier.IsEnabled() && NeedsVerification(userMessage, finalContent, history) {
				onEvent(StreamEvent{Type: EventStatus, Status: "verifying_response"})
				vResult, vErr := a.verifier.Verify(ctx, userMessage, finalContent, history)
				if vErr != nil {
					a.traceLog("verifier_error", map[string]any{"error": vErr.Error()})
				} else {
					a.traceLog("verifier_result", map[string]any{
						"passed":   vResult.Passed,
						"issues":   len(vResult.Issues),
						"duration": vResult.Duration.Milliseconds(),
					})
					if !vResult.Passed {
						corrected, cErr := a.verifier.ApplyCorrection(ctx, userMessage, finalContent, vResult, history)
						if cErr == nil && corrected != finalContent {
							a.traceLog("verifier_corrected", map[string]any{"original_len": len(finalContent), "corrected_len": len(corrected)})
							finalContent = corrected
						}
					}
				}
			}

			// Guardrail: 输出安全检查
			if a.guardrails != nil {
				gr := a.guardrails.Run(ctx, gd.PhaseOutput, finalContent)
				switch gr.Action {
				case gd.ActionBlock:
					a.traceLog("guardrail_block", map[string]any{"guard": gr.GuardName, "phase": "output"})
					finalContent = "抱歉，生成的内容未通过安全检查，请换个方式提问"
				case gd.ActionRedact:
					a.traceLog("guardrail_redact", map[string]any{"guard": gr.GuardName, "phase": "output", "violations": len(gr.Violations)})
					finalContent = gr.RedactedContent
				}
			}
			msg := llm.Message{Role: "assistant", Content: finalContent}
			history = append(history, msg)
			a.traceLog("final_answer_stream", map[string]any{"content_len": len(finalContent)})
			if a.usageTracker != nil {
				a.traceLog("token_usage_summary", map[string]any{"cumulative": a.usageTracker.TotalTokens()})
			}
			return finalContent, history, nil

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
				// 知识盲区检测（非流式降级模式）
				if i == 0 && hasWebSearchTool(a.toolDefs) {
					if gap, pattern := detectKnowledgeGap(resp.Message.Content); gap {
						a.traceLog("knowledge_gap_detected", map[string]any{"pattern": pattern, "retry": true})
						onEvent(StreamEvent{Type: EventStatus, Status: "searching_for_latest_info"})
						nudge := buildSearchNudge(userMessage)
						history = append(history, nudge)
						continue
					}
				}
				onEvent(StreamEvent{Type: EventDelta, Content: resp.Message.Content})
				a.traceLog("final_answer", map[string]any{"content_len": len(resp.Message.Content)})
				return resp.Message.Content, history, nil
			}

			history = a.executeToolCallsWithEvents(ctx, resp.Message.ToolCalls, history, onEvent)
			history = a.fitContextWindow(history)
		}

	continueLoop:
	}

	return "", history, fmt.Errorf("reached maximum iterations (%d)", maxIterations)
}

// executeToolCallsWithEvents 执行工具调用并发射流式事件
// 注意：为保证事件顺序可预测（start→end 配对），这里使用串行执行
// 未来可改为并发执行 + 按顺序发射事件
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
