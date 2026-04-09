package benchmark

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ============================================================
// Source: types.go
// ============================================================

// ============================================================
// Agent Benchmark Framework
//
// 五大评测维度:
//   D1. 幻觉防线 (Hallucination Defense)   — 各防线是否正确拦截
//   D2. 工具使用 (Tool Use)                 — 该调的调了没、参数对不对
//   D3. 推理质量 (Reasoning)                — 推理链路是否连贯
//   D4. 任务完成 (Task Completion)          — 端到端任务是否完成
//   D5. 性能指标 (Performance)              — 延迟、Token 开销
//
// 每个维度下可定义多个 TestCase，每个 TestCase 有:
//   - 输入 (用户消息 + 对话历史)
//   - 期望的判定条件 (Assertions)
//   - 评分逻辑 (0.0 ~ 1.0)
// ============================================================

// Dimension 评测维度
type Dimension string

const (
	DimHallucination  Dimension = "hallucination_defense"
	DimToolUse        Dimension = "tool_use"
	DimReasoning      Dimension = "reasoning"
	DimTaskCompletion Dimension = "task_completion"
	DimPerformance    Dimension = "performance"
)

// TestCase 单个测试用例
type TestCase struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Dimension   Dimension `json:"dimension"`
	SubCategory string    `json:"sub_category"` // e.g. "proactive_search", "numeric_guard"

	// 输入
	UserMessage string        `json:"user_message"`
	History     []llm.Message `json:"history,omitempty"`
	ToolDefs    []llm.ToolDef `json:"tool_defs,omitempty"`

	// Mock LLM 行为: 按顺序返回预设的响应
	MockResponses []MockResponse `json:"mock_responses"`

	// 断言: 测试通过的判定条件
	Assertions []Assertion `json:"assertions"`
}

// MockResponse 模拟 LLM 的单次响应
type MockResponse struct {
	Content    string         `json:"content,omitempty"`
	ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolResult *ToolResult    `json:"tool_result,omitempty"` // 如果有工具调用，模拟工具返回
}

// ToolResult 模拟工具执行结果
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

// AssertionType 断言类型
type AssertionType string

const (
	// 内容断言
	AssertContains      AssertionType = "contains"       // 回复包含某文本
	AssertNotContains   AssertionType = "not_contains"   // 回复不包含某文本
	AssertMatchesRegex  AssertionType = "matches_regex"  // 回复匹配正则

	// 工具断言
	AssertToolCalled    AssertionType = "tool_called"    // 某工具被调用了
	AssertToolNotCalled AssertionType = "tool_not_called" // 某工具没被调用

	// 防线断言
	AssertGuardTriggered    AssertionType = "guard_triggered"     // 某防线被触发
	AssertGuardNotTriggered AssertionType = "guard_not_triggered" // 某防线没被触发

	// 性能断言
	AssertLatencyBelow  AssertionType = "latency_below"  // 延迟低于阈值(ms)
	AssertTokensBelow   AssertionType = "tokens_below"   // Token 数低于阈值

	// 评分断言
	AssertScoreAbove    AssertionType = "score_above"    // LLM-as-Judge 打分高于阈值
)

// Assertion 单个断言
type Assertion struct {
	Type     AssertionType `json:"type"`
	Target   string        `json:"target"`   // 断言目标 (工具名/防线名/正则/文本)
	Value    float64       `json:"value"`     // 数值阈值 (用于 latency_below, tokens_below, score_above)
	Weight   float64       `json:"weight"`    // 此断言在总分中的权重 (0.0 ~ 1.0)
	Critical bool          `json:"critical"`  // 是否关键断言 (失败则整个 case 零分)
}

// ---- 执行结果 ----

// TestResult 单个测试的执行结果
type TestResult struct {
	CaseID      string         `json:"case_id"`
	CaseName    string         `json:"case_name"`
	Dimension   Dimension      `json:"dimension"`
	SubCategory string         `json:"sub_category"`
	Passed      bool           `json:"passed"`
	Score       float64        `json:"score"`        // 0.0 ~ 1.0
	Duration    time.Duration  `json:"duration"`
	TotalTokens int            `json:"total_tokens"`
	Details     []AssertResult `json:"details"`
	Error       string         `json:"error,omitempty"`
}

// AssertResult 单个断言的执行结果
type AssertResult struct {
	Assertion Assertion `json:"assertion"`
	Passed    bool      `json:"passed"`
	Actual    string    `json:"actual"`   // 实际值
	Message   string    `json:"message"`  // 说明
}

// BenchmarkReport 完整评测报告
type BenchmarkReport struct {
	Timestamp    time.Time              `json:"timestamp"`
	TotalCases   int                    `json:"total_cases"`
	PassedCases  int                    `json:"passed_cases"`
	OverallScore float64                `json:"overall_score"` // 加权总分 0-100
	Dimensions   map[Dimension]*DimScore `json:"dimensions"`
	Results      []TestResult           `json:"results"`
}

// DimScore 单维度评分
type DimScore struct {
	Name       string  `json:"name"`
	TotalCases int     `json:"total_cases"`
	Passed     int     `json:"passed"`
	Score      float64 `json:"score"`      // 0-100
	Weight     float64 `json:"weight"`     // 此维度在总分中的权重
}
// ============================================================
// Source: mock_llm.go
// ============================================================

// MockLLMClient 模拟 LLM 客户端，按预设顺序返回响应
// 同时记录所有收到的消息，用于断言检查
type MockLLMClient struct {
	mu            sync.Mutex
	responses     []MockResponse
	callIndex     int
	receivedMsgs  [][]llm.Message // 每次 Chat 调用收到的完整 messages
	receivedTools [][]llm.ToolDef // 每次 Chat 调用收到的 tool defs
}

func NewMockLLMClient(responses []MockResponse) *MockLLMClient {
	return &MockLLMClient{
		responses: responses,
	}
}

func (m *MockLLMClient) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 记录输入
	msgCopy := make([]llm.Message, len(messages))
	copy(msgCopy, messages)
	m.receivedMsgs = append(m.receivedMsgs, msgCopy)

	toolCopy := make([]llm.ToolDef, len(tools))
	copy(toolCopy, tools)
	m.receivedTools = append(m.receivedTools, toolCopy)

	if m.callIndex >= len(m.responses) {
		// 超出预设响应，返回默认空回复
		return &llm.ChatResponse{
			Message:      llm.Message{Role: "assistant", Content: "[mock exhausted]"},
			FinishReason: "stop",
		}, nil
	}

	resp := m.responses[m.callIndex]
	m.callIndex++

	msg := llm.Message{
		Role:      "assistant",
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	}

	finishReason := "stop"
	if len(resp.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}

	return &llm.ChatResponse{
		Message:      msg,
		FinishReason: finishReason,
		Usage: llm.Usage{
			PromptTokens:     len(fmt.Sprintf("%v", messages)) / 4, // 粗略估算
			CompletionTokens: len(resp.Content) / 4,
			TotalTokens:      (len(fmt.Sprintf("%v", messages)) + len(resp.Content)) / 4,
		},
	}, nil
}

// GetReceivedMessages 返回第 n 次调用收到的 messages
func (m *MockLLMClient) GetReceivedMessages(callIdx int) []llm.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if callIdx < len(m.receivedMsgs) {
		return m.receivedMsgs[callIdx]
	}
	return nil
}

// GetAllReceivedMessages 返回所有调用收到的 messages
func (m *MockLLMClient) GetAllReceivedMessages() [][]llm.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.receivedMsgs
}

// CallCount 返回 Chat 被调用的总次数
func (m *MockLLMClient) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callIndex
}
// ============================================================
// Source: report.go
// ============================================================

// PrintReport 打印格式化的评测报告
func PrintReport(r *BenchmarkReport) {
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════════╗")
	fmt.Println("║              Agent Framework Benchmark Report               ║")
	fmt.Printf("║              %s                         ║\n", r.Timestamp.Format("2006-01-02 15:04:05"))
	fmt.Println("╠══════════════════════════════════════════════════════════════╣")
	fmt.Println()

	// 总分
	scoreBar := renderBar(r.OverallScore, 50)
	fmt.Printf("  总分: %.1f / 100  %s\n", r.OverallScore, scoreBar)
	fmt.Printf("  通过: %d / %d 用例\n", r.PassedCases, r.TotalCases)
	fmt.Println()

	// 维度评分
	fmt.Println("  ┌────────────────────────┬───────┬─────────┬────────────────────────────┐")
	fmt.Println("  │ 评测维度               │ 得分  │ 通过率  │ 进度条                     │")
	fmt.Println("  ├────────────────────────┼───────┼─────────┼────────────────────────────┤")

	dimOrder := []Dimension{DimHallucination, DimToolUse, DimReasoning, DimTaskCompletion, DimPerformance}
	for _, dim := range dimOrder {
		ds, ok := r.Dimensions[dim]
		if !ok || ds.TotalCases == 0 {
			continue
		}
		passRate := float64(ds.Passed) / float64(ds.TotalCases) * 100
		bar := renderBar(ds.Score, 20)
		name := padRight(ds.Name, 20)
		fmt.Printf("  │ %s │ %5.1f │ %3.0f%%    │ %s │\n",
			name, ds.Score, passRate, bar)
	}
	fmt.Println("  └────────────────────────┴───────┴─────────┴────────────────────────────┘")
	fmt.Println()

	// 失败用例明细
	var failures []TestResult
	for _, res := range r.Results {
		if !res.Passed {
			failures = append(failures, res)
		}
	}

	if len(failures) > 0 {
		fmt.Printf("  ❌ 失败用例 (%d):\n", len(failures))
		fmt.Println("  ─────────────────────────────────────────────────")
		for _, f := range failures {
			fmt.Printf("  [%s] %s (%.0f%%)\n", f.CaseID, f.CaseName, f.Score*100)
			for _, d := range f.Details {
				if !d.Passed {
					fmt.Printf("    ↳ %s: %s\n", d.Assertion.Type, d.Message)
				}
			}
			if f.Error != "" {
				fmt.Printf("    ↳ Error: %s\n", f.Error)
			}
		}
		fmt.Println()
	}

	// 全部通过的用例
	var passed []TestResult
	for _, res := range r.Results {
		if res.Passed {
			passed = append(passed, res)
		}
	}
	if len(passed) > 0 {
		fmt.Printf("  ✅ 通过用例 (%d):\n", len(passed))
		for _, p := range passed {
			fmt.Printf("    [%s] %s (%.0f%%)\n", p.CaseID, p.CaseName, p.Score*100)
		}
		fmt.Println()
	}

	// 评分等级
	grade := getGrade(r.OverallScore)
	fmt.Printf("  评级: %s\n", grade)
	fmt.Println()
	fmt.Println("╚══════════════════════════════════════════════════════════════╝")
}

func renderBar(score float64, width int) string {
	filled := int(score / 100 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return bar
}

func padRight(s string, length int) string {
	runes := []rune(s)
	// 中文字符占2个宽度
	width := 0
	for _, r := range runes {
		if r > 127 {
			width += 2
		} else {
			width++
		}
	}
	if width >= length {
		return s
	}
	return s + strings.Repeat(" ", length-width)
}

func getGrade(score float64) string {
	switch {
	case score >= 95:
		return "🏆 S  — 卓越 (Outstanding)"
	case score >= 85:
		return "🥇 A  — 优秀 (Excellent)"
	case score >= 75:
		return "🥈 B  — 良好 (Good)"
	case score >= 60:
		return "🥉 C  — 及格 (Acceptable)"
	case score >= 40:
		return "⚠️  D  — 需改进 (Needs Improvement)"
	default:
		return "❌ F  — 不合格 (Failing)"
	}
}
