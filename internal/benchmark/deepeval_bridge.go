package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ============================================================
// DeepEval Bridge — 将 Agent 输出导出为 DeepEval 可消费的 JSON
//
// 架构:
//   Go Agent → 产出 (input, output, context) → 导出 JSON
//   Python DeepEval → 读取 JSON → 运行 Faithfulness/Hallucination 指标
//   → 输出分数 → Go 读取 & 对比
//
// 这是一个桥接层，不依赖 Python 运行时。
// Python 侧的 DeepEval 脚本独立运行。
// ============================================================

// DeepEvalTestCase DeepEval 评测用例格式
type DeepEvalTestCase struct {
	ID               string   `json:"id"`
	Input            string   `json:"input"`              // 用户输入
	ActualOutput     string   `json:"actual_output"`      // Agent 实际输出
	ExpectedOutput   string   `json:"expected_output,omitempty"` // 期望输出（可选）
	Context          []string `json:"context,omitempty"`  // 检索到的上下文（工具结果等）
	RetrievalContext []string `json:"retrieval_context,omitempty"` // RAG 检索上下文
}

// DeepEvalExport 导出数据集
type DeepEvalExport struct {
	AgentName  string             `json:"agent_name"`
	Version    string             `json:"version"`
	TestCases  []DeepEvalTestCase `json:"test_cases"`
}

// DeepEvalResult DeepEval 评测结果（从 Python 侧导入）
type DeepEvalResult struct {
	ID          string  `json:"id"`
	Metric      string  `json:"metric"`       // "faithfulness" / "hallucination" / "answer_relevancy"
	Score       float64 `json:"score"`         // 0.0 ~ 1.0
	Reason      string  `json:"reason"`
	Passed      bool    `json:"passed"`
}

// DeepEvalReport DeepEval 完整报告
type DeepEvalReport struct {
	Results       []DeepEvalResult `json:"results"`
	TotalCases    int              `json:"total_cases"`
	AvgFaithful   float64          `json:"avg_faithfulness"`
	AvgHalluci    float64          `json:"avg_hallucination"`
	AvgRelevancy  float64          `json:"avg_answer_relevancy"`
}

// ExportForDeepEval 将 BenchmarkReport 的结果导出为 DeepEval 格式
func ExportForDeepEval(report *BenchmarkReport, cases []TestCase, outputDir string) (string, error) {
	export := DeepEvalExport{
		AgentName: "SOULOFCINDERS Agent",
		Version:   "1.0",
	}

	// 将测试结果转为 DeepEval 用例
	for i, result := range report.Results {
		if i >= len(cases) {
			break
		}
		tc := cases[i]

		deCase := DeepEvalTestCase{
			ID:           result.CaseID,
			Input:        tc.UserMessage,
			ActualOutput: extractReplyFromResult(result),
		}

		// 从 history 中提取工具返回的上下文
		for _, msg := range tc.History {
			if msg.Role == "tool" && msg.Content != "" {
				deCase.Context = append(deCase.Context, msg.Content)
			}
		}

		export.TestCases = append(export.TestCases, deCase)
	}

	// 写入 JSON 文件
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	outPath := filepath.Join(outputDir, "deepeval_input.json")
	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	return outPath, nil
}

// ImportDeepEvalResults 导入 DeepEval Python 脚本生成的结果
func ImportDeepEvalResults(path string) (*DeepEvalReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var results []DeepEvalResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	report := &DeepEvalReport{
		Results:    results,
		TotalCases: len(results),
	}

	// 计算各指标平均分
	var faithSum, halluciSum, relevSum float64
	var faithN, halluciN, relevN int
	for _, r := range results {
		switch r.Metric {
		case "faithfulness":
			faithSum += r.Score
			faithN++
		case "hallucination":
			halluciSum += r.Score
			halluciN++
		case "answer_relevancy":
			relevSum += r.Score
			relevN++
		}
	}
	if faithN > 0 {
		report.AvgFaithful = faithSum / float64(faithN)
	}
	if halluciN > 0 {
		report.AvgHalluci = halluciSum / float64(halluciN)
	}
	if relevN > 0 {
		report.AvgRelevancy = relevSum / float64(relevN)
	}

	return report, nil
}

// CompareWithDeepEval 对比我们的 Benchmark 和 DeepEval 的结果
func CompareWithDeepEval(ours *BenchmarkReport, theirs *DeepEvalReport) {
	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════════════════════╗")
	fmt.Println("║          Benchmark vs DeepEval 对照报告                   ║")
	fmt.Println("╠═══════════════════════════════════════════════════════════╣")
	fmt.Println("║                                                           ║")
	fmt.Println("║  指标                  内建 Benchmark    DeepEval          ║")
	fmt.Println("║  ──────────────────    ──────────────    ────────          ║")

	// 幻觉防线 vs DeepEval Hallucination
	ourHallu := float64(0)
	if ds, ok := ours.Dimensions[DimHallucination]; ok {
		ourHallu = ds.Score
	}
	fmt.Printf("║  幻觉防线分数           %6.1f%%           %6.1f%%         ║\n",
		ourHallu, (1-theirs.AvgHalluci)*100) // DeepEval hallucination 越低越好
	fmt.Printf("║  忠实度                  N/A              %6.1f%%         ║\n",
		theirs.AvgFaithful*100)
	fmt.Printf("║  回答相关性              N/A              %6.1f%%         ║\n",
		theirs.AvgRelevancy*100)
	fmt.Printf("║  总分                  %6.1f             N/A             ║\n", ours.OverallScore)

	fmt.Println("║                                                           ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════╝")
	fmt.Println()

	// 逐项对比
	if theirs.AvgHalluci > 0.3 && ourHallu > 80 {
		fmt.Println("⚠️  注意: DeepEval 检测到较高幻觉率，但内建 Benchmark 评分较高。")
		fmt.Println("   可能原因: Mock 测试无法覆盖真实 LLM 的幻觉模式。")
		fmt.Println("   建议: 用真实 LLM 重跑 BFCL Live 评测。")
	}
}

// extractReplyFromResult 从测试结果中提取回复文本
func extractReplyFromResult(result TestResult) string {
	for _, detail := range result.Details {
		if detail.Assertion.Type == AssertContains ||
			detail.Assertion.Type == AssertNotContains ||
			detail.Assertion.Type == AssertMatchesRegex {
			if detail.Actual != "" {
				return detail.Actual
			}
		}
	}
	return "[no reply captured]"
}

// PrintDeepEvalReport 打印 DeepEval 结果
func PrintDeepEvalReport(report *DeepEvalReport) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║              DeepEval Evaluation Report             ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  Total Results     : %-30d ║\n", report.TotalCases)
	fmt.Printf("║  Avg Faithfulness  : %-29.1f%% ║\n", report.AvgFaithful*100)
	fmt.Printf("║  Avg Hallucination : %-29.1f%% ║\n", report.AvgHalluci*100)
	fmt.Printf("║  Avg Relevancy     : %-29.1f%% ║\n", report.AvgRelevancy*100)
	fmt.Println("╚══════════════════════════════════════════════════════╝")
}
