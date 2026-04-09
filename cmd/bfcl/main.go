package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/SOULOFCINDERS/agent/internal/benchmark"
	"github.com/SOULOFCINDERS/agent/internal/llm"
)

func main() {
	bfclFile := flag.String("file", "testdata/bfcl/BFCL_v3_exec_simple.json", "BFCL JSONL 文件路径")
	maxCases := flag.Int("max", 20, "最多评测多少条 (0=全部)")
	baseURL := flag.String("url", "", "LLM API Base URL")
	apiKey := flag.String("key", "", "LLM API Key")
	model := flag.String("model", "", "LLM Model")
	verbose := flag.Bool("v", true, "显示详细输出")
	jsonOut := flag.String("json", "", "输出 JSON 报告到文件")
	flag.Parse()

	client := llm.NewOpenAICompatClient(*baseURL, *apiKey, *model)
	fmt.Printf("LLM: %s / %s\n", client.BaseURL, client.Model)

	systemPrompt := `You are a helpful assistant. When the user asks something that can be solved by calling a provided function, use function calling with the correct parameters.`

	runner := benchmark.NewBFCLLiveRunner(client, systemPrompt, *verbose)

	fmt.Printf("\n🦍 BFCL Live Evaluation\n")
	fmt.Printf("   File: %s\n", *bfclFile)
	fmt.Printf("   Max:  %d cases\n\n", *maxCases)

	report, err := runner.RunBFCL(*bfclFile, *maxCases)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	report.Category = *bfclFile
	benchmark.PrintBFCLReport(report)

	if *jsonOut != "" {
		data, _ := json.MarshalIndent(report, "", "  ")
		os.WriteFile(*jsonOut, data, 0644)
		fmt.Printf("📄 JSON report saved to: %s\n", *jsonOut)
	}
}
