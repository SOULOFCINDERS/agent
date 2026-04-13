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
// Chat 执行一次对话（可能包含多轮工具调用）
// 传入 history 实现多轮对话
func (a *LoopAgent) Chat(ctx context.Context, userMessage string, history []llm.Message) (string, []llm.Message, error) {
	prep := a.prepareHistory(ctx, userMessage, history)
	history = prep.history
	if prep.blocked != "" {
		return prep.blocked, history, nil
	}

	for i := 0; i < maxIterations; i++ {
		// LLM 调用前预检
		history = a.fitContextWindowWithCtx(ctx, history)
		history = a.injectNudgeIfNeeded(history)

		a.traceLog("iteration", map[string]any{"i": i, "messages": len(history)})

		// 预算检查
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

		// Phase 1: 更新 cache 时间戳
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

		history = append(history, resp.Message)

		// 如果没有工具调用，执行后置检查并返回
		if len(resp.Message.ToolCalls) == 0 {
			pc := a.postCheckResponse(ctx, userMessage, resp.Message.Content, history, i)
			if pc.retry {
				history = history[:len(history)-1] // 移除 assistant 回复
				history = append(history, pc.retryMsgs...)
				continue
			}
			// 更新 history 中的最终内容（可能被 guardrail/fabrication 修正）
			history[len(history)-1] = llm.Message{Role: "assistant", Content: pc.content}
			history = a.removeNudgeMessages(history)
			a.traceLog("final_answer", map[string]any{"content_len": len(pc.content)})
			return pc.content, history, nil
		}

		// 并发执行所有工具调用
		history = a.executeToolCallsParallel(ctx, resp.Message.ToolCalls, history)
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

// CompactHistory 主动压缩对话历史
// 供 CLI/Web 层在用户主动触发时调用。
// 优先使用 SmartManager.ForceCompact（含摘要），降级到 Manager.Fit（硬裁剪）。
func (a *LoopAgent) CompactHistory(ctx context.Context, history []llm.Message) ([]llm.Message, *ctxwindow.CompactionResult, error) {
	if a.smartCtxManager != nil {
		result := a.smartCtxManager.ForceCompact(ctx, history)
		a.traceLog("compact_history", map[string]any{
			"messages_before":  result.OriginalCount,
			"messages_after":   result.FinalCount,
			"tokens_before":    result.TokensBefore,
			"tokens_after":     result.TokensAfter,
			"strategy":         result.Strategy,
			"summary_inserted": result.SummaryInserted,
		})
		return result.Messages, result, nil
	}

	if a.ctxManager != nil {
		before := len(history)
		tokensBefore := a.ctxManager.EstimateHistory(history)
		fitted := a.ctxManager.Fit(history)
		tokensAfter := a.ctxManager.EstimateHistory(fitted)
		result := &ctxwindow.CompactionResult{
			Messages:      fitted,
			OriginalCount: before,
			FinalCount:    len(fitted),
			TokensBefore:  tokensBefore,
			TokensAfter:   tokensAfter,
			Strategy:      "truncate",
		}
		if before == len(fitted) {
			result.Strategy = "none"
		}
		return fitted, result, nil
	}

	return nil, nil, fmt.Errorf("no context manager available for compaction")
}

// InjectToolDefs 注入额外的工具定义（用于 Multi-Agent handoff 等场景）
// 这些工具已在 registry 中注册，但 schema 不在 BuiltinSchemas 中，
// 需要外部手动提供 ToolDef。
func (a *LoopAgent) InjectToolDefs(defs []llm.ToolDef) {
	a.toolDefs = append(a.toolDefs, defs...)
}
