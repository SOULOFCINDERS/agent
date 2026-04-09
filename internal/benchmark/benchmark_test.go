package benchmark

import (
	"fmt"
	"testing"
)

const defaultSystemPrompt = `你是一个有帮助的AI助手。

回答流程（思维链）：
1. 拆解问题：明确用户真正需要什么
2. 逐步推理：先分析条件和约束，再得出结论
3. 给出答案：确保结论与推理过程一致

引用规则（严格遵守）：
1. 如果你不确定某个事实，请使用搜索工具验证
2. 不要编造 URL、引用来源或名人语录
3. 数学计算必须使用 calc 工具，不要心算
4. 如果没有足够信息回答，坦诚说明`

func TestFullBenchmark(t *testing.T) {
	runner := NewRunner(defaultSystemPrompt, true)
	cases := DefaultTestSuite()

	fmt.Printf("\n🚀 开始 Agent Framework Benchmark (%d 个用例)\n\n", len(cases))

	report := runner.RunAll(cases)
	PrintReport(report)

	// 基线要求: 总分 >= 60
	if report.OverallScore < 60 {
		t.Errorf("总分 %.1f 低于基线 60 分", report.OverallScore)
	}
}

func TestDimensionHallucination(t *testing.T) {
	runner := NewRunner(defaultSystemPrompt, true)
	cases := hallucinationCases()

	fmt.Printf("\n🛡️ 幻觉防线评测 (%d 个用例)\n\n", len(cases))

	report := runner.RunAll(cases)
	PrintReport(report)
}

func TestDimensionToolUse(t *testing.T) {
	runner := NewRunner(defaultSystemPrompt, true)
	cases := toolUseCases()

	fmt.Printf("\n🔧 工具使用评测 (%d 个用例)\n\n", len(cases))

	report := runner.RunAll(cases)
	PrintReport(report)
}

func TestDimensionReasoning(t *testing.T) {
	runner := NewRunner(defaultSystemPrompt, true)
	cases := reasoningCases()

	fmt.Printf("\n🧠 推理质量评测 (%d 个用例)\n\n", len(cases))

	report := runner.RunAll(cases)
	PrintReport(report)
}
