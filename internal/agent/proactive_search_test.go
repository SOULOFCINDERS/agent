package agent

import (
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

func TestDetectProactiveSearch_ProductName(t *testing.T) {
	toolDefs := []llm.ToolDef{{Function: llm.FuncDef{Name: "web_search"}}}

	tests := []struct {
		name     string
		msg      string
		want     bool
	}{
		{"MacBook NEO", "MacBook NEO 怎么样", true},
		{"iPhone 17 Pro", "iPhone 17 Pro 值得买吗", true},
		{"RTX 5090", "RTX 5090 性能如何", true},
		{"Galaxy S26", "Galaxy S26 多少钱", true},
		{"PS6", "PS6 什么时候发布", true},
		{"Generic question", "今天天气怎么样", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectProactiveSearch(tt.msg, toolDefs)
			if result.ShouldSearch != tt.want {
				t.Errorf("ShouldSearch = %v, want %v", result.ShouldSearch, tt.want)
			}
			if tt.want && result.Entity == "" {
				t.Errorf("Entity should not be empty when ShouldSearch is true")
			}
		})
	}
}

func TestDetectProactiveSearch_NoWebSearchTool(t *testing.T) {
	toolDefs := []llm.ToolDef{{Function: llm.FuncDef{Name: "calc"}}}
	result := detectProactiveSearch("MacBook NEO 怎么样", toolDefs)
	if result.ShouldSearch {
		t.Error("should not trigger without web_search tool")
	}
}

func TestDetectProactiveSearch_EntityIndicators(t *testing.T) {
	toolDefs := []llm.ToolDef{{Function: llm.FuncDef{Name: "web_search"}}}

	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"值得买", "华为Mate 70值得买吗", true},
		{"怎么样", "小米SU7怎么样", true},
		{"发布了吗", "新款Switch发布了吗", true},
		{"Too short", "好不好", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectProactiveSearch(tt.msg, toolDefs)
			if result.ShouldSearch != tt.want {
				t.Errorf("ShouldSearch = %v, want %v for msg=%q", result.ShouldSearch, tt.want, tt.msg)
			}
		})
	}
}

func TestExtractEntityFromMessage(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{"MacBook NEO怎么样", "MacBook NEO"},
		{"请问华为Mate 70值得买吗", "华为Mate 70"},
		{"帮我查小米SU7多少钱", "小米SU7"},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got := extractEntityFromMessage(tt.msg)
			if got != tt.want {
				t.Errorf("extractEntityFromMessage(%q) = %q, want %q", tt.msg, got, tt.want)
			}
		})
	}
}

func TestBuildProactiveSearchMessage(t *testing.T) {
	msg := buildProactiveSearchMessage("MacBook NEO")
	if msg.Role != "user" {
		t.Errorf("Role = %q, want 'user'", msg.Role)
	}
	if msg.Content == "" {
		t.Error("Content should not be empty")
	}
	if len(msg.Content) < 10 {
		t.Error("Content should be a meaningful instruction")
	}
}
