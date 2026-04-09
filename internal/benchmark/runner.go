package benchmark

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// Runner benchmark 执行器
type Runner struct {
	systemPrompt string
	verbose      bool
}

// NewRunner 创建评测执行器
func NewRunner(systemPrompt string, verbose bool) *Runner {
	return &Runner{
		systemPrompt: systemPrompt,
		verbose:      verbose,
	}
}

// RunAll 执行全部测试用例，生成报告
func (r *Runner) RunAll(cases []TestCase) *BenchmarkReport {
	report := &BenchmarkReport{
		Timestamp:  time.Now(),
		TotalCases: len(cases),
		Dimensions: make(map[Dimension]*DimScore),
	}

	// 初始化维度评分
	dimWeights := map[Dimension]float64{
		DimHallucination:  0.35, // 幻觉防线权重最高
		DimToolUse:        0.20,
		DimReasoning:      0.20,
		DimTaskCompletion: 0.15,
		DimPerformance:    0.10,
	}
	dimNames := map[Dimension]string{
		DimHallucination:  "幻觉防线",
		DimToolUse:        "工具使用",
		DimReasoning:      "推理质量",
		DimTaskCompletion: "任务完成",
		DimPerformance:    "性能指标",
	}

	for dim, w := range dimWeights {
		report.Dimensions[dim] = &DimScore{
			Name:   dimNames[dim],
			Weight: w,
		}
	}

	// 执行每个 case
	for _, tc := range cases {
		result := r.runCase(tc)
		report.Results = append(report.Results, result)

		if ds, ok := report.Dimensions[tc.Dimension]; ok {
			ds.TotalCases++
			if result.Passed {
				ds.Passed++
				report.PassedCases++
			}
			ds.Score += result.Score
		}
	}

	// 计算维度得分和总分
	var weightedSum float64
	var totalWeight float64
	for _, ds := range report.Dimensions {
		if ds.TotalCases > 0 {
			ds.Score = (ds.Score / float64(ds.TotalCases)) * 100
		}
		weightedSum += ds.Score * ds.Weight
		totalWeight += ds.Weight
	}
	if totalWeight > 0 {
		report.OverallScore = weightedSum / totalWeight
	}

	return report
}

// runCase 执行单个测试用例
func (r *Runner) runCase(tc TestCase) TestResult {
	start := time.Now()
	result := TestResult{
		CaseID:      tc.ID,
		CaseName:    tc.Name,
		Dimension:   tc.Dimension,
		SubCategory: tc.SubCategory,
	}

	// 创建 mock LLM client
	mockClient := NewMockLLMClient(tc.MockResponses)

	// 创建 agent（使用mock工具注册表）
	reg := tools.NewRegistry()
	// 注册mock工具（让agent有工具可用但不真正执行）
	registerMockTools(reg, tc.ToolDefs)

	ag := agent.NewLoopAgent(
		mockClient,
		reg,
		r.systemPrompt,
		nil, // trace
		nil, // memStore
		nil, // compressor
	)

	// 执行
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reply, _, err := ag.Chat(ctx, tc.UserMessage, tc.History)
	result.Duration = time.Since(start)

	if err != nil {
		result.Error = err.Error()
		// 某些 case 期望失败（如预算超限），不一定是错误
	}

	// 收集执行上下文信息
	execCtx := &executionContext{
		reply:        reply,
		err:          err,
		mockClient:   mockClient,
		duration:     result.Duration,
		totalTokens:  estimateTokens(mockClient),
	}
	result.TotalTokens = execCtx.totalTokens

	// 评估断言
	var totalWeight float64
	var weightedScore float64
	allPassed := true

	for _, assertion := range tc.Assertions {
		ar := evaluateAssertion(assertion, execCtx)
		result.Details = append(result.Details, ar)

		weight := assertion.Weight
		if weight == 0 {
			weight = 1.0
		}
		totalWeight += weight

		if ar.Passed {
			weightedScore += weight
		} else {
			if assertion.Critical {
				allPassed = false
			}
		}
	}

	if totalWeight > 0 {
		result.Score = weightedScore / totalWeight
	}

	// 如果有 Critical 断言失败，整个 case 算失败
	result.Passed = allPassed && result.Score >= 0.5

	if r.verbose {
		status := "✅ PASS"
		if !result.Passed {
			status = "❌ FAIL"
		}
		fmt.Printf("  %s [%.0f%%] %s — %s\n", status, result.Score*100, tc.ID, tc.Name)
		for _, d := range result.Details {
			if !d.Passed {
				fmt.Printf("    ↳ ❌ %s: %s (actual: %s)\n", d.Assertion.Type, d.Message, d.Actual)
			}
		}
	}

	return result
}

// executionContext 执行上下文，用于断言评估
type executionContext struct {
	reply       string
	err         error
	mockClient  *MockLLMClient
	duration    time.Duration
	totalTokens int
}

// evaluateAssertion 评估单个断言
func evaluateAssertion(a Assertion, ctx *executionContext) AssertResult {
	ar := AssertResult{Assertion: a}

	switch a.Type {
	case AssertContains:
		ar.Actual = truncate(ctx.reply, 200)
		ar.Passed = strings.Contains(ctx.reply, a.Target)
		if ar.Passed {
			ar.Message = fmt.Sprintf("回复包含 %q", a.Target)
		} else {
			ar.Message = fmt.Sprintf("回复不包含 %q", a.Target)
		}

	case AssertNotContains:
		ar.Actual = truncate(ctx.reply, 200)
		ar.Passed = !strings.Contains(ctx.reply, a.Target)
		if ar.Passed {
			ar.Message = fmt.Sprintf("回复不包含 %q (符合预期)", a.Target)
		} else {
			ar.Message = fmt.Sprintf("回复意外包含 %q", a.Target)
		}

	case AssertMatchesRegex:
		re, err := regexp.Compile(a.Target)
		if err != nil {
			ar.Passed = false
			ar.Message = fmt.Sprintf("无效正则: %s", err)
		} else {
			ar.Actual = truncate(ctx.reply, 200)
			ar.Passed = re.MatchString(ctx.reply)
			if ar.Passed {
				ar.Message = fmt.Sprintf("回复匹配正则 %q", a.Target)
			} else {
				ar.Message = fmt.Sprintf("回复不匹配正则 %q", a.Target)
			}
		}

	case AssertToolCalled:
		called := wasToolCalled(ctx.mockClient, a.Target)
		ar.Actual = fmt.Sprintf("tool_called=%v", called)
		ar.Passed = called
		if ar.Passed {
			ar.Message = fmt.Sprintf("工具 %q 被调用", a.Target)
		} else {
			ar.Message = fmt.Sprintf("工具 %q 未被调用", a.Target)
		}

	case AssertToolNotCalled:
		called := wasToolCalled(ctx.mockClient, a.Target)
		ar.Actual = fmt.Sprintf("tool_called=%v", called)
		ar.Passed = !called
		if ar.Passed {
			ar.Message = fmt.Sprintf("工具 %q 未被调用 (符合预期)", a.Target)
		} else {
			ar.Message = fmt.Sprintf("工具 %q 意外被调用", a.Target)
		}

	case AssertGuardTriggered:
		triggered := wasGuardTriggered(ctx.mockClient, a.Target, ctx.reply)
		ar.Actual = fmt.Sprintf("guard_triggered=%v", triggered)
		ar.Passed = triggered
		if ar.Passed {
			ar.Message = fmt.Sprintf("防线 %q 被触发", a.Target)
		} else {
			ar.Message = fmt.Sprintf("防线 %q 未被触发", a.Target)
		}

	case AssertGuardNotTriggered:
		triggered := wasGuardTriggered(ctx.mockClient, a.Target, ctx.reply)
		ar.Actual = fmt.Sprintf("guard_triggered=%v", triggered)
		ar.Passed = !triggered
		if ar.Passed {
			ar.Message = fmt.Sprintf("防线 %q 未触发 (符合预期)", a.Target)
		} else {
			ar.Message = fmt.Sprintf("防线 %q 意外触发", a.Target)
		}

	case AssertLatencyBelow:
		ms := float64(ctx.duration.Milliseconds())
		ar.Actual = fmt.Sprintf("%.0fms", ms)
		ar.Passed = ms < a.Value
		ar.Message = fmt.Sprintf("延迟 %.0fms (阈值 %.0fms)", ms, a.Value)

	case AssertTokensBelow:
		ar.Actual = fmt.Sprintf("%d tokens", ctx.totalTokens)
		ar.Passed = float64(ctx.totalTokens) < a.Value
		ar.Message = fmt.Sprintf("Token %d (阈值 %.0f)", ctx.totalTokens, a.Value)

	default:
		ar.Passed = false
		ar.Message = fmt.Sprintf("未知断言类型: %s", a.Type)
	}

	return ar
}

// ---- 辅助函数 ----

// wasToolCalled 检查 mock client 是否收到了对某工具的调用请求
// 通过检查 LLM 返回的 tool_calls 来判断
func wasToolCalled(mock *MockLLMClient, toolName string) bool {
	allMsgs := mock.GetAllReceivedMessages()
	for _, msgs := range allMsgs {
		for _, msg := range msgs {
			if msg.Role == "assistant" {
				for _, tc := range msg.ToolCalls {
					if tc.Function.Name == toolName {
						return true
					}
				}
			}
		}
	}
	return false
}

// wasGuardTriggered 通过间接信号判断防线是否被触发
// 不同防线的触发信号:
//   - "knowledge_gap": LLM 被调用 > 1 次（说明否定回复被拦截，重新生成了）
//   - "reasoning_guard": 同上
//   - "fabrication_guard": 回复中包含 "⚠️" 或 "[链接已移除]"
//   - "proactive_search": 第一次 Chat 收到的 messages 中包含主动搜索指令
//   - "numeric_guard": LLM 被调用 > 1 次 且 messages 中包含 "calc" 提醒
func wasGuardTriggered(mock *MockLLMClient, guardName string, reply string) bool {
	callCount := mock.CallCount()

	switch guardName {
	case "knowledge_gap":
		// 知识缺口：LLM 被多次调用（第一次否定被拦截）
		if callCount > 1 {
			msgs := mock.GetReceivedMessages(1)
			for _, msg := range msgs {
				if strings.Contains(msg.Content, "[系统提醒]") && strings.Contains(msg.Content, "搜索") {
					return true
				}
			}
		}
		return false

	case "reasoning_guard":
		if callCount > 1 {
			msgs := mock.GetReceivedMessages(1)
			for _, msg := range msgs {
				if strings.Contains(msg.Content, "推理过程") && strings.Contains(msg.Content, "矛盾") {
					return true
				}
			}
		}
		return false

	case "fabrication_guard":
		return strings.Contains(reply, "⚠️") || strings.Contains(reply, "链接已移除")

	case "proactive_search":
		if callCount >= 1 {
			msgs := mock.GetReceivedMessages(0)
			for _, msg := range msgs {
				if strings.Contains(msg.Content, "web_search") && strings.Contains(msg.Content, "系统指令") {
					return true
				}
			}
		}
		return false

	case "numeric_guard":
		if callCount > 1 {
			msgs := mock.GetReceivedMessages(1)
			for _, msg := range msgs {
				if strings.Contains(msg.Content, "calc") {
					return true
				}
			}
		}
		return false

	default:
		return false
	}
}

func estimateTokens(mock *MockLLMClient) int {
	total := 0
	for _, msgs := range mock.GetAllReceivedMessages() {
		for _, msg := range msgs {
			total += len(msg.Content) / 4
		}
	}
	return total
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// mockTool 实现 domain/tool.Tool 接口
type mockTool struct {
	name string
}

func (m *mockTool) Name() string { return m.name }
func (m *mockTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	return fmt.Sprintf("[mock tool %s result]", m.name), nil
}

// registerMockTools 注册 mock 工具（只注册 schema，执行时返回预设结果）
func registerMockTools(reg *tools.Registry, toolDefs []llm.ToolDef) {
	for _, td := range toolDefs {
		reg.Register(&mockTool{name: td.Function.Name})
	}
}
