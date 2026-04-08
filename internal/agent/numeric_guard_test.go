package agent

import (
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

func TestDetectNumericRisk_CalcNeeded(t *testing.T) {
	// 用户问计算问题，但 LLM 没用 calc 工具
	result := detectNumericRisk(
		"帮我算一下每月存3500，年利率4.2%，5年后有多少",
		"5年后大约有 232,800 元。",
		[]llm.Message{
			{Role: "user", Content: "帮我算一下"},
			{Role: "assistant", Content: "5年后大约有 232,800 元。"},
		},
	)
	if !result.HasRisk {
		t.Error("should detect numeric risk when calc question answered without calc tool")
	}
	if len(result.RiskNumbers) == 0 {
		t.Error("should identify suspicious numbers")
	}
}

func TestDetectNumericRisk_CalcUsed(t *testing.T) {
	// 用户问计算问题，LLM 使用了 calc 工具
	result := detectNumericRisk(
		"帮我算一下 100 * 3.5",
		"结果是 350。",
		[]llm.Message{
			{Role: "user", Content: "帮我算一下 100 * 3.5"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{
				{ID: "c1", Function: llm.FunctionCall{Name: "calc", Arguments: `{"expr":"100*3.5"}`}},
			}},
			{Role: "tool", Content: "350", ToolCallID: "c1"},
			{Role: "assistant", Content: "结果是 350。"},
		},
	)
	if result.HasRisk {
		t.Error("should not flag risk when calc tool was used")
	}
}

func TestDetectNumericRisk_NonCalcQuestion(t *testing.T) {
	// 非计算类问题不应触发
	result := detectNumericRisk(
		"MacBook Neo 多少钱？",
		"MacBook Neo 售价 19999 元起。",
		nil,
	)
	if result.HasRisk {
		t.Error("non-calc question should not trigger numeric risk")
	}
}

func TestDetectNumericRisk_SmallNumbers(t *testing.T) {
	// 小数字和年份不应触发
	result := detectNumericRisk(
		"帮我算一下总共多少",
		"总共有 3 个，分别是 2024 年和 2025 年的。",
		[]llm.Message{},
	)
	if result.HasRisk {
		t.Error("small numbers and years should be filtered out")
	}
}

func TestHasCalcToolDef(t *testing.T) {
	defs := []llm.ToolDef{
		{Function: llm.FuncDef{Name: "web_search"}},
		{Function: llm.FuncDef{Name: "calc"}},
	}
	if !hasCalcToolDef(defs) {
		t.Error("should find calc tool")
	}

	defsNoCalc := []llm.ToolDef{
		{Function: llm.FuncDef{Name: "web_search"}},
	}
	if hasCalcToolDef(defsNoCalc) {
		t.Error("should not find calc tool")
	}
}
