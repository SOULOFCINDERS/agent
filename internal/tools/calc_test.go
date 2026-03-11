package tools

import (
	"context"
	"testing"
)

func TestCalcTool(t *testing.T) {
	tl := NewCalcTool()
	got, err := tl.Execute(context.Background(), map[string]any{"expr": "(1+2)*3"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got != "9" {
		t.Fatalf("expected 9, got %v", got)
	}
}
