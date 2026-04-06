package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/ctxwindow"
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

	return &LoopAgent{
		llmClient:    client,
		registry:     reg,
		systemPrompt: systemPrompt,
		toolDefs:     defs,
		trace:        trace,
		memStore:     memStore,
		compressor:   compressor,
		retryTracker:  newRetryTracker(defaultMaxRetries),
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

	for i := 0; i < maxIterations; i++ {
		// LLM 调用前预检：确保 history 在窗口内（用户消息可能很长）
		history = a.fitContextWindowWithCtx(ctx, history)

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
			a.traceLog("final_answer", map[string]any{"content_len": len(resp.Message.Content)})
			return resp.Message.Content, history, nil
		}

		// 并发执行所有工具调用
		history = a.executeToolCallsParallel(ctx, resp.Message.ToolCalls, history)

		// 每轮工具调用后检查上下文窗口（工具结果可能很大）
		history = a.fitContextWindowWithCtx(ctx, history)
	}

	return "", history, fmt.Errorf("reached maximum iterations (%d)", maxIterations)
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
