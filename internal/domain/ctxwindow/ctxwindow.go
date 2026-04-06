package ctxwindow

import (
	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
)

// Priority 消息优先级（数值越大越重要，越不容易被裁剪）
type Priority int

const (
	PriorityLow      Priority = 0  // 旧的工具结果、历史中间过程
	PriorityNormal   Priority = 10 // 普通历史对话
	PriorityHigh     Priority = 20 // 最近几轮对话
	PriorityCritical Priority = 30 // system prompt、摘要、最新 user 消息
)

// ModelProfile 模型的上下文窗口配置
type ModelProfile struct {
	Name             string  // 模型名
	MaxContextTokens int     // 最大上下文 token 数
	MaxOutputTokens  int     // 最大输出 token 数
	ReserveRatio     float64 // 为输出保留的比例（0-1），默认 0.2
}

// Manager 上下文窗口管理器接口（Domain 层定义）
type Manager interface {
	// Fit 将消息裁剪到模型上下文窗口内
	Fit(messages []conv.Message, tools []conv.ToolDef) ([]conv.Message, error)
}

// TokenEstimator token 估算器接口
type TokenEstimator interface {
	EstimateMessage(msg conv.Message) int
	EstimateTools(tools []conv.ToolDef) int
}
