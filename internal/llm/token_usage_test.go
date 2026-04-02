package llm

import (
	"strings"
	"sync"
	"testing"
)

func TestUsageTrackerRecord(t *testing.T) {
	ut := NewUsageTracker(0) // no budget

	ut.Record(Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})
	ut.Record(Usage{PromptTokens: 200, CompletionTokens: 80, TotalTokens: 280})

	if got := ut.TotalTokens(); got != 430 {
		t.Errorf("TotalTokens = %d, want 430", got)
	}
	if got := ut.PromptTokens(); got != 300 {
		t.Errorf("PromptTokens = %d, want 300", got)
	}
	if got := ut.CompletionTokens(); got != 130 {
		t.Errorf("CompletionTokens = %d, want 130", got)
	}
	if got := ut.CallCount(); got != 2 {
		t.Errorf("CallCount = %d, want 2", got)
	}
}

func TestUsageTrackerRounds(t *testing.T) {
	ut := NewUsageTracker(0)

	ut.Record(Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})
	ut.Record(Usage{PromptTokens: 200, CompletionTokens: 80, TotalTokens: 280})

	rounds := ut.Rounds()
	if len(rounds) != 2 {
		t.Fatalf("Rounds len = %d, want 2", len(rounds))
	}
	if rounds[0].PromptTokens != 100 {
		t.Errorf("Round[0].PromptTokens = %d, want 100", rounds[0].PromptTokens)
	}
	if int64(rounds[1].TotalTokens) + int64(rounds[0].TotalTokens) != 430 {
		t.Errorf("Round[1].CumulativeTotal = %d, want 430", int64(rounds[1].TotalTokens) + int64(rounds[0].TotalTokens))
	}
}

func TestUsageTrackerBudgetUnlimited(t *testing.T) {
	ut := NewUsageTracker(0)

	ut.Record(Usage{TotalTokens: 999999})

	if err := ut.CheckBudget(); err != nil {
		t.Errorf("CheckBudget with 0 budget should never fail, got: %v", err)
	}
	if ut.WouldExceedBudget(999999) {
		t.Error("WouldExceedBudget should return false when budget=0")
	}
}

func TestUsageTrackerBudgetEnforced(t *testing.T) {
	ut := NewUsageTracker(1000)

	ut.Record(Usage{TotalTokens: 600})
	if err := ut.CheckBudget(); err != nil {
		t.Errorf("Should be within budget at 600/1000, got: %v", err)
	}
	if ut.Remaining() != 400 {
		t.Errorf("Remaining = %d, want 400", ut.Remaining())
	}

	ut.Record(Usage{TotalTokens: 500})
	err := ut.CheckBudget()
	if err == nil {
		t.Fatal("Should exceed budget at 1100/1000")
	}
	if err != ErrBudgetExceeded {
		t.Errorf("Expected ErrBudgetExceeded, got: %v", err)
	}
}

func TestUsageTrackerWouldExceed(t *testing.T) {
	ut := NewUsageTracker(1000)
	ut.Record(Usage{TotalTokens: 800})

	if !ut.WouldExceedBudget(300) {
		t.Error("800 + 300 > 1000, should return true")
	}
	if ut.WouldExceedBudget(100) {
		t.Error("800 + 100 < 1000, should return false")
	}
}

func TestUsageTrackerSummary(t *testing.T) {
	ut := NewUsageTracker(5000)
	ut.Record(Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})

	s := ut.Summary()
	if !strings.Contains(s, "150") {
		t.Errorf("Summary should contain total tokens '150', got: %s", s)
	}
	if !strings.Contains(s, "5000") {
		t.Errorf("Summary should contain budget '5000', got: %s", s)
	}
}

func TestUsageTrackerSummaryNoBudget(t *testing.T) {
	ut := NewUsageTracker(0)
	ut.Record(Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150})

	s := ut.Summary()
	if strings.Contains(s, "budget") || strings.Contains(s, "预算") {
		t.Errorf("Summary without budget should not mention budget, got: %s", s)
	}
}

func TestUsageTrackerReset(t *testing.T) {
	ut := NewUsageTracker(1000)
	ut.Record(Usage{TotalTokens: 500})

	ut.Reset()
	if got := ut.TotalTokens(); got != 0 {
		t.Errorf("After reset TotalTokens = %d, want 0", got)
	}
	if got := ut.CallCount(); got != 0 {
		t.Errorf("After reset CallCount = %d, want 0", got)
	}
	if got := len(ut.Rounds()); got != 0 {
		t.Errorf("After reset Rounds len = %d, want 0", got)
	}
	// Budget should be preserved
	if got := ut.Budget(); got != 1000 {
		t.Errorf("After reset Budget = %d, want 1000", got)
	}
}

func TestUsageTrackerConcurrency(t *testing.T) {
	ut := NewUsageTracker(0)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ut.Record(Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
			_ = ut.TotalTokens()
			_ = ut.CheckBudget()
			_ = ut.Summary()
		}()
	}
	wg.Wait()

	if got := ut.TotalTokens(); got != 1500 {
		t.Errorf("After 100 concurrent records, TotalTokens = %d, want 1500", got)
	}
	if got := ut.CallCount(); got != 100 {
		t.Errorf("After 100 concurrent records, CallCount = %d, want 100", got)
	}
}

func TestUsageTrackerZeroUsage(t *testing.T) {
	ut := NewUsageTracker(0)
	// Record zero usage (e.g., when API doesn't return usage)
	ut.Record(Usage{})

	if got := ut.TotalTokens(); got != 0 {
		t.Errorf("TotalTokens = %d, want 0", got)
	}
	if got := ut.CallCount(); got != 1 {
		t.Errorf("CallCount = %d, want 1 (still counts as a call)", got)
	}
}
