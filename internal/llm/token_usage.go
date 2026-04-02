package llm

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Usage 单次 LLM 调用的 token 用量
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// UsageTracker 全局 token 用量追踪器（线程安全）
// 统计维度：单会话累计、各轮次明细
type UsageTracker struct {
	mu       sync.Mutex
	rounds   []RoundUsage // 每轮明细
	budget   int64        // token 预算上限，0 = 无限制
	total    atomic.Int64 // 原子计数，热路径无锁
	prompt   atomic.Int64
	complete atomic.Int64
	calls    atomic.Int64
}

// RoundUsage 单轮 LLM 调用的 token 用量
type RoundUsage struct {
	Round            int    `json:"round"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	Model            string `json:"model,omitempty"`
}

// NewUsageTracker 创建用量追踪器
// budget=0 表示无限制
func NewUsageTracker(budget int64) *UsageTracker {
	return &UsageTracker{
		budget: budget,
	}
}

// Record 记录一次 LLM 调用的 token 用量
func (ut *UsageTracker) Record(u Usage) {
	ut.prompt.Add(int64(u.PromptTokens))
	ut.complete.Add(int64(u.CompletionTokens))
	ut.total.Add(int64(u.TotalTokens))
	ut.calls.Add(1)

	ut.mu.Lock()
	ut.rounds = append(ut.rounds, RoundUsage{
		Round:            len(ut.rounds) + 1,
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	})
	ut.mu.Unlock()
}

// ErrBudgetExceeded 预算超限错误
var ErrBudgetExceeded = fmt.Errorf("token budget exceeded")

// CheckBudget 检查是否超出预算
// 返回 nil 表示还在预算内，返回 ErrBudgetExceeded 表示超限
func (ut *UsageTracker) CheckBudget() error {
	if ut.budget <= 0 {
		return nil // 无限制
	}
	if ut.total.Load() >= ut.budget {
		return ErrBudgetExceeded
	}
	return nil
}

// WouldExceedBudget 预判本次调用后是否会超限
// estimatedTokens 是预估的本次 token 消耗
func (ut *UsageTracker) WouldExceedBudget(estimatedTokens int) bool {
	if ut.budget <= 0 {
		return false
	}
	return ut.total.Load()+int64(estimatedTokens) > ut.budget
}

// TotalTokens 返回累计 total token
func (ut *UsageTracker) TotalTokens() int64 {
	return ut.total.Load()
}

// PromptTokens 返回累计 prompt token
func (ut *UsageTracker) PromptTokens() int64 {
	return ut.prompt.Load()
}

// CompletionTokens 返回累计 completion token
func (ut *UsageTracker) CompletionTokens() int64 {
	return ut.complete.Load()
}

// CallCount 返回 LLM 调用次数
func (ut *UsageTracker) CallCount() int64 {
	return ut.calls.Load()
}

// Budget 返回预算上限
func (ut *UsageTracker) Budget() int64 {
	return ut.budget
}

// Remaining 返回剩余 token 额度，无限制返回 -1
func (ut *UsageTracker) Remaining() int64 {
	if ut.budget <= 0 {
		return -1
	}
	r := ut.budget - ut.total.Load()
	if r < 0 {
		return 0
	}
	return r
}

// Rounds 返回每轮明细的副本
func (ut *UsageTracker) Rounds() []RoundUsage {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	cp := make([]RoundUsage, len(ut.rounds))
	copy(cp, ut.rounds)
	return cp
}

// Summary 返回人类可读的用量摘要
func (ut *UsageTracker) Summary() string {
	calls := ut.calls.Load()
	prompt := ut.prompt.Load()
	complete := ut.complete.Load()
	total := ut.total.Load()

	s := fmt.Sprintf("Token 用量: %d (prompt: %d, completion: %d) | 调用次数: %d",
		total, prompt, complete, calls)

	if ut.budget > 0 {
		remaining := ut.budget - total
		if remaining < 0 {
			remaining = 0
		}
		pct := float64(total) / float64(ut.budget) * 100
		s += fmt.Sprintf(" | 预算: %d (已用 %.1f%%, 剩余 %d)", ut.budget, pct, remaining)
	}

	return s
}

// Reset 重置所有计数（新会话时调用）
func (ut *UsageTracker) Reset() {
	ut.prompt.Store(0)
	ut.complete.Store(0)
	ut.total.Store(0)
	ut.calls.Store(0)
	ut.mu.Lock()
	ut.rounds = nil
	ut.mu.Unlock()
}
