package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// mockVerifierLLM 用于验证器的 mock
type mockVerifierLLM struct{}

func (m *mockVerifierLLM) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.ChatResponse, error) {
	for _, msg := range messages {
		if strings.Contains(msg.Content, "提取") || strings.Contains(msg.Content, "关键事实") {
			return &llm.ChatResponse{
				Message: llm.Message{
					Role:    "assistant",
					Content: `["用户需要用Go开发Agent项目", "项目需要记忆压缩功能", "使用了TF-IDF做向量检索"]`,
				},
			}, nil
		}
		if strings.Contains(msg.Content, "增强") || strings.Contains(msg.Content, "遗漏") {
			return &llm.ChatResponse{
				Message: llm.Message{
					Role:    "assistant",
					Content: "用户正在用Go开发Agent项目，需要实现记忆压缩功能，使用了TF-IDF做向量检索。助手提供了代码实现。",
				},
			}, nil
		}
	}
	return &llm.ChatResponse{
		Message: llm.Message{Role: "assistant", Content: "默认回复"},
	}, nil
}

func TestSummaryVerifier_Disabled(t *testing.T) {
	v := NewSummaryVerifier(&mockVerifierLLM{}, VerifierConfig{
		Enabled: false,
	})

	result, vr, err := v.VerifyAndEnhance(context.Background(), "原始摘要", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vr != nil {
		t.Error("expected nil verify result when disabled")
	}
	if result != "原始摘要" {
		t.Errorf("expected original summary returned, got: %s", result)
	}
}

func TestSummaryVerifier_HighCoverage(t *testing.T) {
	v := NewSummaryVerifier(&mockVerifierLLM{}, VerifierConfig{
		Enabled:     true,
		MinCoverage: 0.7,
	})

	msgs := []llm.Message{
		{Role: "user", Content: "帮我用Go开发Agent项目"},
		{Role: "assistant", Content: "好的，我来帮你实现记忆压缩功能，使用TF-IDF做向量检索"},
	}

	// 摘要覆盖了所有关键事实
	summary := "用户需要用Go开发Agent项目，项目需要记忆压缩功能，使用了TF-IDF做向量检索。"

	result, vr, err := v.VerifyAndEnhance(context.Background(), summary, msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("coverage: %.2f, key facts: %v", vr.CoverageRate, vr.KeyFacts)
	t.Logf("covered: %v, missing: %v", vr.CoveredFacts, vr.MissingFacts)

	if vr.Enhanced {
		t.Error("should NOT enhance when coverage is high")
	}
	if result != summary {
		t.Errorf("should return original summary when coverage is high")
	}
}

func TestSummaryVerifier_LowCoverage_Enhance(t *testing.T) {
	v := NewSummaryVerifier(&mockVerifierLLM{}, VerifierConfig{
		Enabled:     true,
		MinCoverage: 0.7,
	})

	msgs := []llm.Message{
		{Role: "user", Content: "帮我用Go开发Agent项目"},
		{Role: "assistant", Content: "好的，我来实现记忆压缩功能，使用TF-IDF做向量检索"},
	}

	// 摘要遗漏了关键信息
	summary := "用户和助手进行了技术讨论。"

	result, vr, err := v.VerifyAndEnhance(context.Background(), summary, msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("coverage: %.2f, missing: %v", vr.CoverageRate, vr.MissingFacts)
	t.Logf("enhanced: %v, result: %s", vr.Enhanced, result)

	if !vr.Enhanced {
		t.Error("should enhance when coverage is low")
	}
	if result == summary {
		t.Error("enhanced summary should differ from original")
	}
}

func TestExtractFactKeywords(t *testing.T) {
	tests := []struct {
		fact     string
		minCount int
	}{
		{"用户需要用Go开发Agent项目", 3},   // Go, Agent, 项目, 开发...
		{"使用了TF-IDF做向量检索", 2},       // TF-IDF, 向量, 检索...
		{"the user needs a simple function", 2}, // user, simple, function
	}

	for _, tt := range tests {
		kws := extractFactKeywords(tt.fact)
		t.Logf("fact: %s → keywords: %v", tt.fact, kws)
		if len(kws) < tt.minCount {
			t.Errorf("expected >= %d keywords for %q, got %d: %v",
				tt.minCount, tt.fact, len(kws), kws)
		}
	}
}

func TestCheckCoverage(t *testing.T) {
	v := NewSummaryVerifier(nil, VerifierConfig{MinCoverage: 0.7})

	facts := []string{
		"用户喜欢咖啡",
		"用户在北京工作",
		"用户使用Go语言",
	}

	// 摘要覆盖了 2/3 的事实
	summary := "用户是一位在北京工作的Go开发者。"

	result := v.checkCoverage(summary, facts)
	t.Logf("coverage: %.2f, covered: %v, missing: %v",
		result.CoverageRate, result.CoveredFacts, result.MissingFacts)

	// "咖啡" 应该被标记为 missing
	hasMissingCoffee := false
	for _, mf := range result.MissingFacts {
		if strings.Contains(mf, "咖啡") {
			hasMissingCoffee = true
		}
	}
	if !hasMissingCoffee {
		t.Error("expected '咖啡' fact to be missing")
	}
}
