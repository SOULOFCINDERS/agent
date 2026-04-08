package agent

import (
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

func TestDetectUnverifiedCitations_FakeQuote(t *testing.T) {
	reply := `爱因斯坦曾说过："量子力学越成功，它看起来就越荒谬。"这句话深刻揭示了量子力学的哲学困境。`
	result := detectUnverifiedCitations(reply, nil) // no tool calls
	if !result.HasUnverifiedQuotes {
		t.Error("should detect unverified quote")
	}
}

func TestDetectUnverifiedCitations_VerifiedQuote(t *testing.T) {
	reply := `爱因斯坦曾说过："上帝不掷骰子。"这是他对量子力学的著名评论。`
	history := []llm.Message{
		{Role: "tool", Content: `爱因斯坦曾说过："上帝不掷骰子。" 这是他在1926年写给玻尔的信中的话。`},
	}
	result := detectUnverifiedCitations(reply, history)
	if result.HasUnverifiedQuotes {
		t.Error("should not flag quote that appears in tool results")
	}
}

func TestDetectUnverifiedCitations_BookQuote(t *testing.T) {
	reply := `在《人月神话》中，Brooks 指出："没有银弹——没有任何单一的技术或管理进步能够在十年内使生产力提高一个数量级。"`
	result := detectUnverifiedCitations(reply, nil)
	if !result.HasUnverifiedBooks {
		t.Error("should detect unverified book citation")
	}
	if len(result.SuspiciousBooks) == 0 {
		t.Error("should identify the book name")
	}
}

func TestDetectUnverifiedCitations_NoQuotes(t *testing.T) {
	reply := "Go 语言是 Google 在 2009 年开源的编程语言，它的特点是简洁、并发支持好。"
	result := detectUnverifiedCitations(reply, nil)
	if result.HasUnverifiedQuotes || result.HasUnverifiedBooks {
		t.Error("plain factual statement without quotes should not trigger")
	}
}

func TestCleanUnverifiedCitations(t *testing.T) {
	content := "爱因斯坦说过一些话。"
	check := CitationCheckResult{
		HasUnverifiedQuotes: true,
		SuspiciousQuotes:    []string{"某引用"},
	}
	cleaned := cleanUnverifiedCitations(content, check)
	if cleaned == content {
		t.Error("should add disclaimer")
	}
	if !containsStr(cleaned, "⚠️") {
		t.Error("should contain warning emoji")
	}
}

func TestCleanUnverifiedCitations_NoIssues(t *testing.T) {
	content := "正常回复。"
	check := CitationCheckResult{}
	cleaned := cleanUnverifiedCitations(content, check)
	if cleaned != content {
		t.Error("should not modify content when no issues")
	}
}
