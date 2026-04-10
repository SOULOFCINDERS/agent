// Package memory implements conversation memory storage with similarity search and context compression.
package memory

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ================================================================
// 常量 & 默认值
// ================================================================

const (
	// summaryMarkerPrefix 标识一条 system 消息是压缩摘要
	summaryMarkerPrefix = "[对话历史摘要]"
	summaryMarkerSuffix = "[以下是最近的对话]"

	// token 估算参数（中文 ≈ 1.5 token/字, 英文 ≈ 1.3 token/word）
	defaultCharsPerToken = 2 // 偏保守：2 个字符 ≈ 1 token

	// 默认值
	defaultWindowSize  = 3
	defaultMaxMessages = 12
	defaultMaxTokens   = 0 // 0 表示不启用 token-based 触发
	defaultTargetTokens = 0
)

// ================================================================
// Compressor 核心结构
// ================================================================

// Compressor 负责短期记忆（会话历史）的压缩
//
// 支持两种触发模式（可同时开启，任一触发即压缩）：
//  1. 消息数触发（原有逻辑）：history 消息总数 > MaxMessages
//  2. Token 触发（新增）：history 估算 token 数 > MaxTokens
//
// 支持增量压缩：
//   - 如果 history 中已有上一轮的 [对话历史摘要]，本轮只对 "上次摘要之后 ~ 窗口起点之前"
//     的新消息做增量摘要，并将旧摘要和新摘要合并。
//   - 避免重复压缩已经摘要过的内容，减少 LLM 调用量，提高摘要稳定性。
type Compressor struct {
	llmClient    llm.Client
	windowSize   int // 保留最近几轮完整消息（一轮 = user 消息开始到下一个 user 消息之前）
	maxMessages  int // 触发压缩的消息数阈值（0 = 禁用消息数触发）
	maxTokens    int // 触发压缩的 token 估算阈值（0 = 禁用 token 触发）
	targetTokens int // 压缩后的目标 token 数（用于动态调整 windowSize）
}

// CompressorConfig 压缩器配置
type CompressorConfig struct {
	// WindowSize 保留最近几轮的完整消息，默认 3
	WindowSize int

	// MaxMessages 当 history 消息总数超过此值时触发压缩，默认 12。设为 0 禁用。
	MaxMessages int

	// MaxTokens 当 history 估算 token 数超过此值时触发压缩。
	// 默认 0（禁用），建议值: 模型上下文窗口的 60%，如 8192 窗口设为 5000。
	MaxTokens int

	// TargetTokens 压缩后的目标 token 数。
	// 设置后会动态调整保留窗口大小，使压缩后总 token 接近此值。
	// 默认 0（不启用动态窗口，固定使用 WindowSize）。
	TargetTokens int
}

// NewCompressor 创建历史压缩器
func NewCompressor(client llm.Client, cfg CompressorConfig) *Compressor {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = defaultWindowSize
	}
	if cfg.MaxMessages <= 0 && cfg.MaxTokens <= 0 {
		// 两个都没设，使用默认消息数触发
		cfg.MaxMessages = defaultMaxMessages
	}
	return &Compressor{
		llmClient:    client,
		windowSize:   cfg.WindowSize,
		maxMessages:  cfg.MaxMessages,
		maxTokens:    cfg.MaxTokens,
		targetTokens: cfg.TargetTokens,
	}
}

// ================================================================
// CompressResult
// ================================================================

// CompressResult 压缩结果
type CompressResult struct {
	Messages        []llm.Message // 压缩后的消息列表
	WasCompressed   bool          // 是否发生了压缩
	OriginalCount   int           // 原始消息数
	CompressedCount int           // 压缩后消息数
	SummaryText     string        // 生成的摘要（用于调试）
	Incremental     bool          // 是否为增量压缩（复用了旧摘要）
	EstimatedTokens int           // 压缩后估算 token 数
}

// ================================================================
// 核心方法：NeedCompress + Compress
// ================================================================

// NeedCompress 检查历史是否需要压缩（支持消息数 + token 双触发）
func (c *Compressor) NeedCompress(history []llm.Message) bool {
	// 条件 1: 消息数触发
	if c.maxMessages > 0 && len(history) > c.maxMessages {
		return true
	}
	// 条件 2: Token 估算触发
	if c.maxTokens > 0 && estimateTokens(history) > c.maxTokens {
		return true
	}
	return false
}

// Compress 压缩会话历史（支持增量压缩 + token-based 动态窗口）
//
// 流程:
//  1. 判断是否需要压缩
//  2. 分离 system 消息、已有摘要、对话消息
//  3. 计算保留窗口（固定 windowSize 或按 targetTokens 动态调整）
//  4. 对窗口外的新消息做 LLM 摘要（增量：合并旧摘要 + 新摘要）
//  5. 组装压缩后的 history
func (c *Compressor) Compress(ctx context.Context, history []llm.Message) (*CompressResult, error) {
	result := &CompressResult{
		Messages:      history,
		OriginalCount: len(history),
	}

	// 不需要压缩的情况
	if !c.NeedCompress(history) {
		result.CompressedCount = len(history)
		result.EstimatedTokens = estimateTokens(history)
		return result, nil
	}

	// Step 1: 分离消息类型
	var systemMsgs []llm.Message  // 原始 system prompt
	var existingSummary string     // 上一轮的摘要文本（如有）
	var convMsgs []llm.Message     // 纯对话消息（不含 system 和旧摘要）

	for _, m := range history {
		if m.Role == "system" {
			if isSummaryMessage(m) {
				existingSummary = extractSummaryText(m)
			} else {
				systemMsgs = append(systemMsgs, m)
			}
		} else {
			convMsgs = append(convMsgs, m)
		}
	}

	// Step 2: 计算保留窗口
	windowSize := c.windowSize
	if c.targetTokens > 0 {
		windowSize = c.dynamicWindowSize(systemMsgs, convMsgs)
	}

	windowStart := findWindowStart(convMsgs, windowSize)
	if windowStart <= 0 {
		result.CompressedCount = len(history)
		result.EstimatedTokens = estimateTokens(history)
		return result, nil
	}

	toCompress := convMsgs[:windowStart]
	windowMsgs := convMsgs[windowStart:]

	// Step 3: 生成摘要（增量 or 全量）
	var summary string
	var err error
	isIncremental := existingSummary != ""

	if isIncremental {
		// 增量压缩：基于旧摘要 + 新消息生成更新后的摘要
		summary, err = c.incrementalSummarize(ctx, existingSummary, toCompress)
	} else {
		// 全量压缩：对所有窗口外消息生成摘要
		summary, err = c.summarize(ctx, toCompress)
	}

	if err != nil {
		// 降级策略
		if isIncremental {
			// 增量失败：保留旧摘要 + 追加新消息的 fallback 摘要
			summary = existingSummary + "\n" + c.fallbackSummary(toCompress)
		} else {
			summary = c.fallbackSummary(toCompress)
		}
	}

	result.SummaryText = summary
	result.WasCompressed = true
	result.Incremental = isIncremental

	// Step 4: 组装新的历史
	var newHistory []llm.Message

	// 4a. 原始 system 消息
	newHistory = append(newHistory, systemMsgs...)

	// 4b. 摘要作为一条 system 消息插入
	if summary != "" {
		newHistory = append(newHistory, llm.Message{
			Role:    "system",
			Content: summaryMarkerPrefix + "\n" + summary + "\n" + summaryMarkerSuffix,
		})
	}

	// 4c. 窗口内的完整消息
	newHistory = append(newHistory, windowMsgs...)

	result.Messages = newHistory
	result.CompressedCount = len(newHistory)
	result.EstimatedTokens = estimateTokens(newHistory)

	return result, nil
}

// ================================================================
// 增量摘要
// ================================================================

// incrementalSummarize 基于已有摘要和新消息，生成更新后的摘要
func (c *Compressor) incrementalSummarize(ctx context.Context, existingSummary string, newMsgs []llm.Message) (string, error) {
	// 如果没有新消息要压缩，直接返回旧摘要
	if len(newMsgs) == 0 {
		return existingSummary, nil
	}

	var newConv strings.Builder
	for _, m := range newMsgs {
		switch m.Role {
		case "user":
			newConv.WriteString(fmt.Sprintf("用户: %s\n", truncateStr(m.Content, 500)))
		case "assistant":
			if m.Content != "" {
				newConv.WriteString(fmt.Sprintf("助手: %s\n", truncateStr(m.Content, 500)))
			}
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					newConv.WriteString(fmt.Sprintf("助手调用工具: %s(%s)\n",
						tc.Function.Name, truncateStr(tc.Function.Arguments, 200)))
				}
			}
		case "tool":
			newConv.WriteString(fmt.Sprintf("工具结果: %s\n", truncateStr(m.Content, 300)))
		}
	}

	summaryPrompt := []llm.Message{
		{
			Role: "system",
			Content: "你是一个对话摘要助手。你需要将已有的对话摘要和新产生的对话合并，生成一份更新后的完整摘要。" +
				"\n\n要求：" +
				"\n1. 保留已有摘要中仍然相关的关键信息" +
				"\n2. 整合新对话中的重要内容" +
				"\n3. 如果新对话修正或更新了旧摘要中的信息，以新的为准" +
				"\n4. 用中文，5-8句话概括，不超过300字" +
				"\n5. 重点保留：用户意图、已完成操作、关键结果、待跟进事项",
		},
		{
			Role: "user",
			Content: fmt.Sprintf("已有摘要：\n%s\n\n新增对话：\n%s\n\n请生成更新后的完整摘要：",
				existingSummary, newConv.String()),
		},
	}

	resp, err := c.llmClient.Chat(ctx, summaryPrompt, nil)
	if err != nil {
		return "", fmt.Errorf("incremental summarize LLM call: %w", err)
	}

	return resp.Message.Content, nil
}

// ================================================================
// Token 估算 & 动态窗口
// ================================================================

// estimateTokens 估算一组消息的 token 数
// 采用保守策略：中英混合按 2 字符/token 估算
func estimateTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		// 消息本身的 content
		total += estimateStringTokens(m.Content)
		// 工具调用也占 token
		for _, tc := range m.ToolCalls {
			total += estimateStringTokens(tc.Function.Name)
			total += estimateStringTokens(tc.Function.Arguments)
		}
		// 每条消息有 ~4 token 的 role/格式 overhead
		total += 4
	}
	return total
}

// estimateStringTokens 估算单个字符串的 token 数
func estimateStringTokens(s string) int {
	if s == "" {
		return 0
	}
	charCount := utf8.RuneCountInString(s)
	tokens := charCount / defaultCharsPerToken
	if tokens == 0 && charCount > 0 {
		tokens = 1
	}
	return tokens
}

// dynamicWindowSize 根据 targetTokens 动态计算应保留的窗口轮数
// 思路：从 1 轮开始往上加，直到 system + 摘要估算 + 窗口消息 逼近 targetTokens
func (c *Compressor) dynamicWindowSize(systemMsgs []llm.Message, convMsgs []llm.Message) int {
	if c.targetTokens <= 0 {
		return c.windowSize
	}

	// system 消息和摘要的固定开销（摘要按 150 token 估算）
	fixedTokens := estimateTokens(systemMsgs) + 150

	remaining := c.targetTokens - fixedTokens
	if remaining <= 0 {
		return 1 // 至少保留 1 轮
	}

	// 从 windowSize 开始尝试，逐步缩小，找到最大的能放进 remaining 的窗口
	for ws := c.windowSize; ws >= 1; ws-- {
		start := findWindowStart(convMsgs, ws)
		windowMsgs := convMsgs[start:]
		windowTokens := estimateTokens(windowMsgs)
		if windowTokens <= remaining {
			return ws
		}
	}

	return 1 // 兜底：至少保留 1 轮
}

// ================================================================
// 全量摘要（原有逻辑，保持不变）
// ================================================================

// summarize 使用 LLM 生成对话历史摘要
func (c *Compressor) summarize(ctx context.Context, msgs []llm.Message) (string, error) {
	var conv strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			conv.WriteString(fmt.Sprintf("用户: %s\n", truncateStr(m.Content, 500)))
		case "assistant":
			if m.Content != "" {
				conv.WriteString(fmt.Sprintf("助手: %s\n", truncateStr(m.Content, 500)))
			}
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					conv.WriteString(fmt.Sprintf("助手调用工具: %s(%s)\n",
						tc.Function.Name, truncateStr(tc.Function.Arguments, 200)))
				}
			}
		case "tool":
			conv.WriteString(fmt.Sprintf("工具结果: %s\n", truncateStr(m.Content, 300)))
		}
	}

	summaryPrompt := []llm.Message{
		{
			Role: "system",
			Content: "你是一个对话摘要助手。请将以下对话历史压缩为简洁的摘要，保留关键信息：" +
				"\n1. 用户的主要意图和请求" +
				"\n2. 已完成的操作和得到的关键结果" +
				"\n3. 需要后续跟进的事项" +
				"\n\n要求：用中文，3-5句话概括，不超过200字。",
		},
		{
			Role:    "user",
			Content: "请摘要以下对话历史：\n\n" + conv.String(),
		},
	}

	resp, err := c.llmClient.Chat(ctx, summaryPrompt, nil)
	if err != nil {
		return "", fmt.Errorf("summarize LLM call: %w", err)
	}

	return resp.Message.Content, nil
}

// ================================================================
// 降级策略
// ================================================================

// fallbackSummary 当 LLM 摘要失败时的降级方案
func (c *Compressor) fallbackSummary(msgs []llm.Message) string {
	var parts []string
	for _, m := range msgs {
		if m.Role == "user" {
			parts = append(parts, "- 用户: "+truncateStr(m.Content, 100))
		} else if m.Role == "assistant" && m.Content != "" {
			parts = append(parts, "- 助手: "+truncateStr(m.Content, 100))
		}
	}
	if len(parts) > 5 {
		parts = parts[:5]
	}
	return strings.Join(parts, "\n")
}

// ================================================================
// 工具函数
// ================================================================

// isSummaryMessage 判断一条 system 消息是否为压缩摘要
func isSummaryMessage(m llm.Message) bool {
	return m.Role == "system" && strings.HasPrefix(m.Content, summaryMarkerPrefix)
}

// extractSummaryText 从摘要消息中提取纯摘要文本
func extractSummaryText(m llm.Message) string {
	text := m.Content
	text = strings.TrimPrefix(text, summaryMarkerPrefix)
	text = strings.TrimSuffix(text, summaryMarkerSuffix)
	text = strings.TrimSpace(text)
	return text
}

// findWindowStart 从末尾往前数 windowSize 个 "轮次"，返回窗口起始索引
func findWindowStart(msgs []llm.Message, windowSize int) int {
	userCount := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			userCount++
			if userCount >= windowSize {
				return i
			}
		}
	}
	return 0
}

// truncateStr 截断字符串到指定长度
func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
