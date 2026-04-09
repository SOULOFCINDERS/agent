package agent

import (
	"context"

	gd "github.com/SOULOFCINDERS/agent/internal/domain/guardrail"
	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// prepareHistory 执行对话前的公共准备工作：
//   - 注入 system prompt
//   - 追加 user message
//   - 输入 guardrail
//   - 注入相关记忆
//   - 短期记忆压缩
//   - proactive search 检测
//
// 返回处理后的 history 和可能的 guardrail 拦截结果。
// 如果 blocked != ""，调用方应直接返回该内容。
type prepareResult struct {
	history []llm.Message
	blocked string // guardrail 拦截文本，为空表示未拦截
}

func (a *LoopAgent) prepareHistory(ctx context.Context, userMessage string, history []llm.Message) prepareResult {
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
			return prepareResult{history: history, blocked: gr.BlockReason}
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

	// Proactive Search
	proactiveResult := detectProactiveSearch(userMessage, a.toolDefs)
	if proactiveResult.ShouldSearch {
		a.traceLog("proactive_search", map[string]any{
			"entity": proactiveResult.Entity,
			"reason": proactiveResult.Reason,
		})
		history = append(history, buildProactiveSearchMessage(proactiveResult.Entity))
	}

	return prepareResult{history: history}
}

// postCheckResult 后置检查的结果
type postCheckResult struct {
	content   string        // 最终内容（可能被修正）
	retry     bool          // 是否需要重试
	retryMsgs []llm.Message // retry 时追加到 history 的消息
}

// postCheckResponse 执行对话后的公共后置检查：
//   - 知识盲区检测
//   - 推理一致性检查
//   - Fabrication Guard
//   - Verification Agent
//   - 输出 Guardrail
//
// iteration: 当前循环轮次（仅第 0 轮执行某些检测）
func (a *LoopAgent) postCheckResponse(ctx context.Context, userMessage, finalContent string, history []llm.Message, iteration int) postCheckResult {
	// 知识盲区检测
	if iteration == 0 && hasWebSearchTool(a.toolDefs) {
		if gap, pattern := detectKnowledgeGap(finalContent); gap {
			a.traceLog("knowledge_gap_detected", map[string]any{"pattern": pattern, "retry": true})
			return postCheckResult{
				content:   finalContent,
				retry:     true,
				retryMsgs: []llm.Message{buildSearchNudge(userMessage)},
			}
		}
	}

	// 推理一致性检查
	if iteration == 0 {
		rCheck := detectReasoningContradiction(finalContent)
		if rCheck.HasContradiction {
			a.traceLog("reasoning_contradiction", map[string]any{
				"reasoning_claim":  rCheck.ReasoningClaim,
				"conclusion_claim": rCheck.ConclusionClaim,
			})
			return postCheckResult{
				content:   finalContent,
				retry:     true,
				retryMsgs: []llm.Message{buildReasoningFixNudge(userMessage, rCheck)},
			}
		}
	}

	// Fabrication Guard
	fabCheck := DetectFabrication(userMessage, finalContent, history, a.toolDefs)
	if fabCheck.HasFabrication() {
		a.traceLog("fabrication_detected", map[string]any{
			"numeric_risk":      fabCheck.NumericRisk,
			"fabricated_urls":   fabCheck.FabricatedURLs,
			"unverified_quotes": fabCheck.SuspiciousQuotes,
			"unverified_books":  fabCheck.SuspiciousBooks,
		})
		if iteration == 0 && fabCheck.NeedsRegeneration() {
			return postCheckResult{
				content:   finalContent,
				retry:     true,
				retryMsgs: []llm.Message{buildCalcNudge(userMessage)},
			}
		}
		if fabCheck.NeedsContentFix() {
			finalContent = FixFabricatedContent(finalContent, fabCheck)
		}
	}

	// Verification Agent
	if a.verifier != nil && a.verifier.IsEnabled() && NeedsVerification(userMessage, finalContent, history) {
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
			a.traceLog("guardrail_block", map[string]any{"guard": gr.GuardName, "phase": "output", "reason": gr.BlockReason})
			finalContent = "抱歉，生成的内容未通过安全检查，请换个方式提问"
		case gd.ActionRedact:
			a.traceLog("guardrail_redact", map[string]any{"guard": gr.GuardName, "phase": "output", "violations": len(gr.Violations)})
			finalContent = gr.RedactedContent
		}
	}

	return postCheckResult{content: finalContent}
}
