// Package memory implements conversation memory storage with similarity search and context compression.
package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// Compressor 负责短期记忆（会话历史）的压缩
// 策略：滑动窗口 + LLM 摘要
//
// 工作原理:
//   1. 保留最近 N 轮消息（窗口内），确保最新上下文完整
//   2. 窗口外的历史消息由 LLM 生成一段摘要
//   3. 摘要作为一条 system 消息插入，替换原始历史
//   4. 总消息数 = 1(system) + 1(摘要) + 窗口内消息
//
// Token 节省效果：
//   - 10 轮对话 × 每轮约 800 token = 8000 token
//   - 压缩后：摘要 ~300 token + 最近 3 轮 ~2400 token = ~2700 token
//   - 节省约 66%
type Compressor struct {
	llmClient   llm.Client
	windowSize  int // 保留最近几轮完整消息（一轮 = user 消息开始到下一个 user 消息之前）
	maxMessages int // 触发压缩的消息数阈值
}

// CompressorConfig 压缩器配置
type CompressorConfig struct {
	// WindowSize 保留最近几轮的完整消息，默认 3
	WindowSize int
	// MaxMessages 当 history 消息总数超过此值时触发压缩，默认 12
	MaxMessages int
}

// NewCompressor 创建历史压缩器
func NewCompressor(client llm.Client, cfg CompressorConfig) *Compressor {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 3
	}
	if cfg.MaxMessages <= 0 {
		cfg.MaxMessages = 12
	}
	return &Compressor{
		llmClient:   client,
		windowSize:  cfg.WindowSize,
		maxMessages: cfg.MaxMessages,
	}
}

// CompressResult 压缩结果
type CompressResult struct {
	Messages        []llm.Message // 压缩后的消息列表
	WasCompressed   bool          // 是否发生了压缩
	OriginalCount   int           // 原始消息数
	CompressedCount int           // 压缩后消息数
	SummaryText     string        // 生成的摘要（用于调试）
}

// Compress 压缩会话历史
// 传入完整 history（包含 system 消息），返回压缩后的 history
func (c *Compressor) Compress(ctx context.Context, history []llm.Message) (*CompressResult, error) {
	result := &CompressResult{
		Messages:      history,
		OriginalCount: len(history),
	}

	// 不需要压缩的情况
	if len(history) <= c.maxMessages {
		result.CompressedCount = len(history)
		return result, nil
	}

	// 分离出 system 消息和对话消息
	var systemMsgs []llm.Message
	var convMsgs []llm.Message
	for _, m := range history {
		if m.Role == "system" {
			systemMsgs = append(systemMsgs, m)
		} else {
			convMsgs = append(convMsgs, m)
		}
	}

	// 计算窗口：保留最近 windowSize 轮
	windowStart := findWindowStart(convMsgs, c.windowSize)

	if windowStart <= 0 {
		result.CompressedCount = len(history)
		return result, nil
	}

	// 需要被压缩的消息
	toCompress := convMsgs[:windowStart]
	// 窗口内保留的消息
	windowMsgs := convMsgs[windowStart:]

	// 用 LLM 生成摘要
	summary, err := c.summarize(ctx, toCompress)
	if err != nil {
		// 摘要失败时退化为简单截断
		summary = c.fallbackSummary(toCompress)
	}

	result.SummaryText = summary
	result.WasCompressed = true

	// 组装新的历史
	var newHistory []llm.Message

	// 1. system 消息
	newHistory = append(newHistory, systemMsgs...)

	// 2. 摘要作为一条 system 消息插入
	if summary != "" {
		newHistory = append(newHistory, llm.Message{
			Role:    "system",
			Content: "[对话历史摘要]\n" + summary + "\n[以下是最近的对话]",
		})
	}

	// 3. 窗口内的完整消息
	newHistory = append(newHistory, windowMsgs...)

	result.Messages = newHistory
	result.CompressedCount = len(newHistory)

	return result, nil
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

// truncateStr 截断字符串到指定长度
func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// NeedCompress 检查历史是否需要压缩
func (c *Compressor) NeedCompress(history []llm.Message) bool {
	return len(history) > c.maxMessages
}
