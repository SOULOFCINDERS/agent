package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
)

// RunCLI 从命令行运行 benchmark 并输出报告
func RunCLI(systemPrompt string, outputJSON string) {
	runner := NewRunner(systemPrompt, true)
	cases := DefaultTestSuite()

	fmt.Printf("\n🚀 Agent Framework Benchmark\n")
	fmt.Printf("   %d 个测试用例, 5 个评测维度\n\n", len(cases))

	report := runner.RunAll(cases)
	PrintReport(report)

	if outputJSON != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "JSON 序列化失败: %v\n", err)
			return
		}
		if err := os.WriteFile(outputJSON, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "写入文件失败: %v\n", err)
			return
		}
		fmt.Printf("  📄 JSON 报告已保存至: %s\n\n", outputJSON)
	}
}

// RunBFCLCLI 从命令行运行 BFCL 评测
// mode: "mock" 使用 Mock LLM 做回归测试; "live" 使用真实 LLM
func RunBFCLCLI(bfclPath string, mode string, systemPrompt string, maxCases int) {
	fmt.Printf("\n🦍 BFCL (Berkeley Function Calling Leaderboard) Evaluation\n")
	fmt.Printf("   文件: %s\n", bfclPath)
	fmt.Printf("   模式: %s\n\n", mode)

	if mode == "mock" {
		// Mock 模式: 转换为 TestCase 后用内建 Runner 跑
		cases, err := ConvertBFCLFile(bfclPath, BFCLConvertOptions{
			Mode:     "mock",
			MaxCases: maxCases,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "转换 BFCL 数据失败: %v\n", err)
			return
		}
		fmt.Printf("   已转换 %d 个 BFCL 用例\n\n", len(cases))

		runner := NewRunner(systemPrompt, true)
		report := runner.RunAll(cases)
		PrintReport(report)
	} else {
		fmt.Println("   ⚠️  Live 模式需要配置真实 LLM Client")
		fmt.Println("   请在代码中创建 BFCLLiveRunner 并传入 LLM Client")
		fmt.Println("   示例:")
		fmt.Println("     client := llm.NewOpenAICompatClient(baseURL, apiKey, model)")
		fmt.Println("     runner := benchmark.NewBFCLLiveRunner(client, systemPrompt, true)")
		fmt.Println("     report, _ := runner.RunBFCL(bfclPath, maxCases)")
		fmt.Println("     benchmark.PrintBFCLReport(report)")
	}
}

// ExportDeepEvalCLI 导出 Benchmark 结果为 DeepEval 格式
func ExportDeepEvalCLI(systemPrompt string, outputDir string) {
	runner := NewRunner(systemPrompt, false)
	cases := DefaultTestSuite()
	report := runner.RunAll(cases)

	path, err := ExportForDeepEval(report, cases, outputDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "导出失败: %v\n", err)
		return
	}
	fmt.Printf("✅ DeepEval 输入数据已导出至: %s\n", path)
	fmt.Println()
	fmt.Println("下一步:")
	fmt.Println("  python scripts/deepeval/run_deepeval.py \\")
	fmt.Printf("    --input %s \\\n", path)
	fmt.Printf("    --output %s/deepeval_results.json\n", outputDir)
}

// ImportDeepEvalCLI 导入 DeepEval 结果并与 Benchmark 对比
func ImportDeepEvalCLI(systemPrompt string, deepevalResultPath string) {
	// 运行内建 Benchmark
	runner := NewRunner(systemPrompt, false)
	cases := DefaultTestSuite()
	ourReport := runner.RunAll(cases)

	// 导入 DeepEval 结果
	theirReport, err := ImportDeepEvalResults(deepevalResultPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "导入 DeepEval 结果失败: %v\n", err)
		return
	}

	// 打印两者报告
	PrintReport(ourReport)
	PrintDeepEvalReport(theirReport)
	CompareWithDeepEval(ourReport, theirReport)
}
