package ctxwindow

import (
	"fmt"
	"sync"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ---- 消息优先级 ----

// Priority 消息优先级（数值越大越重要，越不容易被裁剪）
type Priority int

const (
	PriorityLow      Priority = 0  // 旧的工具结果、历史中间过程
	PriorityNormal   Priority = 10 // 普通历史对话
	PriorityHigh     Priority = 20 // 最近几轮对话
	PriorityCritical Priority = 30 // system prompt、摘要、最新 user 消息
)

// ---- 模型配置预设 ----

// ModelProfile 模型的上下文窗口配置
type ModelProfile struct {
	Name             string // 模型名
	MaxContextTokens int    // 最大上下文 token 数
	MaxOutputTokens  int    // 最大输出 token 数
	ReserveRatio     float64 // 为输出保留的比例（0-1），默认 0.2
}

// 预置的常见模型配置
var KnownModels = map[string]ModelProfile{
	"gpt-3.5-turbo":     {Name: "gpt-3.5-turbo", MaxContextTokens: 4096, MaxOutputTokens: 4096, ReserveRatio: 0.25},
	"gpt-3.5-turbo-16k": {Name: "gpt-3.5-turbo-16k", MaxContextTokens: 16384, MaxOutputTokens: 4096, ReserveRatio: 0.2},
	"gpt-4":             {Name: "gpt-4", MaxContextTokens: 8192, MaxOutputTokens: 4096, ReserveRatio: 0.25},
	"gpt-4-turbo":       {Name: "gpt-4-turbo", MaxContextTokens: 128000, MaxOutputTokens: 4096, ReserveRatio: 0.1},
	"gpt-4o":            {Name: "gpt-4o", MaxContextTokens: 128000, MaxOutputTokens: 16384, ReserveRatio: 0.1},
	"deepseek-chat":     {Name: "deepseek-chat", MaxContextTokens: 64000, MaxOutputTokens: 8192, ReserveRatio: 0.15},
	"deepseek-v3":       {Name: "deepseek-v3", MaxContextTokens: 64000, MaxOutputTokens: 8192, ReserveRatio: 0.15},
	"qwen2.5:14b":       {Name: "qwen2.5:14b", MaxContextTokens: 32768, MaxOutputTokens: 8192, ReserveRatio: 0.2},
	"qwen2.5:32b":       {Name: "qwen2.5:32b", MaxContextTokens: 32768, MaxOutputTokens: 8192, ReserveRatio: 0.2},
	"qwen2.5:72b":       {Name: "qwen2.5:72b", MaxContextTokens: 131072, MaxOutputTokens: 8192, ReserveRatio: 0.1},
}

// DefaultProfile 用于未知模型的默认配置
var DefaultProfile = ModelProfile{
	Name:             "default",
	MaxContextTokens: 8192,
	MaxOutputTokens:  4096,
	ReserveRatio:     0.25,
}

// LookupModel 查找模型配置，未知模型返回默认配置
func LookupModel(name string) ModelProfile {
	if p, ok := KnownModels[name]; ok {
		return p
	}
	// 尝试前缀匹配
	for key, p := range KnownModels {
		if len(name) > len(key) && name[:len(key)] == key {
			return p
		}
	}
	return DefaultProfile
}

// ---- 窗口管理器 ----

// ManagerConfig 窗口管理器配置
type ManagerConfig struct {
	// Model 模型配置（为空则使用 DefaultProfile）
	Model ModelProfile

	// MaxInputTokens 最大输入 token 数
	// 如果设置为 0，自动根据 Model 计算: MaxContextTokens * (1 - ReserveRatio)
	MaxInputTokens int

	// ProtectRecentRounds 保护最近 N 轮不被裁剪（默认 2）
	ProtectRecentRounds int

	// ToolResultMaxTokens 单个工具结果的最大 token 数（超过则截断）
	ToolResultMaxTokens int

	// SummaryTokenBudget 摘要的 token 预算（默认 300）
	SummaryTokenBudget int

	// EnableAutoTruncate 启用自动截断（默认 true）
	EnableAutoTruncate bool
}

// Manager 上下文窗口管理器
// 职责：
//   1. 估算当前 history 的 token 数
//   2. 判断是否即将超出窗口
//   3. 按优先级裁剪消息，保证不超出模型上下文窗口
//   4. 提供窗口使用情况报告
type Manager struct {
	mu        sync.Mutex
	config    ManagerConfig
	estimator *TokenEstimator

	// 统计数据
	totalFits      int // Fit 调用总次数
	totalTruncates int // 发生截断的次数
}

// NewManager 创建上下文窗口管理器
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.Model.MaxContextTokens == 0 {
		cfg.Model = DefaultProfile
	}
	if cfg.MaxInputTokens == 0 {
		reserved := int(float64(cfg.Model.MaxContextTokens) * cfg.Model.ReserveRatio)
		cfg.MaxInputTokens = cfg.Model.MaxContextTokens - reserved
	}
	if cfg.ProtectRecentRounds <= 0 {
		cfg.ProtectRecentRounds = 2
	}
	if cfg.ToolResultMaxTokens <= 0 {
		cfg.ToolResultMaxTokens = 2000
	}
	if cfg.SummaryTokenBudget <= 0 {
		cfg.SummaryTokenBudget = 300
	}

	return &Manager{
		config:    cfg,
		estimator: DefaultEstimator(),
	}
}

// WindowStatus 窗口使用状态
type WindowStatus struct {
	MaxInputTokens   int     // 最大输入 token 预算
	EstimatedTokens  int     // 当前估算 token 数
	UsagePercent     float64 // 使用率 (0-1)
	MessageCount     int     // 消息总数
	HasRoom          bool    // 是否还有空间
	RemainingTokens  int     // 剩余 token 数
}

// Status 返回当前 history 的窗口使用状态
func (m *Manager) Status(history []llm.Message) WindowStatus {
	estimated := m.EstimateHistory(history)
	remaining := m.config.MaxInputTokens - estimated
	if remaining < 0 {
		remaining = 0
	}

	return WindowStatus{
		MaxInputTokens:  m.config.MaxInputTokens,
		EstimatedTokens: estimated,
		UsagePercent:    float64(estimated) / float64(m.config.MaxInputTokens),
		MessageCount:    len(history),
		HasRoom:         estimated < m.config.MaxInputTokens,
		RemainingTokens: remaining,
	}
}

// EstimateHistory 估算整个 history 的 token 数
func (m *Manager) EstimateHistory(history []llm.Message) int {
	total := 3 // 基础开销: <|im_start|>...<|im_end|> 等
	for _, msg := range history {
		total += m.estimateMessage(msg)
	}
	return total
}

// estimateMessage 估算单条消息的 token 数
func (m *Manager) estimateMessage(msg llm.Message) int {
	tokens := m.estimator.OverheadPerMessage
	tokens += m.estimator.EstimateText(msg.Content)

	for _, tc := range msg.ToolCalls {
		tokens += m.estimator.OverheadPerToolCall
		tokens += m.estimator.EstimateText(tc.Function.Name)
		tokens += m.estimator.EstimateToolCallJSON(tc.Function.Arguments)
	}

	if msg.ToolCallID != "" {
		tokens += m.estimator.EstimateText(msg.ToolCallID)
	}

	return tokens
}

// NeedsTruncation 判断 history 是否需要截断
func (m *Manager) NeedsTruncation(history []llm.Message) bool {
	return m.EstimateHistory(history) > m.config.MaxInputTokens
}

// WouldExceed 预判添加新消息后是否会超出窗口
func (m *Manager) WouldExceed(history []llm.Message, newMsgTokenEstimate int) bool {
	current := m.EstimateHistory(history)
	return current+newMsgTokenEstimate > m.config.MaxInputTokens
}

// Fit 核心方法：确保 history 适配上下文窗口
// 返回裁剪后的 history（不修改原始 slice）
//
// 裁剪策略（按优先级从低到高）：
//   1. 截断过长的工具结果
//   2. 移除旧的工具结果消息
//   3. 移除旧的对话轮次（保护最近 N 轮）
//   4. 如果仍然超出，截断最早的保护轮次中的工具结果
//
// 绝不裁剪：system 消息、最新的 user 消息
func (m *Manager) Fit(history []llm.Message) []llm.Message {
	m.mu.Lock()
	m.totalFits++
	m.mu.Unlock()

	if !m.NeedsTruncation(history) {
		return history
	}

	m.mu.Lock()
	m.totalTruncates++
	m.mu.Unlock()

	// 复制一份避免修改原始数据
	result := make([]llm.Message, len(history))
	copy(result, history)

	// Phase 1: 截断过长的工具结果
	result = m.truncateLongToolResults(result)
	if m.EstimateHistory(result) <= m.config.MaxInputTokens {
		return result
	}

	// Phase 2: 标记每条消息的优先级，然后按优先级从低到高移除
	priorities := m.assignPriorities(result)
	result = m.removeByPriority(result, priorities)

	return result
}

// truncateLongToolResults 截断超长的工具结果
func (m *Manager) truncateLongToolResults(msgs []llm.Message) []llm.Message {
	maxTokens := m.config.ToolResultMaxTokens

	for i, msg := range msgs {
		if msg.Role != "tool" {
			continue
		}
		estimated := m.estimator.EstimateText(msg.Content)
		if estimated > maxTokens {
			// 按字符比例截断
			ratio := float64(maxTokens) / float64(estimated)
			maxChars := int(float64(len(msg.Content)) * ratio)
			if maxChars < 100 {
				maxChars = 100
			}
			runes := []rune(msg.Content)
			if maxChars < len(runes) {
				msgs[i].Content = string(runes[:maxChars]) + "\n... [truncated by context window manager]"
			}
		}
	}
	return msgs
}

// assignPriorities 为每条消息分配优先级
func (m *Manager) assignPriorities(msgs []llm.Message) []Priority {
	priorities := make([]Priority, len(msgs))

	// 找到最后 N 个 user 消息的位置，用于确定保护范围
	protectStart := findProtectStart(msgs, m.config.ProtectRecentRounds)

	for i, msg := range msgs {
		switch {
		case msg.Role == "system":
			priorities[i] = PriorityCritical

		case i >= protectStart && msg.Role == "user":
			priorities[i] = PriorityCritical

		case i >= protectStart:
			priorities[i] = PriorityHigh

		case msg.Role == "tool":
			priorities[i] = PriorityLow

		case msg.Role == "assistant" && len(msg.ToolCalls) > 0 && msg.Content == "":
			// 纯工具调用的 assistant 消息，依赖其对应的 tool 结果
			priorities[i] = PriorityLow

		default:
			priorities[i] = PriorityNormal
		}
	}

	return priorities
}

// removeByPriority 按优先级从低到高移除消息，直到在预算内
func (m *Manager) removeByPriority(msgs []llm.Message, priorities []Priority) []llm.Message {
	budget := m.config.MaxInputTokens

	// 按优先级分组索引
	levels := []Priority{PriorityLow, PriorityNormal, PriorityHigh}

	keep := make([]bool, len(msgs))
	for i := range keep {
		keep[i] = true
	}

	for _, level := range levels {
		if m.estimateKept(msgs, keep) <= budget {
			break
		}

		// 从前往后移除该优先级的消息
		for i := 0; i < len(msgs); i++ {
			if priorities[i] != level || !keep[i] {
				continue
			}

			// 检查是否可以安全移除（不破坏 tool_call → tool 的配对）
			if canRemove(msgs, keep, i) {
				keep[i] = true // 先标记为 false 的候选
				// 如果是 assistant+tool_calls，同时移除对应的 tool 结果
				if msgs[i].Role == "assistant" && len(msgs[i].ToolCalls) > 0 {
					toolCallIDs := make(map[string]bool)
					for _, tc := range msgs[i].ToolCalls {
						toolCallIDs[tc.ID] = true
					}
					for j := i + 1; j < len(msgs); j++ {
						if msgs[j].Role == "tool" && toolCallIDs[msgs[j].ToolCallID] {
							keep[j] = false
						}
					}
				}
				keep[i] = false
			}

			if m.estimateKept(msgs, keep) <= budget {
				break
			}
		}
	}

	// 构建结果
	var result []llm.Message
	for i, msg := range msgs {
		if keep[i] {
			result = append(result, msg)
		}
	}

	return result
}

// estimateKept 估算保留消息的总 token 数
func (m *Manager) estimateKept(msgs []llm.Message, keep []bool) int {
	total := 3
	for i, msg := range msgs {
		if keep[i] {
			total += m.estimateMessage(msg)
		}
	}
	return total
}

// canRemove 检查移除某条消息是否安全
// 规则：tool 消息可以和对应的 assistant（含 tool_calls）一起移除
func canRemove(msgs []llm.Message, keep []bool, idx int) bool {
	msg := msgs[idx]

	// system 消息绝不移除
	if msg.Role == "system" {
		return false
	}

	// tool 消息：检查对应的 assistant 是否已被移除
	if msg.Role == "tool" && msg.ToolCallID != "" {
		// 找到对应的 assistant 消息
		for i := idx - 1; i >= 0; i-- {
			if msgs[i].Role == "assistant" {
				for _, tc := range msgs[i].ToolCalls {
					if tc.ID == msg.ToolCallID {
						// 对应的 assistant 还在，需要一起移除
						return keep[i] == false || true
					}
				}
			}
		}
	}

	return true
}

// findProtectStart 找到需要保护的最近 N 轮对话的起始位置
func findProtectStart(msgs []llm.Message, rounds int) int {
	userCount := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			userCount++
			if userCount >= rounds {
				return i
			}
		}
	}
	return 0
}

// Stats 返回管理器统计信息
func (m *Manager) Stats() (totalFits, totalTruncates int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalFits, m.totalTruncates
}

// Config 返回当前配置（只读）
func (m *Manager) Config() ManagerConfig {
	return m.config
}

// FormatStatus 返回人类可读的窗口状态
func FormatStatus(s WindowStatus) string {
	return fmt.Sprintf(
		"上下文窗口: %d/%d tokens (%.1f%%) | %d 条消息 | 剩余 %d tokens",
		s.EstimatedTokens, s.MaxInputTokens,
		s.UsagePercent*100,
		s.MessageCount,
		s.RemainingTokens,
	)
}
