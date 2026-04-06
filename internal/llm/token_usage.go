package llm

import (
	"fmt"
	"sync"
	"sync/atomic"

	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
)

type Usage = conv.Usage

type UsageTracker struct {
	mu       sync.Mutex
	rounds   []RoundUsage
	budget   int64
	total    atomic.Int64
	prompt   atomic.Int64
	complete atomic.Int64
	calls    atomic.Int64
}

type RoundUsage struct {
	Round            int    `json:"round"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	Model            string `json:"model,omitempty"`
}

func NewUsageTracker(budget int64) *UsageTracker {
	return &UsageTracker{budget: budget}
}

func (ut *UsageTracker) Record(u Usage) {
	ut.prompt.Add(int64(u.PromptTokens))
	ut.complete.Add(int64(u.CompletionTokens))
	ut.total.Add(int64(u.TotalTokens))
	ut.calls.Add(1)
	ut.mu.Lock()
	ut.rounds = append(ut.rounds, RoundUsage{
		Round: len(ut.rounds) + 1, PromptTokens: u.PromptTokens,
		CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens,
	})
	ut.mu.Unlock()
}

var ErrBudgetExceeded = fmt.Errorf("token budget exceeded")

func (ut *UsageTracker) CheckBudget() error {
	if ut.budget <= 0 { return nil }
	if ut.total.Load() >= ut.budget { return ErrBudgetExceeded }
	return nil
}
func (ut *UsageTracker) WouldExceedBudget(estimatedTokens int) bool {
	if ut.budget <= 0 { return false }
	return ut.total.Load()+int64(estimatedTokens) > ut.budget
}
func (ut *UsageTracker) TotalTokens() int64      { return ut.total.Load() }
func (ut *UsageTracker) PromptTokens() int64     { return ut.prompt.Load() }
func (ut *UsageTracker) CompletionTokens() int64 { return ut.complete.Load() }
func (ut *UsageTracker) CallCount() int64         { return ut.calls.Load() }
func (ut *UsageTracker) Budget() int64            { return ut.budget }
func (ut *UsageTracker) Remaining() int64 {
	if ut.budget <= 0 { return -1 }
	r := ut.budget - ut.total.Load()
	if r < 0 { return 0 }
	return r
}
func (ut *UsageTracker) Rounds() []RoundUsage {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	cp := make([]RoundUsage, len(ut.rounds))
	copy(cp, ut.rounds)
	return cp
}
func (ut *UsageTracker) Summary() string {
	calls := ut.calls.Load()
	prompt := ut.prompt.Load()
	complete := ut.complete.Load()
	total := ut.total.Load()
	s := fmt.Sprintf("Token 用量: %d (prompt: %d, completion: %d) | 调用次数: %d", total, prompt, complete, calls)
	if ut.budget > 0 {
		remaining := ut.budget - total
		if remaining < 0 { remaining = 0 }
		pct := float64(total) / float64(ut.budget) * 100
		s += fmt.Sprintf(" | 预算: %d (已用 %.1f%%, 剩余 %d)", ut.budget, pct, remaining)
	}
	return s
}
func (ut *UsageTracker) Reset() {
	ut.prompt.Store(0)
	ut.complete.Store(0)
	ut.total.Store(0)
	ut.calls.Store(0)
	ut.mu.Lock()
	ut.rounds = nil
	ut.mu.Unlock()
}
