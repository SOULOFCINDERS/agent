package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/ctxwindow"
	gd "github.com/SOULOFCINDERS/agent/internal/domain/guardrail"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/memory"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

const maxIterations = 15

// LoopAgent 实现 LLM 驱动的对话循环 Agent
// 核心流程: User → LLM → [Tool Call → 执行 → 结果返回 LLM] → ... → 最终回复
type LoopAgent struct {
	llmClient    llm.Client
	registry     *tools.Registry
	systemPrompt string
	toolDefs     []llm.ToolDef
	trace        io.Writer
	memStore     *memory.Store
	compressor   *memory.Compressor
	retryTracker  *retryTracker
	usageTracker *llm.UsageTracker
	ctxManager      *ctxwindow.Manager       // 上下文窗口管理器（基础）
	smartCtxManager *ctxwindow.SmartManager  // 增强版上下文窗口管理器（支持摘要压缩）
	guardrails      gd.Pipeline              // 安全检查管道（可选）
	verifier        *Verifier                // 验证子 Agent（可选）
	enableNudge     bool                     // Phase 1: 是否启用 Nudge 上下文效率提醒
}

func NewLoopAgent(client llm.Client, reg *tools.Registry, systemPrompt string, trace io.Writer, memStore *memory.Store, compressor *memory.Compressor) *LoopAgent {
	if trace == nil {
		trace = io.Discard
	}

	// 从 registry 的 schema 构建 LLM tool definitions
	schemas := tools.BuiltinSchemas()
	var defs []llm.ToolDef
	for name, s := range schemas {
		if reg.Get(name) == nil {
			continue // 只注册已 register 的工具
		}
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.FuncDef{
				Name:        name,
				Description: s.Desc,
				Parameters:  s.Schema,
			},
		})
	}

	// 添加飞书工具 schema（如果已注册）
	feishuSchemas := feishuToolSchemas()
	for name, s := range feishuSchemas {
		if reg.Get(name) == nil {
			continue
		}
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.FuncDef{
				Name:        name,
				Description: s.Desc,
				Parameters:  s.Schema,
			},
		})
	}

	// 添加记忆工具 schema（如果已注册）
	memSchemas := tools.MemoryToolSchemas()
	for name, s := range memSchemas {
		if reg.Get(name) == nil {
			continue
		}
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.FuncDef{
				Name:        name,
				Description: s.Desc,
				Parameters:  s.Schema,
			},
		})
	}

	// 添加 RAG 工具 schema（如果已注册）
	ragSchemas := tools.RAGToolSchemas()
	for name, s := range ragSchemas {
		if reg.Get(name) == nil {
			continue
		}
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.FuncDef{
				Name:        name,
				Description: s.Desc,
				Parameters:  s.Schema,
			},
		})
	}

	return &LoopAgent{
		llmClient:    client,
		registry:     reg,
		systemPrompt: systemPrompt,
		toolDefs:     defs,
		trace:        trace,
		memStore:     memStore,
		compressor:   compressor,
		retryTracker:  newRetryTracker(defaultMaxRetries),
		enableNudge:  true, // 默认启用 Nudge
	}
}

// feishuToolSchemas 返回飞书工具的 schema
func feishuToolSchemas() map[string]struct {
	Desc   string
	Schema json.RawMessage
} {
	return map[string]struct {
		Desc   string
		Schema json.RawMessage
	}{
		"feishu_read_doc": {
			Desc: "读取飞书文档的纯文本内容。传入文档ID或文档URL",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"document_id": {
						"type": "string",
						"description": "飞书文档的 document_id 或完整 URL"
					}
				},
				"required": ["document_id"]
			}`),
		},
		"feishu_create_doc": {
			Desc: "创建一个新的飞书文档，返回文档ID和访问链接",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {
						"type": "string",
						"description": "文档标题"
					},
					"folder_token": {
						"type": "string",
						"description": "可选，目标文件夹 token"
					}
				},
				"required": ["title"]
			}`),
		},
	}
}

// Chat 执行一次对话（可能包含多轮工具调用）
// 传入 history 实现多轮对话
func (a *LoopAgent) Chat(ctx context.Context, userMessage string, history []llm.Message) (string, []llm.Message, error) {
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


	// Guardrail: 输入安全检查
	if a.guardrails != nil {
		gr := a.guardrails.Run(ctx, gd.PhaseInput, userMessage)
		switch gr.Action {
		case gd.ActionBlock:
			a.traceLog("guardrail_block", map[string]any{"guard": gr.GuardName, "phase": "input", "reason": gr.BlockReason})
			return gr.BlockReason, history, nil
		case gd.ActionRedact:
			a.traceLog("guardrail_redact", map[string]any{"guard": gr.GuardName, "phase": "input", "violations": len(gr.Violations)})
			history[len(history)-1] = llm.Message{Role: "user", Content: gr.RedactedContent}
		}
	}

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

	// 短期记忆压缩：历史过长时压缩旧消息
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

	// 上下文窗口管理已移入循环内（每次 LLM 调用前 PreCheck + 工具调用后 PostCheck）

	// Proactive Search: 在 LLM 回复前检测是否需要主动搜索
	// 防止 LLM "先否认再纠正" 的问题（如用户问 MacBook NEO，LLM 先说不存在再搜索纠正）
	proactiveResult := detectProactiveSearch(userMessage, a.toolDefs)
	if proactiveResult.ShouldSearch {
		a.traceLog("proactive_search", map[string]any{
			"entity": proactiveResult.Entity,
			"reason": proactiveResult.Reason,
		})
		proactiveMsg := buildProactiveSearchMessage(proactiveResult.Entity)
		history = append(history, proactiveMsg)
	}

	for i := 0; i < maxIterations; i++ {
		// LLM 调用前预检：确保 history 在窗口内（用户消息可能很长）
		history = a.fitContextWindowWithCtx(ctx, history)

		// Phase 1: Nudge 注入 — 上下文使用率过高时提醒 LLM 注意效率
		history = a.injectNudgeIfNeeded(history)

		a.traceLog("iteration", map[string]any{"i": i, "messages": len(history)})

		// 预算检查（调用前）
		if a.usageTracker != nil {
			if err := a.usageTracker.CheckBudget(); err != nil {
				a.traceLog("budget_exceeded", map[string]any{"total": a.usageTracker.TotalTokens(), "budget": a.usageTracker.Budget()})
				return "", history, fmt.Errorf("token 预算已耗尽 (已用 %d / 预算 %d)，请开始新会话或增加预算",
					a.usageTracker.TotalTokens(), a.usageTracker.Budget())
			}
		}

		resp, err := a.llmClient.Chat(ctx, history, a.toolDefs)
		if err != nil {
			return "", history, fmt.Errorf("LLM call failed: %w", err)
		}

		// Phase 1: 更新 cache 时间戳（每次收到 LLM 回复后）
		if a.ctxManager != nil {
			a.ctxManager.UpdateLastAssistantTime(time.Now())
		}

		// 记录 token 用量
		if a.usageTracker != nil && resp.Usage.TotalTokens > 0 {
			a.usageTracker.Record(resp.Usage)
			a.traceLog("token_usage", map[string]any{
				"prompt":     resp.Usage.PromptTokens,
				"completion": resp.Usage.CompletionTokens,
				"total":      resp.Usage.TotalTokens,
				"cumulative": a.usageTracker.TotalTokens(),
			})
		}

		// 追加 assistant 消息到历史
		history = append(history, resp.Message)

		// 如果没有工具调用，返回最终文本
		if len(resp.Message.ToolCalls) == 0 {
			finalContent := resp.Message.Content

			// 知识盲区检测：如果 LLM 用"训练截止"否定事实，且有搜索工具，自动触发搜索
			if i == 0 && hasWebSearchTool(a.toolDefs) {
				if gap, pattern := detectKnowledgeGap(finalContent); gap {
					a.traceLog("knowledge_gap_detected", map[string]any{"pattern": pattern, "retry": true})
					// 移除这条否定性回复，注入搜索提醒，让 LLM 重新回答
					history = history[:len(history)-1] // 移除 assistant 否定回复
					nudge := buildSearchNudge(userMessage)
					history = append(history, nudge)
					continue // 回到循环顶部，LLM 会看到搜索提醒
				}
			}

			// 推理一致性检查：如果推理过程和结论矛盾，强制 LLM 重新给出一致结论
			if i == 0 {
				rCheck := detectReasoningContradiction(finalContent)
				if rCheck.HasContradiction {
					a.traceLog("reasoning_contradiction", map[string]any{
						"reasoning_claim":  rCheck.ReasoningClaim,
						"conclusion_claim": rCheck.ConclusionClaim,
					})
					history = history[:len(history)-1] // 移除矛盾回复
					nudge := buildReasoningFixNudge(userMessage, rCheck)
					history = append(history, nudge)
					continue
				}
			}

			// Fabrication Guard: 统一检测数值编造 + URL 编造 + 引用编造
			fabCheck := DetectFabrication(userMessage, finalContent, history, a.toolDefs)
			if fabCheck.HasFabrication() {
				a.traceLog("fabrication_detected", map[string]any{
					"numeric_risk":    fabCheck.NumericRisk,
					"fabricated_urls": fabCheck.FabricatedURLs,
					"unverified_quotes": fabCheck.SuspiciousQuotes,
					"unverified_books":  fabCheck.SuspiciousBooks,
				})
				// 数值编造需要打回重新生成（带 calc 工具）
				if i == 0 && fabCheck.NeedsRegeneration() {
					history = history[:len(history)-1]
					nudge := buildCalcNudge(userMessage)
					history = append(history, nudge)
					continue
				}
				// URL/引用编造直接修正内容
				if fabCheck.NeedsContentFix() {
					finalContent = FixFabricatedContent(finalContent, fabCheck)
					history[len(history)-1] = llm.Message{Role: "assistant", Content: finalContent}
				}
			}

			// Verification Agent: 独立对抗性验证
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
							history[len(history)-1] = llm.Message{Role: "assistant", Content: finalContent}
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
					return "抱歉，生成的内容未通过安全检查，请换个方式提问", history, nil
				case gd.ActionRedact:
					a.traceLog("guardrail_redact", map[string]any{"guard": gr.GuardName, "phase": "output", "violations": len(gr.Violations)})
					finalContent = gr.RedactedContent
					history[len(history)-1] = llm.Message{Role: "assistant", Content: finalContent}
				}
			}
			// 清除 Nudge 消息（不持久化到会话历史中）
			history = a.removeNudgeMessages(history)
			a.traceLog("final_answer", map[string]any{"content_len": len(finalContent)})
			return finalContent, history, nil
		}

		// 并发执行所有工具调用
		history = a.executeToolCallsParallel(ctx, resp.Message.ToolCalls, history)

		// 每轮工具调用后检查上下文窗口（工具结果可能很大）
		history = a.fitContextWindowWithCtx(ctx, history)
	}

	return "", history, fmt.Errorf("reached maximum iterations (%d)", maxIterations)
}

// ---- Phase 1: Nudge 注入相关方法 ----

// nudgeMarker 用于标识 Nudge 消息，便于后续清理
const nudgeMarker = "[CONTEXT EFFICIENCY]"
const nudgeCriticalMarker = "[CONTEXT CRITICAL]"

// injectNudgeIfNeeded 在 LLM 调用前检查上下文使用率，按需注入 Nudge 提醒
// Nudge 消息作为临时 system 消息注入到 history 末尾（最新 user 消息之后）
// 返回可能被修改的 history
func (a *LoopAgent) injectNudgeIfNeeded(history []llm.Message) []llm.Message {
	if !a.enableNudge {
		return history
	}

	// 需要上下文管理器来获取使用状态
	if a.ctxManager == nil {
		return history
	}

	// 先清除旧的 Nudge 消息，避免重复
	history = a.removeNudgeMessages(history)

	status := a.ctxManager.Status(history)
	nudgeMsg := ctxwindow.NudgeMessage(status)
	if nudgeMsg == "" {
		return history
	}

	a.traceLog("nudge_inject", map[string]any{
		"usage_percent": fmt.Sprintf("%.1f%%", status.UsagePercent*100),
		"remaining":     status.RemainingTokens,
		"cache_temp":    status.CacheTemp,
	})

	// 将 Nudge 消息追加到末尾
	history = append(history, llm.Message{
		Role:    "system",
		Content: nudgeMsg,
	})

	return history
}

// removeNudgeMessages 从 history 中移除所有 Nudge 消息
// Nudge 是临时性的效率提醒，不应该持久化到会话存储中
func (a *LoopAgent) removeNudgeMessages(history []llm.Message) []llm.Message {
	result := make([]llm.Message, 0, len(history))
	for _, msg := range history {
		if msg.Role == "system" && isNudgeMessage(msg.Content) {
			continue
		}
		result = append(result, msg)
	}
	return result
}

// isNudgeMessage 判断消息内容是否为 Nudge 提醒
func isNudgeMessage(content string) bool {
	if len(content) < len(nudgeMarker) {
		return false
	}
	return content[:len(nudgeMarker)] == nudgeMarker ||
		(len(content) >= len(nudgeCriticalMarker) && content[:len(nudgeCriticalMarker)] == nudgeCriticalMarker)
}

// fitContextWindow 使用上下文窗口管理器裁剪历史（向后兼容）
func (a *LoopAgent) fitContextWindow(history []llm.Message) []llm.Message {
	return a.fitContextWindowWithCtx(context.Background(), history)
}

// fitContextWindowWithCtx 带 context 的裁剪
// 优先使用 SmartManager（支持摘要压缩），降级到基础 Manager
func (a *LoopAgent) fitContextWindowWithCtx(ctx context.Context, history []llm.Message) []llm.Message {
	// 优先使用 SmartManager
	if a.smartCtxManager != nil {
		before := len(history)
		result := a.smartCtxManager.SmartFit(ctx, history)

		if result.FinalCount < before {
			a.traceLog("smart_context_fit", map[string]any{
				"messages_before":  before,
				"messages_after":   result.FinalCount,
				"tokens_before":    result.TokensBefore,
				"tokens_after":     result.TokensAfter,
				"strategy":         result.Strategy,
				"summary_inserted": result.SummaryInserted,
			})
		}
		return result.Messages
	}

	// 降级到基础 Manager
	if a.ctxManager == nil {
		return history
	}

	before := len(history)
	beforeTokens := a.ctxManager.EstimateHistory(history)
	result := a.ctxManager.Fit(history)

	if len(result) < before {
		afterTokens := a.ctxManager.EstimateHistory(result)
		a.traceLog("context_window_fit", map[string]any{
			"messages_before": before,
			"messages_after":  len(result),
			"tokens_before":   beforeTokens,
			"tokens_after":    afterTokens,
			"tokens_saved":    beforeTokens - afterTokens,
		})
	}

	return result
}

// executeTool 已被 executeOneToolSafe (parallel.go) 取代

func (a *LoopAgent) traceLog(event string, data map[string]any) {
	if a.trace == io.Discard {
		return
	}
	entry := map[string]any{
		"at":    time.Now().Format(time.RFC3339),
		"event": event,
	}
	for k, v := range data {
		entry[k] = v
	}
	b, _ := json.Marshal(entry)
	_, _ = fmt.Fprintf(a.trace, "%s\n", b)
}

// SetVerifier 设置验证子 Agent（Verification Agent）
func (a *LoopAgent) SetVerifier(v *Verifier) {
	a.verifier = v
}

// GetVerifier 返回验证子 Agent
func (a *LoopAgent) GetVerifier() *Verifier {
	return a.verifier
}

// SetGuardrails 设置安全检查管道
func (a *LoopAgent) SetGuardrails(p gd.Pipeline) {
	a.guardrails = p
}

// GetClient 返回内部的 LLM 客户端（用于类型断言检查能力）
func (a *LoopAgent) GetClient() llm.Client {
	return a.llmClient
}

// GetUsageTracker 返回 token 用量追踪器
func (a *LoopAgent) GetUsageTracker() *llm.UsageTracker {
	return a.usageTracker
}

// SetUsageTracker 设置 token 用量追踪器
func (a *LoopAgent) SetUsageTracker(ut *llm.UsageTracker) {
	a.usageTracker = ut
}

// SetContextManager 设置上下文窗口管理器（基础版）
func (a *LoopAgent) SetContextManager(mgr *ctxwindow.Manager) {
	a.ctxManager = mgr
}

// SetSmartContextManager 设置增强版上下文窗口管理器（支持摘要压缩）
// 设置后优先使用 SmartManager，忽略基础 Manager
func (a *LoopAgent) SetSmartContextManager(mgr *ctxwindow.SmartManager) {
	a.smartCtxManager = mgr
}

// GetSmartContextManager 获取增强版上下文窗口管理器
func (a *LoopAgent) GetSmartContextManager() *ctxwindow.SmartManager {
	return a.smartCtxManager
}

// GetContextManager 获取上下文窗口管理器
func (a *LoopAgent) GetContextManager() *ctxwindow.Manager {
	return a.ctxManager
}

// SetNudgeEnabled 启用或禁用 Nudge 上下文效率提醒
func (a *LoopAgent) SetNudgeEnabled(enabled bool) {
	a.enableNudge = enabled
}

// ContextWindowStatus 返回当前上下文窗口状态（无管理器返回零值）
func (a *LoopAgent) ContextWindowStatus(history []llm.Message) *ctxwindow.WindowStatus {
	if a.ctxManager == nil {
		return nil
	}
	s := a.ctxManager.Status(history)
	return &s
}

// InjectToolDefs 注入额外的工具定义（用于 Multi-Agent handoff 等场景）
// 这些工具已在 registry 中注册，但 schema 不在 BuiltinSchemas 中，
// 需要外部手动提供 ToolDef。
func (a *LoopAgent) InjectToolDefs(defs []llm.ToolDef) {
	a.toolDefs = append(a.toolDefs, defs...)
}
