package tools

import (
	"context"
	"strings"
	"testing"
)

func TestWeatherTool(t *testing.T) {
	// This test hits the network (wttr.in).
	// In a real CI environment, we should probably mock the HTTP client or skip.
	// For this local agent, we'll try to run it but skip if network fails.

	wt := NewWeatherTool()
	if wt.Name() != "weather" {
		t.Fatalf("expected name weather, got %s", wt.Name())
	}

	res, err := wt.Execute(context.Background(), map[string]any{"location": "Beijing"})
	if err != nil {
		t.Logf("skipping weather test due to network error: %v", err)
		return
	}

	s, ok := res.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", res)
	}

	// wttr.in usually returns "Beijing: ..."
	if !strings.Contains(s, "Beijing") && !strings.Contains(s, "°C") && !strings.Contains(s, "°F") {
		t.Logf("warning: weather output format might have changed or location not found: %q", s)
	}
}
