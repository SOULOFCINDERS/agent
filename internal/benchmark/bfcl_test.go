package benchmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// Source: bfcl_converter_test.go
// ============================================================

func TestParseBFCLQuestion_Nested(t *testing.T) {
	// [[{role: "user", content: "..."}]]
	raw := json.RawMessage(`[[{"role":"user","content":"Find the area of a triangle"}]]`)
	got, err := parseBFCLQuestion(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Find the area of a triangle" {
		t.Errorf("got %q", got)
	}
}

func TestParseBFCLQuestion_String(t *testing.T) {
	raw := json.RawMessage(`"What is 2+2?"`)
	got, err := parseBFCLQuestion(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != "What is 2+2?" {
		t.Errorf("got %q", got)
	}
}

func TestParseBFCLFunctions(t *testing.T) {
	raw := json.RawMessage(`[{"name":"calc_area","description":"Calculate area","parameters":{"type":"dict","properties":{"base":{"type":"integer","description":"The base."}},"required":["base"]}}]`)
	defs, names, err := parseBFCLFunctions(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || names[0] != "calc_area" {
		t.Errorf("unexpected: defs=%d, names=%v", len(defs), names)
	}
	// 验证 dict → object 转换
	var params map[string]interface{}
	json.Unmarshal(defs[0].Function.Parameters, &params)
	if params["type"] != "object" {
		t.Errorf("type should be 'object', got %q", params["type"])
	}
}

func TestParseBFCLGroundTruth(t *testing.T) {
	gts := []string{"calc_binomial_probability(n=20, k=5, p=0.6)"}
	calls := parseBFCLGroundTruth(gts)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].FuncName != "calc_binomial_probability" {
		t.Errorf("func name: %q", calls[0].FuncName)
	}
	if calls[0].Args["n"] != "20" || calls[0].Args["k"] != "5" || calls[0].Args["p"] != "0.6" {
		t.Errorf("args: %v", calls[0].Args)
	}
}

func TestParseBFCLGroundTruth_NestedArgs(t *testing.T) {
	gts := []string{`calculate_cosine_similarity(vectorA=[0.5, 0.7], vectorB=[0.4, 0.6])`}
	calls := parseBFCLGroundTruth(gts)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].FuncName != "calculate_cosine_similarity" {
		t.Errorf("func name: %q", calls[0].FuncName)
	}
	if calls[0].Args["vectorA"] != "[0.5, 0.7]" {
		t.Errorf("vectorA: %q", calls[0].Args["vectorA"])
	}
}

func TestConvertBFCLEntry_Simple(t *testing.T) {
	entry := BFCLEntry{
		ID:       "simple_0",
		Question: json.RawMessage(`[[{"role":"user","content":"Find the area of a triangle with base 10 and height 5."}]]`),
		Function: json.RawMessage(`[{"name":"calculate_triangle_area","description":"Calculate triangle area","parameters":{"type":"dict","properties":{"base":{"type":"integer","description":"base"},"height":{"type":"integer","description":"height"}},"required":["base","height"]}}]`),
	}

	tc, err := convertBFCLEntry(entry, BFCLConvertOptions{Mode: "mock"})
	if err != nil {
		t.Fatal(err)
	}

	if tc.ID != "BFCL-simple_0" {
		t.Errorf("ID: %q", tc.ID)
	}
	if len(tc.ToolDefs) != 1 {
		t.Errorf("ToolDefs: %d", len(tc.ToolDefs))
	}
	if len(tc.MockResponses) == 0 {
		t.Error("expected MockResponses in mock mode")
	}
	if tc.Dimension != DimToolUse {
		t.Errorf("dimension: %q", tc.Dimension)
	}
}

func TestConvertBFCLEntry_WithGroundTruth(t *testing.T) {
	entry := BFCLEntry{
		ID:          "exec_simple_0",
		Question:    json.RawMessage(`[[{"role":"user","content":"Roll a die 20 times, what are the odds of exactly 5 sixes?"}]]`),
		Function:    json.RawMessage(`[{"name":"calc_binomial_probability","description":"Calculates probability","parameters":{"type":"dict","properties":{"n":{"type":"integer","description":"trials"},"k":{"type":"integer","description":"successes"},"p":{"type":"float","description":"probability"}},"required":["n","k","p"]}}]`),
		GroundTruth: []string{"calc_binomial_probability(n=20, k=5, p=0.6)"},
	}

	tc, err := convertBFCLEntry(entry, BFCLConvertOptions{Mode: "live"})
	if err != nil {
		t.Fatal(err)
	}

	// live 模式不应有 MockResponses
	if len(tc.MockResponses) > 0 {
		t.Error("live mode should not have MockResponses")
	}

	// 应有 tool_called 断言
	found := false
	for _, a := range tc.Assertions {
		if a.Type == AssertToolCalled && a.Target == "calc_binomial_probability" {
			found = true
		}
	}
	if !found {
		t.Error("missing AssertToolCalled for calc_binomial_probability")
	}
}

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"n=20, k=5, p=0.6", 3},
		{`vectorA=[0.5, 0.7], vectorB=[0.4, 0.6]`, 2},
		{`name="hello, world", age=25`, 2},
	}
	for _, tt := range tests {
		got := splitArgs(tt.input)
		if len(got) != tt.expected {
			t.Errorf("splitArgs(%q): got %d parts, want %d: %v", tt.input, len(got), tt.expected, got)
		}
	}
}

func TestInferBFCLCategory(t *testing.T) {
	tests := map[string]string{
		"simple_0":            "simple",
		"exec_simple_3":       "exec_simple",
		"multiple_12":         "multiple",
		"multi_turn_base_5":   "multi_turn",
		"live_simple_0":       "live",
	}
	for id, want := range tests {
		got := inferBFCLCategory(id)
		if got != want {
			t.Errorf("inferBFCLCategory(%q) = %q, want %q", id, got, want)
		}
	}
}
// ============================================================
// Source: bfcl_compare_test.go
// ============================================================

func TestSemanticEqual_Numbers(t *testing.T) {
	tests := []struct {
		expected string
		actual   interface{}
		want     bool
	}{
		{"10", float64(10), true},
		{"10", float64(10.0), true},
		{"10.0", float64(10), true},
		{"0.6", float64(0.6), true},
		{"50.0", float64(50), true},
		{"15", float64(15.5), false},
	}
	for _, tt := range tests {
		got := semanticEqual(tt.expected, tt.actual)
		if got != tt.want {
			t.Errorf("semanticEqual(%q, %v) = %v, want %v", tt.expected, tt.actual, got, tt.want)
		}
	}
}

func TestSemanticEqual_Arrays(t *testing.T) {
	// JSON 反序列化后 [0.5, 0.7] → []interface{}{0.5, 0.7}
	actual := []interface{}{0.5, 0.7, 0.2, 0.9, 0.1}
	if !semanticEqual("[0.5, 0.7, 0.2, 0.9, 0.1]", actual) {
		t.Error("array should match")
	}
	if semanticEqual("[0.5, 0.7, 0.2, 0.9, 0.3]", actual) {
		t.Error("different array should not match")
	}
}

func TestSemanticEqual_Strings(t *testing.T) {
	if !semanticEqual("'hello'", "hello") {
		t.Error("quoted string should match")
	}
	if !semanticEqual("\"AAPL\"", "AAPL") {
		t.Error("double-quoted string should match")
	}
}

func TestSemanticEqual_Booleans(t *testing.T) {
	if !semanticEqual("True", true) {
		t.Error("True should match true")
	}
	if !semanticEqual("False", false) {
		t.Error("False should match false")
	}
}

func TestSemanticEqual_Tuples(t *testing.T) {
	// Python tuple → JSON array
	actual := []interface{}{45.76, 4.85}
	if !semanticEqual("(45.76, 4.85)", actual) {
		t.Error("tuple (45.76, 4.85) should match array [45.76, 4.85]")
	}

	actual2 := []interface{}{32.71, -117.16}
	if !semanticEqual("(32.71, -117.16)", actual2) {
		t.Error("tuple (32.71, -117.16) should match array [32.71, -117.16]")
	}

	// 不同值应该不匹配
	if semanticEqual("(1.0, 2.0)", []interface{}{1.0, 3.0}) {
		t.Error("different tuple should not match")
	}
}

func TestSemanticEqual_Lambda(t *testing.T) {
	// 同变量名
	if !semanticEqual("lambda x: x+1", "lambda x: x+1") {
		t.Error("identical lambda should match")
	}

	// 不同变量名，同结构
	if !semanticEqual("lambda x: x+1", "lambda t: t+1") {
		t.Error("lambda x: x+1 should match lambda t: t+1 (alpha equivalence)")
	}

	// 不同变量名，复杂表达式
	if !semanticEqual("lambda x: x**2 + x", "lambda t: t**2 + t") {
		t.Error("lambda x: x**2 + x should match lambda t: t**2 + t")
	}

	// 不同结构不应匹配
	if semanticEqual("lambda x: x+1", "lambda x: x+2") {
		t.Error("structurally different lambda should not match")
	}

	// 多参数不同变量
	if !semanticEqual("lambda x: x * 2", "lambda y: y * 2") {
		t.Error("lambda x: x * 2 should match lambda y: y * 2")
	}
}

func TestCompareLambda(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"lambda x: x+1", "lambda x: x+1", true},
		{"lambda x: x+1", "lambda t: t+1", true},
		{"lambda x: x**2", "lambda y: y**2", true},
		{"lambda x: x+1", "lambda x: x+2", false},
		{"lambda x: x*x+x", "lambda t: t*t+t", true},
	}
	for _, tt := range tests {
		got := compareLambda(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareLambda(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseBFCLGroundTruth_ListArgs(t *testing.T) {
	gts := []string{"calculate_cosine_similarity(vectorA=[0.5, 0.7, 0.2], vectorB=[0.4, 0.6, 0.3])"}
	calls := parseBFCLGroundTruth(gts)
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Args["vectorA"] != "[0.5, 0.7, 0.2]" {
		t.Errorf("vectorA: %q", calls[0].Args["vectorA"])
	}
	if calls[0].Args["vectorB"] != "[0.4, 0.6, 0.3]" {
		t.Errorf("vectorB: %q", calls[0].Args["vectorB"])
	}
}
// ============================================================
// Source: bfcl_integration_test.go
// ============================================================

// TestBFCLConvertFromSampleData 使用内嵌的 BFCL 样例数据测试端到端转换
func TestBFCLConvertFromSampleData(t *testing.T) {
	// 模拟 BFCL JSONL 数据（3条样例，来自 BFCL_v3_simple.json 和 BFCL_v3_exec_simple.json）
	sampleData := []string{
		`{"id":"simple_0","question":[[{"role":"user","content":"Find the area of a triangle with a base of 10 units and height of 5 units."}]],"function":[{"name":"calculate_triangle_area","description":"Calculate the area of a triangle.","parameters":{"type":"dict","properties":{"base":{"type":"integer","description":"The base."},"height":{"type":"integer","description":"The height."}},"required":["base","height"]}}]}`,
		`{"id":"simple_1","question":[[{"role":"user","content":"Calculate the factorial of 5 using math functions."}]],"function":[{"name":"math.factorial","description":"Calculate the factorial.","parameters":{"type":"dict","properties":{"number":{"type":"integer","description":"The number."}},"required":["number"]}}]}`,
		`{"id":"exec_simple_0","question":[[{"role":"user","content":"Roll a die 20 times, probability of exactly 5 sixes with p=0.6?"}]],"function":[{"name":"calc_binomial_probability","description":"Calculates probability.","parameters":{"type":"dict","properties":{"n":{"type":"integer","description":"trials"},"k":{"type":"integer","description":"successes"},"p":{"type":"float","description":"probability"}},"required":["n","k","p"]}}],"execution_result_type":["exact_match"],"ground_truth":["calc_binomial_probability(n=20, k=5, p=0.6)"]}`,
	}

	// 写入临时文件
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "bfcl_sample.jsonl")
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range sampleData {
		f.WriteString(line + "\n")
	}
	f.Close()

	// Mock 模式转换
	t.Run("MockMode", func(t *testing.T) {
		cases, err := ConvertBFCLFile(tmpFile, BFCLConvertOptions{
			Mode:     "mock",
			MaxCases: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(cases) != 3 {
			t.Fatalf("expected 3 cases, got %d", len(cases))
		}

		// 验证第一个 case
		tc := cases[0]
		if tc.ID != "BFCL-simple_0" {
			t.Errorf("ID: %q", tc.ID)
		}
		if tc.Dimension != DimToolUse {
			t.Errorf("dimension: %q", tc.Dimension)
		}
		if len(tc.ToolDefs) != 1 {
			t.Errorf("ToolDefs: %d", len(tc.ToolDefs))
		}
		if len(tc.MockResponses) == 0 {
			t.Error("expected MockResponses")
		}
		if len(tc.Assertions) == 0 {
			t.Error("expected Assertions")
		}

		// 验证有 ground_truth 的 case
		tc3 := cases[2]
		if tc3.ID != "BFCL-exec_simple_0" {
			t.Errorf("case 3 ID: %q", tc3.ID)
		}
		foundAssertion := false
		for _, a := range tc3.Assertions {
			if a.Type == AssertToolCalled && a.Target == "calc_binomial_probability" {
				foundAssertion = true
			}
		}
		if !foundAssertion {
			t.Error("case 3 missing AssertToolCalled for calc_binomial_probability")
		}
	})

	// Live 模式转换
	t.Run("LiveMode", func(t *testing.T) {
		cases, err := ConvertBFCLFile(tmpFile, BFCLConvertOptions{
			Mode: "live",
		})
		if err != nil {
			t.Fatal(err)
		}
		// Live 模式不应有 MockResponses
		for _, tc := range cases {
			if len(tc.MockResponses) > 0 {
				t.Errorf("case %s should not have MockResponses in live mode", tc.ID)
			}
		}
	})

	// 用内建 Runner 跑 mock 模式的 BFCL 用例
	t.Run("RunWithMockRunner", func(t *testing.T) {
		cases, _ := ConvertBFCLFile(tmpFile, BFCLConvertOptions{Mode: "mock"})
		runner := NewRunner("You are a helpful assistant.", true)
		report := runner.RunAll(cases)

		if report.TotalCases != 3 {
			t.Errorf("total cases: %d", report.TotalCases)
		}
		// Mock 模式下所有都应该通过
		if report.PassedCases != 3 {
			t.Errorf("passed: %d/3", report.PassedCases)
			for _, r := range report.Results {
				if !r.Passed {
					t.Logf("  FAIL: %s — %v", r.CaseID, r.Details)
				}
			}
		}
	})
}

// TestDeepEvalExport 测试 DeepEval 数据导出
func TestDeepEvalExport(t *testing.T) {
	runner := NewRunner("test", false)
	cases := DefaultTestSuite()
	report := runner.RunAll(cases)

	tmpDir := t.TempDir()
	path, err := ExportForDeepEval(report, cases, tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// 验证文件存在且格式正确
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var export DeepEvalExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if export.AgentName == "" {
		t.Error("AgentName empty")
	}
	if len(export.TestCases) == 0 {
		t.Error("no test cases exported")
	}

	// 验证每个导出的 case 都有 input
	for _, tc := range export.TestCases {
		if tc.Input == "" {
			t.Errorf("case %s has empty input", tc.ID)
		}
	}

	t.Logf("Exported %d cases to %s", len(export.TestCases), path)
}

// TestDeepEvalImport 测试 DeepEval 结果导入
func TestDeepEvalImport(t *testing.T) {
	// 模拟 DeepEval 结果
	results := []DeepEvalResult{
		{ID: "H-PS-01", Metric: "faithfulness", Score: 0.9, Passed: true},
		{ID: "H-PS-01", Metric: "hallucination", Score: 0.1, Passed: true},
		{ID: "H-KG-01", Metric: "faithfulness", Score: 0.8, Passed: true},
		{ID: "H-KG-01", Metric: "hallucination", Score: 0.2, Passed: true},
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "deepeval_results.json")
	data, _ := json.Marshal(results)
	os.WriteFile(tmpFile, data, 0644)

	report, err := ImportDeepEvalResults(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalCases != 4 {
		t.Errorf("total: %d", report.TotalCases)
	}
	if report.AvgFaithful < 0.8 {
		t.Errorf("avg faithfulness: %.2f", report.AvgFaithful)
	}
	if report.AvgHalluci > 0.2 {
		t.Errorf("avg hallucination: %.2f (should be low)", report.AvgHalluci)
	}
}
