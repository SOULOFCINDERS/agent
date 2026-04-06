package ctxwindow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ---------- 摘要压缩式裁剪 ----------

// Summarizer 摘要生成接口
// 由外部实现（如 memory.Compressor），解耦领域依赖
type Summarizer interface {
	// Summarize 将一组消息压缩为一段摘要文本
	Summarize(ctx context.Context, messages []llm.Message) (string, error)
}

// CompactionConfig 摘要式裁剪配置
type CompactionConfig struct {
	// EnableSummary 启用摘要压缩（否则直接丢弃）
	EnableSummary bool

	// SummaryMaxTokens 摘要的最大 token 预算
	// 超过此值的摘要会被截断
	SummaryMaxTokens int

	// MinMessagesToSummarize 触发摘要的最少消息数
	// 少于此数量的消息直接丢弃（不值得调用 LLM 生成摘要）
	MinMessagesToSummarize int

	// SummaryFallback 摘要失败时是否降级为直接丢弃
	SummaryFallback bool
}

// DefaultCompactionConfig 默认摘要压缩配置
func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		EnableSummary:          true,
		SummaryMaxTokens:       400,
		MinMessagesToSummarize: 4,
		SummaryFallback:        true,
	}
}

// CompactionResult 压缩裁剪结果
type CompactionResult struct {
	Messages        []llm.Message // 裁剪后的消息
	OriginalCount   int           // 原始消息数
	FinalCount      int           // 裁剪后消息数
	TokensBefore    int           // 裁剪前 token 数
	TokensAfter     int           // 裁剪后 token 数
	SummaryInserted bool          // 是否插入了摘要
	Strategy        string        // 使用的策略："summary" | "truncate" | "none"
}

// SmartManager 增强版上下文窗口管理器
// 在 Manager 的基础上增加：
//  1. 摘要压缩：裁剪前先生成摘要，减少信息丢失
//  2. 预检机制：LLM 调用前主动检查并裁剪
//  3. 增量估算缓存：避免重复遍历计算
type SmartManager struct {
	*Manager                      // 继承基础管理器
	summarizer    Summarizer      // 摘要生成器（可选）
	compaction    CompactionConfig // 摘要压缩配置
	trace         io.Writer        // trace 日志
}

// SmartManagerConfig SmartManager 配置
type SmartManagerConfig struct {
	// Base 基础管理器配置
	Base ManagerConfig

	// Compaction 摘要压缩配置
	Compaction CompactionConfig

	// Summarizer 摘要生成器（为 nil 则不启用摘要）
	Summarizer Summarizer

	// Trace 日志输出
	Trace io.Writer
}

// NewSmartManager 创建增强版上下文窗口管理器
func NewSmartManager(cfg SmartManagerConfig) *SmartManager {
	if cfg.Trace == nil {
		cfg.Trace = io.Discard
	}
	base := NewManager(cfg.Base)

	return &SmartManager{
		Manager:    base,
		summarizer: cfg.Summarizer,
		compaction: cfg.Compaction,
		trace:      cfg.Trace,
	}
}

// SmartFit 智能裁剪：先尝试摘要压缩，再降级为硬裁剪
//
// 流程：
//  1. 检查是否需要裁剪（不需要则直接返回）
//  2. Phase 0: 截断超长工具结果（快速释放空间）
//  3. Phase 1: 摘要压缩（如果启用且有 Summarizer）
//     - 识别可压缩区间（保护区之外的旧消息）
//     - 调用 Summarizer 生成摘要
//     - 用摘要替换原始消息
//  4. Phase 2: 如果摘要后仍超标，降级为硬裁剪（Manager.Fit）
func (sm *SmartManager) SmartFit(ctx context.Context, history []llm.Message) *CompactionResult {
	result := &CompactionResult{
		OriginalCount: len(history),
		TokensBefore:  sm.EstimateHistory(history),
		Strategy:      "none",
	}

	// 不需要裁剪
	if !sm.NeedsTruncation(history) {
		result.Messages = history
		result.FinalCount = len(history)
		result.TokensAfter = result.TokensBefore
		return result
	}

	sm.traceLog("smart_fit_start", map[string]any{
		"messages":    len(history),
		"tokens":      result.TokensBefore,
		"budget":      sm.config.MaxInputTokens,
		"overflow":    result.TokensBefore - sm.config.MaxInputTokens,
	})

	// 复制，不修改原始数据
	working := make([]llm.Message, len(history))
	copy(working, history)

	// Phase 0: 截断超长工具结果
	working = sm.truncateLongToolResults(working)
	if !sm.needsTruncationForSlice(working) {
		result.Messages = working
		result.FinalCount = len(working)
		result.TokensAfter = sm.EstimateHistory(working)
		result.Strategy = "truncate"
		sm.traceLog("smart_fit_done_phase0", map[string]any{
			"tokens_after": result.TokensAfter,
		})
		return result
	}

	// Phase 1: 摘要压缩
	if sm.summarizer != nil && sm.compaction.EnableSummary {
		summarized, ok := sm.trySummarize(ctx, working)
		if ok {
			working = summarized
			result.SummaryInserted = true
			result.Strategy = "summary"

			if !sm.needsTruncationForSlice(working) {
				result.Messages = working
				result.FinalCount = len(working)
				result.TokensAfter = sm.EstimateHistory(working)
				sm.traceLog("smart_fit_done_phase1", map[string]any{
					"messages_after": len(working),
					"tokens_after":   result.TokensAfter,
				})
				return result
			}
		}
	}

	// Phase 2: 降级为硬裁剪
	working = sm.Fit(working)
	if result.Strategy == "summary" {
		result.Strategy = "summary+truncate"
	} else {
		result.Strategy = "truncate"
	}

	result.Messages = working
	result.FinalCount = len(working)
	result.TokensAfter = sm.EstimateHistory(working)

	sm.traceLog("smart_fit_done_phase2", map[string]any{
		"messages_after": len(working),
		"tokens_after":   result.TokensAfter,
		"strategy":       result.Strategy,
	})

	return result
}

// PreCheck LLM 调用前的预检裁剪
// 与 SmartFit 相同逻辑，但语义上明确是"调用前检查"
// 返回裁剪后的 history，可直接传给 LLM
func (sm *SmartManager) PreCheck(ctx context.Context, history []llm.Message) []llm.Message {
	r := sm.SmartFit(ctx, history)
	return r.Messages
}

// PostToolCheck 工具调用后的检查裁剪
// 工具结果可能很大，需要立即检查
func (sm *SmartManager) PostToolCheck(ctx context.Context, history []llm.Message) []llm.Message {
	r := sm.SmartFit(ctx, history)
	return r.Messages
}

// needsTruncationForSlice 内部方法，避免和 Manager.NeedsTruncation 混淆
func (sm *SmartManager) needsTruncationForSlice(msgs []llm.Message) bool {
	return sm.EstimateHistory(msgs) > sm.config.MaxInputTokens
}

// trySummarize 尝试对旧消息生成摘要并替换
func (sm *SmartManager) trySummarize(ctx context.Context, msgs []llm.Message) ([]llm.Message, bool) {
	// 分离 system 消息和对话消息
	var systemMsgs []llm.Message
	var convMsgs []llm.Message
	for _, m := range msgs {
		if m.Role == "system" {
			systemMsgs = append(systemMsgs, m)
		} else {
			convMsgs = append(convMsgs, m)
		}
	}

	// 确定保护范围：最近 N 轮
	protectStart := findProtectStart(convMsgs, sm.config.ProtectRecentRounds)
	if protectStart <= 0 {
		// 没有可压缩的旧消息
		return msgs, false
	}

	// 可压缩区间
	toSummarize := convMsgs[:protectStart]
	protected := convMsgs[protectStart:]

	// 消息太少不值得摘要
	if len(toSummarize) < sm.compaction.MinMessagesToSummarize {
		sm.traceLog("summary_skip", map[string]any{
			"reason":    "too_few_messages",
			"count":     len(toSummarize),
			"threshold": sm.compaction.MinMessagesToSummarize,
		})
		return msgs, false
	}

	sm.traceLog("summary_start", map[string]any{
		"messages_to_summarize": len(toSummarize),
		"messages_protected":    len(protected),
	})

	// 调用 Summarizer
	summary, err := sm.summarizer.Summarize(ctx, toSummarize)
	if err != nil {
		sm.traceLog("summary_error", map[string]any{"error": err.Error()})
		if sm.compaction.SummaryFallback {
			// 降级：生成简单的要点摘要
			summary = fallbackSummary(toSummarize)
		} else {
			return msgs, false
		}
	}

	// 截断过长的摘要
	if sm.compaction.SummaryMaxTokens > 0 {
		estimator := DefaultEstimator()
		summaryTokens := estimator.EstimateText(summary)
		if summaryTokens > sm.compaction.SummaryMaxTokens {
			ratio := float64(sm.compaction.SummaryMaxTokens) / float64(summaryTokens)
			maxChars := int(float64(len([]rune(summary))) * ratio)
			runes := []rune(summary)
			if maxChars < len(runes) {
				summary = string(runes[:maxChars]) + "..."
			}
		}
	}

	sm.traceLog("summary_done", map[string]any{
		"summary_len":    len(summary),
		"replaced_count": len(toSummarize),
	})

	// 组装新消息列表
	var result []llm.Message

	// 1. system 消息
	result = append(result, systemMsgs...)

	// 2. 摘要消息
	if summary != "" {
		result = append(result, llm.Message{
			Role:    "system",
			Content: "[Earlier conversation summary]\n" + summary + "\n[Recent conversation follows]",
		})
	}

	// 3. 保护区内的消息
	result = append(result, protected...)

	return result, true
}

// fallbackSummary 降级摘要：提取 user 消息的关键内容
func fallbackSummary(msgs []llm.Message) string {
	var points []string
	for _, m := range msgs {
		switch m.Role {
		case "user":
			text := m.Content
			runes := []rune(text)
			if len(runes) > 80 {
				text = string(runes[:80]) + "..."
			}
			points = append(points, "- User: "+text)
		case "assistant":
			if m.Content != "" {
				text := m.Content
				runes := []rune(text)
				if len(runes) > 80 {
					text = string(runes[:80]) + "..."
				}
				points = append(points, "- Assistant: "+text)
			}
			if len(m.ToolCalls) > 0 {
				names := make([]string, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					names[i] = tc.Function.Name
				}
				points = append(points, "- Tools used: "+strings.Join(names, ", "))
			}
		}
	}
	if len(points) > 8 {
		points = points[:8]
	}
	return strings.Join(points, "\n")
}

func (sm *SmartManager) traceLog(event string, data map[string]any) {
	if sm.trace == io.Discard {
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
	fmt.Fprintf(sm.trace, "%s\n", b)
}

// ---------- LLM-based Summarizer 实现 ----------

// LLMSummarizer 使用 LLM 生成摘要的 Summarizer 实现
type LLMSummarizer struct {
	client llm.Client
}

// NewLLMSummarizer 创建基于 LLM 的摘要生成器
func NewLLMSummarizer(client llm.Client) *LLMSummarizer {
	return &LLMSummarizer{client: client}
}

// Summarize 使用 LLM 将对话历史压缩为摘要
func (s *LLMSummarizer) Summarize(ctx context.Context, messages []llm.Message) (string, error) {
	var conv strings.Builder
	for _, m := range messages {
		switch m.Role {
		case "user":
			text := m.Content
			runes := []rune(text)
			if len(runes) > 500 {
				text = string(runes[:500]) + "..."
			}
			conv.WriteString(fmt.Sprintf("User: %s\n", text))
		case "assistant":
			if m.Content != "" {
				text := m.Content
				runes := []rune(text)
				if len(runes) > 500 {
					text = string(runes[:500]) + "..."
				}
				conv.WriteString(fmt.Sprintf("Assistant: %s\n", text))
			}
			for _, tc := range m.ToolCalls {
				args := tc.Function.Arguments
				runes := []rune(args)
				if len(runes) > 200 {
					args = string(runes[:200]) + "..."
				}
				conv.WriteString(fmt.Sprintf("Assistant called tool: %s(%s)\n", tc.Function.Name, args))
			}
		case "tool":
			text := m.Content
			runes := []rune(text)
			if len(runes) > 300 {
				text = string(runes[:300]) + "..."
			}
			conv.WriteString(fmt.Sprintf("Tool result: %s\n", text))
		}
	}

	prompt := []llm.Message{
		{
			Role: "system",
			Content: "You are a conversation summarizer. Compress the following conversation history into a concise summary. " +
				"Preserve:\n" +
				"1. User's main intentions and requests\n" +
				"2. Key actions taken and results obtained\n" +
				"3. Important context needed for continuing the conversation\n" +
				"4. Any unresolved issues or follow-up items\n\n" +
				"Requirements: 3-6 sentences, under 200 words. Use the same language as the conversation.",
		},
		{
			Role:    "user",
			Content: "Summarize this conversation:\n\n" + conv.String(),
		},
	}

	resp, err := s.client.Chat(ctx, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("LLM summarize: %w", err)
	}

	return resp.Message.Content, nil
}
