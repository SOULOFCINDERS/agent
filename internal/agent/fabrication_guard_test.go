package agent

import (
	"strings"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ---- 数值编造测试 ----

func TestDetectFabrication_NumericRisk(t *testing.T) {
	toolDefs := []llm.ToolDef{{Function: llm.FuncDef{Name: "calc"}}}
	history := []llm.Message{
		{Role: "user", Content: "帮我算一下 3456 * 789"},
	}
	reply := "3456 × 789 = 2,726,784"

	result := DetectFabrication("帮我算一下 3456 * 789", reply, history, toolDefs)
	if !result.NumericRisk {
		t.Error("should detect numeric risk when calc tool not used")
	}
	if !result.NeedsRegeneration() {
		t.Error("numeric risk should need regeneration")
	}
}

func TestDetectFabrication_NumericRisk_CalcUsed(t *testing.T) {
	toolDefs := []llm.ToolDef{{Function: llm.FuncDef{Name: "calc"}}}
	history := []llm.Message{
		{Role: "user", Content: "帮我算一下 3456 * 789"},
		{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{
			{Function: llm.FunctionCall{Name: "calc", Arguments: `{"expr":"3456*789"}`}},
		}},
		{Role: "tool", Content: "2726784"},
	}
	reply := "3456 × 789 = 2,726,784"

	result := DetectFabrication("帮我算一下 3456 * 789", reply, history, toolDefs)
	if result.NumericRisk {
		t.Error("should not flag when calc tool was used")
	}
}

func TestDetectFabrication_NumericRisk_NotCalcQuestion(t *testing.T) {
	toolDefs := []llm.ToolDef{{Function: llm.FuncDef{Name: "calc"}}}
	history := []llm.Message{}
	reply := "MacBook Pro 售价 14999 元起"

	result := DetectFabrication("MacBook Pro 多少钱", reply, history, toolDefs)
	if result.NumericRisk {
		t.Error("price query should not trigger numeric risk")
	}
}

// ---- URL 编造测试 ----

func TestDetectFabrication_FabricatedURL(t *testing.T) {
	toolDefs := []llm.ToolDef{{Function: llm.FuncDef{Name: "web_search"}}}
	history := []llm.Message{
		{Role: "tool", Content: "搜索结果：https://real-site.com/article"},
	}
	reply := "更多信息请访问 https://real-site.com/article 和 https://fake-site.com/not-real"

	result := DetectFabrication("", reply, history, toolDefs)
	if len(result.FabricatedURLs) != 1 {
		t.Errorf("expected 1 fabricated URL, got %d", len(result.FabricatedURLs))
	}
	if len(result.FabricatedURLs) > 0 && !strings.Contains(result.FabricatedURLs[0], "fake-site.com") {
		t.Errorf("expected fake-site.com, got %s", result.FabricatedURLs[0])
	}
}

func TestDetectFabrication_NoToolResults_NoFlagURL(t *testing.T) {
	history := []llm.Message{}
	reply := "GitHub 仓库：https://github.com/example/repo"

	result := DetectFabrication("", reply, history, nil)
	if len(result.FabricatedURLs) > 0 {
		t.Error("should not flag URLs when no tool results exist")
	}
}

// ---- 引用编造测试 ----

func TestDetectFabrication_UnverifiedQuote(t *testing.T) {
	history := []llm.Message{} // 没有任何搜索
	reply := `爱因斯坦曾说过："想象力比知识更重要。"`

	result := DetectFabrication("", reply, history, nil)
	if !result.HasUnverifiedQuotes {
		t.Error("should detect unverified quote")
	}
}

func TestDetectFabrication_VerifiedQuote(t *testing.T) {
	history := []llm.Message{
		{Role: "tool", Content: `爱因斯坦曾说过："想象力比知识更重要。"这句话出自1931年的一次演讲。`},
	}
	reply := `爱因斯坦曾说过："想象力比知识更重要。"`

	result := DetectFabrication("", reply, history, nil)
	if result.HasUnverifiedQuotes {
		t.Error("should not flag quote that exists in tool results")
	}
}

func TestDetectFabrication_UnverifiedBook(t *testing.T) {
	history := []llm.Message{}
	reply := `在《思考，快与慢》一书中提到，人的决策受两个系统支配。`

	result := DetectFabrication("", reply, history, nil)
	if !result.HasUnverifiedBooks {
		t.Error("should detect unverified book citation")
	}
}

// ---- 统一修正测试 ----

func TestFixFabricatedContent_URL(t *testing.T) {
	content := "请看 [文章](https://fake.com/article) 了解更多"
	result := FabricationCheckResult{
		FabricatedURLs: []string{"https://fake.com/article"},
	}
	fixed := FixFabricatedContent(content, result)
	if strings.Contains(fixed, "https://fake.com") {
		t.Error("fabricated URL should be removed")
	}
	if !strings.Contains(fixed, "文章") {
		t.Error("link text should be preserved")
	}
}

func TestFixFabricatedContent_Citation(t *testing.T) {
	content := "这是一段正常的回答。"
	result := FabricationCheckResult{
		HasUnverifiedQuotes: true,
		SuspiciousQuotes:    []string{"某人曾说过："},
	}
	fixed := FixFabricatedContent(content, result)
	if !strings.Contains(fixed, "⚠️") {
		t.Error("should add disclaimer for unverified citations")
	}
}

func TestHasFabrication(t *testing.T) {
	empty := FabricationCheckResult{}
	if empty.HasFabrication() {
		t.Error("empty result should not have fabrication")
	}

	numericOnly := FabricationCheckResult{NumericRisk: true, RiskNumbers: []string{"123"}}
	if !numericOnly.HasFabrication() {
		t.Error("numeric risk should count as fabrication")
	}

	urlOnly := FabricationCheckResult{FabricatedURLs: []string{"https://x.com"}}
	if !urlOnly.HasFabrication() {
		t.Error("fabricated URL should count as fabrication")
	}
}
