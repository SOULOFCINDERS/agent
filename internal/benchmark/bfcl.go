package benchmark

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ============================================================
// Source: bfcl_converter.go
// ============================================================

// ============================================================
// BFCL (Berkeley Function Calling Leaderboard) 用例转换器
//
// 将 BFCL 的 JSONL 测试数据转换成本框架的 TestCase 格式
// 支持两种模式:
//   1. Mock 模式: 生成含预设响应的 TestCase（用于框架回归测试）
//   2. Live 模式: 生成只含 Assertions 的 TestCase（用于真实 LLM 评测）
// ============================================================

// BFCLEntry BFCL 原始数据格式（JSONL，每行一个）
type BFCLEntry struct {
	ID       string          `json:"id"`
	Question json.RawMessage `json:"question"` // [[{role, content}]] 或 string
	Function json.RawMessage `json:"function"` // [{name, description, parameters}]

	// exec 类型才有的字段
	GroundTruth         []string `json:"ground_truth,omitempty"`
	ExecutionResultType []string `json:"execution_result_type,omitempty"`
}

// BFCLFunction BFCL 函数定义
type BFCLFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// BFCLConvertOptions 转换选项
type BFCLConvertOptions struct {
	// Mode: "mock" 生成 MockResponse + Assertion; "live" 只生成 Assertion
	Mode string
	// MaxCases: 最多转换多少条, 0 = 全部
	MaxCases int
	// Category: 手动指定 BFCL 类别标签 (simple/multiple/parallel/exec_simple 等)
	Category string
}

// ConvertBFCLFile 从 BFCL JSONL 文件读取并转换为 TestCase 列表
func ConvertBFCLFile(path string, opts BFCLConvertOptions) ([]TestCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open BFCL file: %w", err)
	}
	defer f.Close()

	var entries []BFCLEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry BFCLEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parse BFCL entry: %w (line: %s)", err, truncateLine(line, 200))
		}
		entries = append(entries, entry)
		if opts.MaxCases > 0 && len(entries) >= opts.MaxCases {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan BFCL file: %w", err)
	}

	var cases []TestCase
	for _, entry := range entries {
		tc, err := convertBFCLEntry(entry, opts)
		if err != nil {
			// 跳过格式异常的条目，打印警告
			fmt.Fprintf(os.Stderr, "WARN: skip entry %s: %v\n", entry.ID, err)
			continue
		}
		cases = append(cases, tc)
	}
	return cases, nil
}

// ConvertBFCLEntries 直接从 BFCLEntry 列表转换
func ConvertBFCLEntries(entries []BFCLEntry, opts BFCLConvertOptions) ([]TestCase, error) {
	var cases []TestCase
	for _, entry := range entries {
		tc, err := convertBFCLEntry(entry, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: skip entry %s: %v\n", entry.ID, err)
			continue
		}
		cases = append(cases, tc)
	}
	return cases, nil
}

func convertBFCLEntry(entry BFCLEntry, opts BFCLConvertOptions) (TestCase, error) {
	// 1. 解析用户消息
	userMsg, err := parseBFCLQuestion(entry.Question)
	if err != nil {
		return TestCase{}, fmt.Errorf("parse question: %w", err)
	}

	// 2. 解析函数定义 → 转换为 llm.ToolDef
	toolDefs, funcNames, err := parseBFCLFunctions(entry.Function)
	if err != nil {
		return TestCase{}, fmt.Errorf("parse functions: %w", err)
	}

	// 3. 解析 ground_truth → 提取期望调用的函数名和参数
	expectedCalls := parseBFCLGroundTruth(entry.GroundTruth)

	// 4. 确定子类别
	subCat := opts.Category
	if subCat == "" {
		subCat = inferBFCLCategory(entry.ID)
	}

	tc := TestCase{
		ID:          fmt.Sprintf("BFCL-%s", entry.ID),
		Name:        fmt.Sprintf("BFCL: %s", truncateLine(userMsg, 60)),
		Description: fmt.Sprintf("BFCL 用例 %s，可用函数: %s", entry.ID, strings.Join(funcNames, ", ")),
		Dimension:   DimToolUse,
		SubCategory: fmt.Sprintf("bfcl_%s", subCat),
		UserMessage: userMsg,
		ToolDefs:    toolDefs,
	}

	// 5. 生成断言
	if len(expectedCalls) > 0 {
		// 有 ground_truth: 断言必须调用指定的函数
		for _, ec := range expectedCalls {
			tc.Assertions = append(tc.Assertions, Assertion{
				Type:     AssertToolCalled,
				Target:   ec.FuncName,
				Weight:   1.0,
				Critical: true,
			})
		}
	} else {
		// 无 ground_truth: 至少应调用某个可用函数
		if len(funcNames) == 1 {
			tc.Assertions = append(tc.Assertions, Assertion{
				Type:     AssertToolCalled,
				Target:   funcNames[0],
				Weight:   1.0,
				Critical: true,
			})
		}
	}

	// 6. 如果是 mock 模式，生成预设响应
	if opts.Mode == "mock" {
		tc.MockResponses = generateMockResponses(expectedCalls, funcNames, userMsg)
	}

	return tc, nil
}

// ---- 解析 BFCL 数据格式 ----

// parseBFCLQuestion 从 BFCL 的 question 字段解析用户消息
// BFCL 格式: [[{role: "user", content: "..."}]] (嵌套列表) 或直接 string
func parseBFCLQuestion(raw json.RawMessage) (string, error) {
	// 尝试方式1: 直接 string
	var strQ string
	if err := json.Unmarshal(raw, &strQ); err == nil {
		return strQ, nil
	}

	// 尝试方式2: [[{role, content}]]
	var nested [][]struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &nested); err == nil {
		if len(nested) > 0 && len(nested[0]) > 0 {
			// 取最后一轮的用户消息
			for i := len(nested[0]) - 1; i >= 0; i-- {
				if nested[0][i].Role == "user" {
					return nested[0][i].Content, nil
				}
			}
			return nested[0][0].Content, nil
		}
	}

	// 尝试方式3: [{role, content}]
	var flat []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &flat); err == nil && len(flat) > 0 {
		for i := len(flat) - 1; i >= 0; i-- {
			if flat[i].Role == "user" {
				return flat[i].Content, nil
			}
		}
		return flat[0].Content, nil
	}

	return "", fmt.Errorf("unsupported question format: %s", truncateLine(string(raw), 200))
}

// parseBFCLFunctions 解析 BFCL 函数定义 → llm.ToolDef
func parseBFCLFunctions(raw json.RawMessage) ([]llm.ToolDef, []string, error) {
	var funcs []BFCLFunction
	if err := json.Unmarshal(raw, &funcs); err != nil {
		return nil, nil, fmt.Errorf("unmarshal functions: %w", err)
	}

	var toolDefs []llm.ToolDef
	var names []string
	for _, f := range funcs {
		params := convertBFCLParams(f.Parameters)
		paramsJSON, _ := json.Marshal(params)

		toolDefs = append(toolDefs, llm.ToolDef{
			Type: "function",
			Function: llm.FuncDef{
				Name:        f.Name,
				Description: f.Description,
				Parameters:  paramsJSON,
			},
		})
		names = append(names, f.Name)
	}
	return toolDefs, names, nil
}

// convertBFCLParams 将 BFCL 的 {type: "dict", properties, required} 转为标准 JSON Schema
func convertBFCLParams(params map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range params {
		result[k] = v
	}
	// BFCL 使用 "dict" 作为类型，转换为标准的 "object"
	if t, ok := result["type"].(string); ok && t == "dict" {
		result["type"] = "object"
	}
	return result
}

// BFCLExpectedCall ground_truth 解析后的期望调用
type BFCLExpectedCall struct {
	FuncName string
	Args     map[string]string // key=value 的原始字符串表示
}

// parseBFCLGroundTruth 解析 ground_truth 字段
// 格式: ["func_name(arg1=val1, arg2=val2)"]
func parseBFCLGroundTruth(groundTruth []string) []BFCLExpectedCall {
	var calls []BFCLExpectedCall
	// 匹配 func_name(...)
	re := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_.]*)\((.*)\)$`)

	for _, gt := range groundTruth {
		gt = strings.TrimSpace(gt)
		matches := re.FindStringSubmatch(gt)
		if matches == nil {
			continue
		}
		call := BFCLExpectedCall{
			FuncName: matches[1],
			Args:     make(map[string]string),
		}
		// 简单解析参数（不处理嵌套括号的极端情况）
		argsStr := matches[2]
		if argsStr != "" {
			for _, pair := range splitArgs(argsStr) {
				pair = strings.TrimSpace(pair)
				if idx := strings.Index(pair, "="); idx > 0 {
					key := strings.TrimSpace(pair[:idx])
					val := strings.TrimSpace(pair[idx+1:])
					call.Args[key] = val
				}
			}
		}
		calls = append(calls, call)
	}
	return calls
}

// splitArgs 按逗号分割参数，但跳过括号/引号内的逗号
func splitArgs(s string) []string {
	var parts []string
	depth := 0
	inStr := false
	strChar := byte(0)
	start := 0

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inStr {
			if ch == strChar && (i == 0 || s[i-1] != '\\') {
				inStr = false
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inStr = true
			strChar = ch
			continue
		}
		if ch == '(' || ch == '[' || ch == '{' {
			depth++
		} else if ch == ')' || ch == ']' || ch == '}' {
			depth--
		} else if ch == ',' && depth == 0 {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

// inferBFCLCategory 从 ID 推断类别
func inferBFCLCategory(id string) string {
	switch {
	case strings.HasPrefix(id, "simple"):
		return "simple"
	case strings.HasPrefix(id, "multiple"):
		return "multiple"
	case strings.HasPrefix(id, "parallel"):
		return "parallel"
	case strings.HasPrefix(id, "exec_simple"):
		return "exec_simple"
	case strings.HasPrefix(id, "exec_multiple"):
		return "exec_multiple"
	case strings.HasPrefix(id, "exec_parallel"):
		return "exec_parallel"
	case strings.HasPrefix(id, "multi_turn"):
		return "multi_turn"
	case strings.HasPrefix(id, "live"):
		return "live"
	default:
		return "unknown"
	}
}

// generateMockResponses 为 Mock 模式生成预设响应
func generateMockResponses(expectedCalls []BFCLExpectedCall, funcNames []string, userMsg string) []MockResponse {
	if len(expectedCalls) > 0 {
		// 有 ground_truth: 模拟 LLM 正确调用
		var toolCalls []llm.ToolCall
		for i, ec := range expectedCalls {
			argsMap := make(map[string]interface{})
			for k, v := range ec.Args {
				argsMap[k] = v
			}
			argsJSON, _ := json.Marshal(argsMap)
			toolCalls = append(toolCalls, llm.ToolCall{
				ID:   fmt.Sprintf("bfcl_tc_%d", i),
				Type: "function",
				Function: llm.FunctionCall{
					Name:      ec.FuncName,
					Arguments: string(argsJSON),
				},
			})
		}
		return []MockResponse{
			{ToolCalls: toolCalls},
			{Content: fmt.Sprintf("[BFCL mock] 已调用 %d 个函数完成任务。", len(toolCalls))},
		}
	}

	// 无 ground_truth: 用第一个可用函数生成模拟调用
	if len(funcNames) > 0 {
		return []MockResponse{
			{ToolCalls: []llm.ToolCall{{
				ID:   "bfcl_tc_0",
				Type: "function",
				Function: llm.FunctionCall{
					Name:      funcNames[0],
					Arguments: "{}",
				},
			}}},
			{Content: "[BFCL mock] 函数调用完成。"},
		}
	}

	return []MockResponse{
		{Content: "[BFCL mock] 无可用函数。"},
	}
}

// ---- BFCL Live Runner (真实 LLM 评测) ----

// BFCLLiveResult 单条 BFCL Live 评测结果
type BFCLLiveResult struct {
	ID             string   `json:"id"`
	Question       string   `json:"question"`
	ExpectedFunc   []string `json:"expected_func"`
	ActualFunc     []string `json:"actual_func"`
	FuncMatch      bool     `json:"func_match"`
	ArgsMatch      bool     `json:"args_match"`
	RawToolCalls   string   `json:"raw_tool_calls,omitempty"`
}

// BFCLLiveReport BFCL Live 评测汇总报告
type BFCLLiveReport struct {
	Total          int     `json:"total"`
	FuncCorrect    int     `json:"func_correct"`
	ArgsCorrect    int     `json:"args_correct"`
	FuncAccuracy   float64 `json:"func_accuracy"`
	ArgsAccuracy   float64 `json:"args_accuracy"`
	Category       string  `json:"category"`
	Results        []BFCLLiveResult `json:"results"`
}

// ---- 辅助 ----

func truncateLine(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
// ============================================================
// Source: bfcl_compare.go
// ============================================================

// ============================================================
// BFCL 参数比较器 — 语义等价比较
//
// 解决的问题:
//   1. "10" vs "10.0" — 数值等价
//   2. "[0.5, 0.7]" vs "[0.5,0.7]" — 列表格式差异
//   3. "'hello'" vs "hello" — 引号差异
//   4. "True" vs "true" — 布尔大小写
//   5. "(45.76, 4.85)" vs [45.76, 4.85] — Python tuple vs JSON array
//   6. "lambda x: x+1" vs "lambda t: t+1" — lambda 变量名等价
// ============================================================

// semanticEqual 语义等价比较两个值
// expectedStr 来自 ground_truth (Python 表达式格式)
// actualVal 来自 JSON 反序列化后的 Go 值
func semanticEqual(expectedStr string, actualVal interface{}) bool {
	expectedStr = strings.TrimSpace(expectedStr)
	expectedStr = strings.Trim(expectedStr, "\"'")

	// 1. 直接字符串匹配
	av := fmt.Sprintf("%v", actualVal)
	if expectedStr == av {
		return true
	}

	// 2. 数值比较 (10 == 10.0)
	if expNum, err := strconv.ParseFloat(expectedStr, 64); err == nil {
		if actNum, ok := toFloat64(actualVal); ok {
			if math.Abs(expNum-actNum) < 1e-9 {
				return true
			}
		}
	}

	// 3. 布尔比较 (True == true)
	expLower := strings.ToLower(expectedStr)
	if expLower == "true" || expLower == "false" {
		if actBool, ok := actualVal.(bool); ok {
			return (expLower == "true") == actBool
		}
	}

	// 4. 列表比较 ([0.5, 0.7] == [0.5,0.7])
	if strings.HasPrefix(expectedStr, "[") && strings.HasSuffix(expectedStr, "]") {
		return compareJSONArrays(expectedStr, actualVal)
	}

	// 5. Python tuple → JSON array: (45.76, 4.85) == [45.76, 4.85]
	if strings.HasPrefix(expectedStr, "(") && strings.HasSuffix(expectedStr, ")") {
		// 转换为 JSON array 格式再比较
		arrayStr := "[" + expectedStr[1:len(expectedStr)-1] + "]"
		return compareJSONArrays(arrayStr, actualVal)
	}

	// 6. Lambda 等价: "lambda x: x+1" == "lambda t: t+1"
	if strings.HasPrefix(expectedStr, "lambda ") {
		if actStr, ok := actualVal.(string); ok && strings.HasPrefix(actStr, "lambda ") {
			return compareLambda(expectedStr, actStr)
		}
	}

	// 7. None == null
	if expLower == "none" && actualVal == nil {
		return true
	}

	// 8. 字符串 vs 字符串（去引号后比较）
	if actStr, ok := actualVal.(string); ok {
		if expectedStr == actStr {
			return true
		}
	}

	return false
}

// compareLambda 比较两个 lambda 表达式是否语义等价
// 例如 "lambda x: x+1" 和 "lambda t: t+1" 是等价的（仅变量名不同）
func compareLambda(a, b string) bool {
	// 快速路径：完全相同
	if a == b {
		return true
	}

	// 提取 lambda 参数和表达式
	aParam, aBody := parseLambdaParts(a)
	bParam, bBody := parseLambdaParts(b)

	if aParam == "" || bParam == "" {
		return false
	}

	// 如果参数名相同，直接比较 body
	if aParam == bParam {
		return normalizeExpr(aBody) == normalizeExpr(bBody)
	}

	// 参数名不同，尝试替换后比较
	// 把 b 的变量名替换为 a 的变量名再比较
	normalizedB := replaceLambdaVar(bBody, bParam, aParam)
	return normalizeExpr(aBody) == normalizeExpr(normalizedB)
}

// parseLambdaParts 解析 "lambda x: x+1" → ("x", "x+1")
func parseLambdaParts(s string) (string, string) {
	s = strings.TrimPrefix(s, "lambda ")
	idx := strings.Index(s, ":")
	if idx < 0 {
		return "", ""
	}
	param := strings.TrimSpace(s[:idx])
	body := strings.TrimSpace(s[idx+1:])
	return param, body
}

// replaceLambdaVar 在表达式中替换变量名（整词替换）
func replaceLambdaVar(body, oldVar, newVar string) string {
	result := ""
	i := 0
	for i < len(body) {
		// 检查是否匹配整个变量名
		if i+len(oldVar) <= len(body) && body[i:i+len(oldVar)] == oldVar {
			// 检查前后是否为非标识符字符
			before := i == 0 || !isIdentChar(body[i-1])
			after := i+len(oldVar) >= len(body) || !isIdentChar(body[i+len(oldVar)])
			if before && after {
				result += newVar
				i += len(oldVar)
				continue
			}
		}
		result += string(body[i])
		i++
	}
	return result
}

// isIdentChar 是否为标识符字符
func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

// normalizeExpr 标准化表达式（去除多余空格）
func normalizeExpr(s string) string {
	s = strings.TrimSpace(s)
	// 压缩连续空格
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

// compareJSONArrays 比较 Python 格式的列表与 JSON 数组
func compareJSONArrays(expectedStr string, actualVal interface{}) bool {
	// 把 Python 列表表示转为 JSON
	jsonStr := expectedStr
	jsonStr = strings.ReplaceAll(jsonStr, "'", "\"")
	jsonStr = strings.ReplaceAll(jsonStr, "True", "true")
	jsonStr = strings.ReplaceAll(jsonStr, "False", "false")
	jsonStr = strings.ReplaceAll(jsonStr, "None", "null")

	var expected interface{}
	if err := json.Unmarshal([]byte(jsonStr), &expected); err != nil {
		return false
	}

	return deepEqual(expected, actualVal)
}

// deepEqual 深度比较两个 JSON 值（数值容差）
func deepEqual(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	switch av := a.(type) {
	case float64:
		if bv, ok := toFloat64(b); ok {
			return math.Abs(av-bv) < 1e-9
		}
	case string:
		if bv, ok := b.(string); ok {
			return av == bv
		}
	case bool:
		if bv, ok := b.(bool); ok {
			return av == bv
		}
	case []interface{}:
		bv, ok := b.([]interface{})
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]interface{}:
		bv, ok := b.(map[string]interface{})
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !deepEqual(v, bv[k]) {
				return false
			}
		}
		return true
	}

	// fallback: 字符串比较
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// toFloat64 尝试将任意值转为 float64
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}
// ============================================================
// Source: bfcl_runner.go
// ============================================================

// ============================================================
// BFCL Live Runner — 用真实 LLM 跑 BFCL 用例
//
// 核心: 直接调 LLM Chat API（单轮），检查返回的 tool_calls
// 不经过 Agent loop（BFCL 测试的是 LLM 的 function calling 能力）
// ============================================================

// BFCLLiveRunner 真实 LLM 的 BFCL 评测执行器
type BFCLLiveRunner struct {
	llmClient    llm.Client
	systemPrompt string
	verbose      bool
}

// NewBFCLLiveRunner 创建 BFCL Live 评测执行器
func NewBFCLLiveRunner(client llm.Client, systemPrompt string, verbose bool) *BFCLLiveRunner {
	return &BFCLLiveRunner{
		llmClient:    client,
		systemPrompt: systemPrompt,
		verbose:      verbose,
	}
}

// RunBFCL 执行 BFCL 评测（从文件加载）
func (r *BFCLLiveRunner) RunBFCL(path string, maxCases int) (*BFCLLiveReport, error) {
	entries, err := loadBFCLFile(path, maxCases)
	if err != nil {
		return nil, err
	}
	return r.RunBFCLEntries(entries)
}

// RunBFCLEntries 执行 BFCL 评测（从条目列表）
func (r *BFCLLiveRunner) RunBFCLEntries(entries []BFCLEntry) (*BFCLLiveReport, error) {
	report := &BFCLLiveReport{
		Total: len(entries),
	}

	for i, entry := range entries {
		result := r.runBFCLEntry(entry)
		report.Results = append(report.Results, result)

		if result.FuncMatch {
			report.FuncCorrect++
		}
		if result.ArgsMatch {
			report.ArgsCorrect++
		}

		if r.verbose {
			status := "✅"
			if !result.FuncMatch {
				status = "❌"
			}
			fmt.Printf("  %s [%d/%d] %s — expected: %v, actual: %v\n",
				status, i+1, len(entries), result.ID,
				result.ExpectedFunc, result.ActualFunc)
		}
	}

	if report.Total > 0 {
		report.FuncAccuracy = float64(report.FuncCorrect) / float64(report.Total) * 100
		report.ArgsAccuracy = float64(report.ArgsCorrect) / float64(report.Total) * 100
	}

	return report, nil
}

func (r *BFCLLiveRunner) runBFCLEntry(entry BFCLEntry) BFCLLiveResult {
	result := BFCLLiveResult{ID: entry.ID}

	// 解析用户消息
	userMsg, err := parseBFCLQuestion(entry.Question)
	if err != nil {
		result.Question = fmt.Sprintf("[parse error: %v]", err)
		return result
	}
	result.Question = truncateLine(userMsg, 100)

	// 解析期望调用
	expectedCalls := parseBFCLGroundTruth(entry.GroundTruth)
	for _, ec := range expectedCalls {
		result.ExpectedFunc = append(result.ExpectedFunc, ec.FuncName)
	}

	// 解析工具定义
	toolDefs, _, err := parseBFCLFunctions(entry.Function)
	if err != nil {
		return result
	}

	// 构建 messages: system + user
	messages := []llm.Message{
		{Role: "system", Content: r.systemPrompt},
		{Role: "user", Content: userMsg},
	}

	// 直接调 LLM Chat API（单轮，不经过 Agent loop）
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := r.llmClient.Chat(ctx, messages, toolDefs)
	if err != nil {
		result.Question += fmt.Sprintf(" [LLM error: %v]", err)
		return result
	}

	// 提取实际的 tool_calls
	if resp != nil && len(resp.Message.ToolCalls) > 0 {
		for _, tc := range resp.Message.ToolCalls {
			result.ActualFunc = append(result.ActualFunc, tc.Function.Name)
		}
		raw, _ := json.Marshal(resp.Message.ToolCalls)
		result.RawToolCalls = string(raw)
	}

	// 检查函数名匹配
	result.FuncMatch = matchFuncNames(result.ExpectedFunc, result.ActualFunc)

	// 检查参数匹配
	result.ArgsMatch = result.FuncMatch && matchFuncArgs(expectedCalls, resp.Message.ToolCalls)

	return result
}

// matchFuncNames 检查期望和实际的函数名是否匹配
func matchFuncNames(expected, actual []string) bool {
	if len(expected) == 0 {
		return len(actual) > 0
	}
	expectedSet := make(map[string]bool)
	for _, f := range expected {
		expectedSet[f] = true
	}
	for _, f := range actual {
		if expectedSet[f] {
			return true
		}
	}
	return false
}

// matchFuncArgs 简化版参数匹配
func matchFuncArgs(expected []BFCLExpectedCall, actual []llm.ToolCall) bool {
	if len(expected) == 0 || len(actual) == 0 {
		return false
	}
	for _, ec := range expected {
		matched := false
		for _, tc := range actual {
			if tc.Function.Name != ec.FuncName {
				continue
			}
			var actualArgs map[string]interface{}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &actualArgs); err != nil {
				continue
			}
			allMatch := true
			for k, expectedVal := range ec.Args {
				actualVal, ok := actualArgs[k]
				if !ok {
					allMatch = false
					break
				}
				// 使用语义等价比较（处理 10 vs 10.0、列表格式差异等）
				if !semanticEqual(expectedVal, actualVal) {
					allMatch = false
					break
				}
			}
			if allMatch {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// loadBFCLFile 加载 BFCL JSONL 文件
func loadBFCLFile(path string, maxCases int) ([]BFCLEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open BFCL file: %w", err)
	}
	defer f.Close()

	var entries []BFCLEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry BFCLEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
		if maxCases > 0 && len(entries) >= maxCases {
			break
		}
	}
	return entries, scanner.Err()
}

// PrintBFCLReport 打印 BFCL 评测报告
func PrintBFCLReport(report *BFCLLiveReport) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║             BFCL Live Evaluation Report             ║")
	fmt.Println("╠══════════════════════════════════════════════════════╣")
	fmt.Printf("║  Category       : %-33s ║\n", truncateLine(report.Category, 33))
	fmt.Printf("║  Total Cases    : %-33d ║\n", report.Total)
	fmt.Printf("║  Func Correct   : %-33d ║\n", report.FuncCorrect)
	fmt.Printf("║  Args Correct   : %-33d ║\n", report.ArgsCorrect)
	fmt.Printf("║  Func Accuracy  : %-32.1f%% ║\n", report.FuncAccuracy)
	fmt.Printf("║  Args Accuracy  : %-32.1f%% ║\n", report.ArgsAccuracy)
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()
}
