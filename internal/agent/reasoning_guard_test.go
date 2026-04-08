package agent

import (
	"testing"
)

func TestDetectReasoningContradiction_CarWash(t *testing.T) {
	// 模拟实际场景：推理中说"当然开车去"，结论却说"走路去"
	reply := `这是一个需要逻辑推理的问题。

分析：
- 距离仅50米，步行很近
- 但是去洗车店的目的是洗车，你当然需要开车去把车带到洗车店

如果你车就停在家门口，且准备直接开进去洗，那你当然开车去。

✅ 结论：
走路去 -- 更快、更省事。`

	result := detectReasoningContradiction(reply)
	if !result.HasContradiction {
		t.Error("should detect contradiction: reasoning says '开车去' but conclusion says '走路去'")
	}
}

func TestDetectReasoningContradiction_Consistent(t *testing.T) {
	// 推理和结论一致的情况
	reply := `分析：去洗车店需要带车，所以必须开车。

✅ 结论：
建议开车去，因为洗车需要把车带过去。`

	result := detectReasoningContradiction(reply)
	if result.HasContradiction {
		t.Error("should not detect contradiction when reasoning and conclusion are consistent")
	}
}

func TestDetectReasoningContradiction_NoConclusion(t *testing.T) {
	// 没有明确结论标记的回复
	reply := "Go 语言是一种很好的编程语言，适合做后端服务。"
	result := detectReasoningContradiction(reply)
	if result.HasContradiction {
		t.Error("should not flag when there's no conclusion marker")
	}
}

func TestExtractActionAdvice(t *testing.T) {
	text := "你当然开车去，因为需要把车带过去。建议走路去更方便。"
	actions := extractActionAdvice(text)
	if len(actions) < 2 {
		t.Errorf("expected at least 2 actions, got %d: %v", len(actions), actions)
	}
}

func TestAreContradictory(t *testing.T) {
	if !areContradictory("开车去", "走路去") {
		t.Error("开车 and 走路 should be contradictory")
	}
	if !areContradictory("步行过去", "开车去") {
		t.Error("步行 and 开车 should be contradictory")
	}
	if areContradictory("开车去", "开车去") {
		t.Error("same action should not be contradictory")
	}
	if areContradictory("吃饭", "睡觉") {
		t.Error("unrelated actions should not be contradictory")
	}
}
