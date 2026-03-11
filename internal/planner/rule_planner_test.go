package planner

import (
	"context"
	"testing"
)

func TestRulePlanner_CalcPrefix(t *testing.T) {
	p := NewRulePlanner()
	plan, err := p.Plan(context.Background(), "calc: (1+2)*3")
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Tool != "calc" {
		t.Fatalf("expected tool calc, got %q", plan.Steps[0].Tool)
	}
}

func TestRulePlanner_MultiStep(t *testing.T) {
	p := NewRulePlanner()
	plan, err := p.Plan(context.Background(), "read: README.md first 3 lines then summarize")
	if err != nil {
		t.Fatalf("Plan error: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[0].Tool != "read_file" {
		t.Fatalf("expected tool read_file, got %q", plan.Steps[0].Tool)
	}
	if plan.Steps[1].Tool != "summarize" {
		t.Fatalf("expected tool summarize, got %q", plan.Steps[1].Tool)
	}
}
