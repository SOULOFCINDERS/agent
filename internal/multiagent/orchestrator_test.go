package multiagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// ---- 测试用 Mock LLM ----

// mockOrchestratorLLM 模拟编排 Agent 的行为：
// - 第一轮：返回一个 tool_call 委派给 research_agent
// - 第二轮：收到 tool result 后返回最终回复
type mockOrchestratorLLM struct {
	callCount int
}

func (m *mockOrchestratorLLM) Chat(ctx context.Context, messages []llm.Message, tls []llm.ToolDef) (*llm.ChatResponse, error) {
	m.callCount++

	// 查看最后一条消息
	last := messages[len(messages)-1]

	switch {
	case m.callCount == 1 && last.Role == "user":
		// 第一轮：委派给 research_agent
		return &llm.ChatResponse{
			Message: llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "research_agent",
							Arguments: `{"task":"搜索Go错误处理最佳实践","context":"用户想了解Go语言的错误处理"}`,
						},
					},
				},
			},
			FinishReason: "tool_calls",
		}, nil

	case last.Role == "tool":
		// 收到子 Agent 结果后，返回最终回复
		return &llm.ChatResponse{
			Message: llm.Message{
				Role:    "assistant",
				Content: "根据研究Agent的调查结果，Go错误处理的最佳实践包括：使用 errors.Is/As、自定义错误类型、wrap errors 等。",
			},
			FinishReason: "stop",
		}, nil

	default:
		return &llm.ChatResponse{
			Message: llm.Message{
				Role:    "assistant",
				Content: "默认回复",
			},
			FinishReason: "stop",
		}, nil
	}
}

// mockSubAgentLLM 模拟子 Agent 的行为：直接返回结果
type mockSubAgentLLM struct{}

func (m *mockSubAgentLLM) Chat(ctx context.Context, messages []llm.Message, tls []llm.ToolDef) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Message: llm.Message{
			Role:    "assistant",
			Content: "经过搜索，Go错误处理最佳实践包括：1) 使用 errors.Is 和 errors.As 进行错误检查 2) 使用 fmt.Errorf 的 %w 动词包装错误 3) 定义自定义错误类型",
		},
		FinishReason: "stop",
	}, nil
}

// mockVerboseSubAgentLLM 模拟返回超长结果的子 Agent
type mockVerboseSubAgentLLM struct{}

func (m *mockVerboseSubAgentLLM) Chat(ctx context.Context, messages []llm.Message, tls []llm.ToolDef) (*llm.ChatResponse, error) {
	// 生成一个约 5000 字符的长回复
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(fmt.Sprintf("第%d段：这是一段非常详细的搜索结果内容，包含了大量的技术细节和实现方案。", i+1))
	}
	return &llm.ChatResponse{
		Message: llm.Message{
			Role:    "assistant",
			Content: sb.String(),
		},
		FinishReason: "stop",
	}, nil
}

// mockBudgetTrackingLLM 追踪 token 使用量的 mock
type mockBudgetTrackingLLM struct {
	calls int
}

func (m *mockBudgetTrackingLLM) Chat(ctx context.Context, messages []llm.Message, tls []llm.ToolDef) (*llm.ChatResponse, error) {
	m.calls++
	return &llm.ChatResponse{
		Message: llm.Message{
			Role:    "assistant",
			Content: "回复内容",
		},
		Usage: llm.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
		FinishReason: "stop",
	}, nil
}

// ---- 原有测试 ----

func TestAgentDefValidate(t *testing.T) {
	tests := []struct {
		name    string
		def     AgentDef
		wantErr bool
	}{
		{"valid", AgentDef{Name: "test", Description: "desc", SystemPrompt: "prompt"}, false},
		{"no_name", AgentDef{Description: "desc", SystemPrompt: "prompt"}, true},
		{"no_desc", AgentDef{Name: "test", SystemPrompt: "prompt"}, true},
		{"no_prompt", AgentDef{Name: "test", Description: "desc"}, true},
		{"name_too_long", AgentDef{Name: strings.Repeat("a", 41), Description: "desc", SystemPrompt: "prompt"}, true},
		{"negative_budget", AgentDef{Name: "test", Description: "desc", SystemPrompt: "prompt", MaxTokenBudget: -1}, true},
		{"valid_with_budget", AgentDef{Name: "test", Description: "desc", SystemPrompt: "prompt", MaxTokenBudget: 5000}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.def.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHandoffToolSchema(t *testing.T) {
	def := ResearchAgentDef()
	ht := NewHandoffTool(def, &mockSubAgentLLM{}, tools.NewRegistry(), nil)

	if ht.Name() != "research_agent" {
		t.Errorf("Name() = %q, want %q", ht.Name(), "research_agent")
	}

	s := ht.Schema()
	if s.Desc == "" {
		t.Error("Schema description should not be empty")
	}

	// 验证 schema 是有效 JSON
	var m map[string]any
	if err := json.Unmarshal(s.Schema, &m); err != nil {
		t.Errorf("Schema is not valid JSON: %v", err)
	}

	t.Logf("HandoffTool schema: desc=%q", s.Desc)
}

func TestHandoffToolExecute(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())

	def := AgentDef{
		Name:         "test_agent",
		Description:  "测试Agent",
		SystemPrompt: "你是测试助手",
		ToolNames:    []string{"echo"},
	}

	ht := NewHandoffTool(def, &mockSubAgentLLM{}, reg, nil)

	ctx := context.Background()
	result, err := ht.Execute(ctx, map[string]any{
		"task": "帮我搜索一些信息",
	})

	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	reply, ok := result.(string)
	if !ok {
		t.Fatalf("Execute() result is not string: %T", result)
	}

	if reply == "" {
		t.Error("Execute() returned empty reply")
	}

	t.Logf("HandoffTool result: %s", reply)
}

func TestHandoffToolMissingTask(t *testing.T) {
	def := AgentDef{Name: "test", Description: "test", SystemPrompt: "test"}
	ht := NewHandoffTool(def, &mockSubAgentLLM{}, tools.NewRegistry(), nil)

	_, err := ht.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Error("Execute() should fail without task field")
	}
}

func TestOrchestratorCreation(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())
	reg.Register(tools.NewCalcTool())

	orch, err := NewOrchestrator(OrchestratorConfig{
		LLMClient:      &mockOrchestratorLLM{},
		GlobalRegistry: reg,
		AgentDefs: []AgentDef{
			ResearchAgentDef(),
			CodeAgentDef(),
		},
		DirectTools: []string{"echo", "calc"},
	})

	if err != nil {
		t.Fatalf("NewOrchestrator() error: %v", err)
	}

	if len(orch.AgentDefs()) != 2 {
		t.Errorf("AgentDefs() len = %d, want 2", len(orch.AgentDefs()))
	}
}

func TestOrchestratorDuplicateName(t *testing.T) {
	reg := tools.NewRegistry()

	_, err := NewOrchestrator(OrchestratorConfig{
		LLMClient:      &mockSubAgentLLM{},
		GlobalRegistry: reg,
		AgentDefs: []AgentDef{
			{Name: "dup", Description: "a", SystemPrompt: "a"},
			{Name: "dup", Description: "b", SystemPrompt: "b"},
		},
	})

	if err == nil {
		t.Error("should fail with duplicate agent names")
	}
}

func TestOrchestratorEndToEnd(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())

	mockClient := &orchestratorE2EMock{}

	orch, err := NewOrchestrator(OrchestratorConfig{
		LLMClient:      mockClient,
		GlobalRegistry: reg,
		AgentDefs: []AgentDef{
			{
				Name:         "helper_agent",
				Description:  "通用助手",
				SystemPrompt: "你是通用助手",
			},
		},
		TraceWriter: os.Stderr,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator() error: %v", err)
	}

	ctx := context.Background()
	reply, _, err := orch.Chat(ctx, "帮我做一件事", nil)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	if reply == "" {
		t.Error("Chat() returned empty reply")
	}

	t.Logf("Final reply: %s", reply)
	t.Logf("Mock LLM calls: %d", mockClient.calls)
}

// orchestratorE2EMock 模拟完整的端到端流程
type orchestratorE2EMock struct {
	calls int
}

func (m *orchestratorE2EMock) Chat(ctx context.Context, messages []llm.Message, tls []llm.ToolDef) (*llm.ChatResponse, error) {
	m.calls++

	isOrchestrator := false
	isSubAgent := false
	for _, msg := range messages {
		if msg.Role == "system" {
			if strings.Contains(msg.Content, "任务编排助手") || strings.Contains(msg.Content, "委派") {
				isOrchestrator = true
			}
			if strings.Contains(msg.Content, "通用助手") {
				isSubAgent = true
			}
		}
	}

	last := messages[len(messages)-1]

	if isOrchestrator {
		if last.Role == "user" {
			return &llm.ChatResponse{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{{
						ID:   fmt.Sprintf("call_%d", m.calls),
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "helper_agent",
							Arguments: `{"task":"帮用户完成这件事"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}, nil
		}
		if last.Role == "tool" {
			return &llm.ChatResponse{
				Message: llm.Message{
					Role:    "assistant",
					Content: "任务完成！助手Agent回复：" + last.Content,
				},
				FinishReason: "stop",
			}, nil
		}
	}

	if isSubAgent {
		return &llm.ChatResponse{
			Message: llm.Message{
				Role:    "assistant",
				Content: "我已经完成了任务，结果是：一切顺利。",
			},
			FinishReason: "stop",
		}, nil
	}

	return &llm.ChatResponse{
		Message: llm.Message{Role: "assistant", Content: "ok"},
		FinishReason: "stop",
	}, nil
}

func TestSubRegistry(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())
	reg.Register(tools.NewCalcTool())

	def := AgentDef{
		Name: "test", Description: "test", SystemPrompt: "test",
		ToolNames: []string{"echo"},
	}
	sub := def.SubRegistry(reg)

	if sub.Get("echo") == nil {
		t.Error("sub registry should have echo")
	}
	if sub.Get("calc") != nil {
		t.Error("sub registry should not have calc")
	}
}

func TestPresetAgentDefs(t *testing.T) {
	presets := []AgentDef{
		ResearchAgentDef(),
		CodeAgentDef(),
		WriterAgentDef(),
	}

	for _, def := range presets {
		t.Run(def.Name, func(t *testing.T) {
			if err := def.Validate(); err != nil {
				t.Errorf("preset %q is invalid: %v", def.Name, err)
			}
			if len(def.ToolNames) == 0 {
				t.Errorf("preset %q has no tools", def.Name)
			}
			if def.MaxTokenBudget <= 0 {
				t.Errorf("preset %q should have MaxTokenBudget set", def.Name)
			}
			t.Logf("%s: %d tools, prompt %d chars, budget %d",
				def.Name, len(def.ToolNames), len(def.SystemPrompt), def.MaxTokenBudget)
		})
	}
}

// ============================================================
// 改进1 测试: compactResult — 结果截断
// ============================================================

func TestCompactResult_Short(t *testing.T) {
	// 短结果不截断
	result := "这是一个简短的回复"
	out := compactResult(result, 1000)
	if out != result {
		t.Errorf("short result should not be compacted, got %q", out)
	}
}

func TestCompactResult_Long(t *testing.T) {
	// 生成一个长字符串（约 3000 字符 = ~1500 tokens）
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(fmt.Sprintf("段落%d：这是搜索结果的详细内容。", i))
	}
	result := sb.String()

	// 限制 500 tokens
	out := compactResult(result, 500)
	if len(out) >= len(result) {
		t.Errorf("long result should be compacted, original=%d, got=%d", len(result), len(out))
	}

	if !strings.Contains(out, "已省略约") {
		t.Error("compacted result should contain omission marker")
	}

	t.Logf("Original: %d chars, Compacted: %d chars", len(result), len(out))
}

func TestCompactResult_ZeroLimit(t *testing.T) {
	result := "任何内容"
	out := compactResult(result, 0)
	if out != result {
		t.Error("zero limit should not compact")
	}
}

func TestCompactResult_ExactBoundary(t *testing.T) {
	// 创建刚好等于限制的内容
	result := strings.Repeat("ab", 100) // 200 chars = 100 tokens
	out := compactResult(result, 100)
	if out != result {
		t.Error("exact boundary should not compact")
	}
}

func TestHandoffTool_CompactResult(t *testing.T) {
	// 端到端测试：子 Agent 返回超长结果，应被截断
	reg := tools.NewRegistry()

	def := AgentDef{
		Name:         "verbose_agent",
		Description:  "话多的Agent",
		SystemPrompt: "你是话多的助手",
	}

	ht := NewHandoffTool(def, &mockVerboseSubAgentLLM{}, reg, nil)
	ht.SetMaxResultTokens(200) // 非常小的限制

	ctx := context.Background()
	result, err := ht.Execute(ctx, map[string]any{
		"task": "搜索信息",
	})

	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	reply := result.(string)
	// 应该被截断了
	if !strings.Contains(reply, "已省略约") {
		t.Logf("Reply length: %d", len(reply))
		// 如果原始结果足够短就不会截断，所以只在确实长的情况下检查
		if estimateStringTokens(reply) > 200 {
			t.Error("verbose result should be compacted")
		}
	}
}

// ============================================================
// 改进2 测试: compactOrchestratorHistory — 历史窗口管理
// ============================================================

func TestCompactOrchestratorHistory_NoCompaction(t *testing.T) {
	history := []llm.Message{
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "你好！"},
	}

	out := compactOrchestratorHistory(history, 10000)
	if len(out) != len(history) {
		t.Errorf("should not compact small history")
	}
	if out[0].Content != "你好" {
		t.Errorf("content should be unchanged")
	}
}

func TestCompactOrchestratorHistory_CompactsToolResults(t *testing.T) {
	// 构造一个有大量 tool_result 的历史
	longContent := strings.Repeat("这是一段很长的搜索结果。", 200) // ~3400 字符

	history := []llm.Message{
		{Role: "user", Content: "搜索Go错误处理"},
		{Role: "assistant", Content: "好的，让我搜索"},
		{Role: "tool", Content: longContent, ToolCallID: "call_1"},
		{Role: "assistant", Content: "根据搜索结果..."},
		{Role: "user", Content: "再搜索一下 Rust"},
		{Role: "assistant", Content: "好的"},
		{Role: "tool", Content: longContent, ToolCallID: "call_2"},
		{Role: "assistant", Content: "结果如下"},
	}

	// 设置一个较小的 token 限制
	out := compactOrchestratorHistory(history, 500)

	// 最近 4 条应该不变
	if out[len(out)-1].Content != "结果如下" {
		t.Error("last message should be protected")
	}

	// 较早的 tool_result 应该被压缩
	toolMsg := out[2] // 第一个 tool result
	if !strings.Contains(toolMsg.Content, "上下文已压缩") {
		t.Errorf("old tool result should be compacted, got: %s", toolMsg.Content[:100])
	}

	t.Logf("Compacted tool result length: %d", len(toolMsg.Content))
}

func TestCompactOrchestratorHistory_ProtectsRecent(t *testing.T) {
	// 只有 3 条消息，都应该受保护
	history := []llm.Message{
		{Role: "user", Content: strings.Repeat("x", 10000)},
		{Role: "tool", Content: strings.Repeat("y", 10000)},
		{Role: "assistant", Content: strings.Repeat("z", 10000)},
	}

	out := compactOrchestratorHistory(history, 100)

	// 所有消息都在受保护范围内（最近 4 条 > 总量 3 条）
	if out[0].Content != history[0].Content {
		t.Error("all messages should be protected when count <= 4")
	}
}

func TestCompactOrchestratorHistory_SkipsSystem(t *testing.T) {
	history := []llm.Message{
		{Role: "system", Content: strings.Repeat("system prompt ", 100)},
		{Role: "user", Content: "hello"},
		{Role: "tool", Content: strings.Repeat("big result ", 500)},
		{Role: "assistant", Content: "reply"},
		{Role: "user", Content: "next"},
		{Role: "assistant", Content: "next reply"},
		{Role: "tool", Content: strings.Repeat("another result ", 500)},
		{Role: "assistant", Content: "final"},
	}

	out := compactOrchestratorHistory(history, 500)

	// system 消息应该完整保留
	if out[0].Content != history[0].Content {
		t.Error("system message should never be compacted")
	}
}

func TestOrchestratorWithMaxHistory(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())

	mockClient := &orchestratorE2EMock{}

	orch, err := NewOrchestrator(OrchestratorConfig{
		LLMClient:        mockClient,
		GlobalRegistry:   reg,
		AgentDefs:        []AgentDef{{Name: "helper_agent", Description: "通用助手", SystemPrompt: "你是通用助手"}},
		MaxHistoryTokens: 5000,
	})
	if err != nil {
		t.Fatalf("NewOrchestrator() error: %v", err)
	}

	if orch.maxHistoryTokens != 5000 {
		t.Errorf("maxHistoryTokens = %d, want 5000", orch.maxHistoryTokens)
	}

	// 动态设置
	orch.SetMaxHistoryTokens(3000)
	if orch.maxHistoryTokens != 3000 {
		t.Errorf("after SetMaxHistoryTokens, got %d, want 3000", orch.maxHistoryTokens)
	}
}

// ============================================================
// 改进3 测试: Token 预算控制
// ============================================================

func TestHandoffTool_TokenBudget(t *testing.T) {
	reg := tools.NewRegistry()

	def := AgentDef{
		Name:           "budget_agent",
		Description:    "有预算的Agent",
		SystemPrompt:   "你是助手",
		MaxTokenBudget: 500, // 500 token 预算
	}

	mock := &mockBudgetTrackingLLM{}
	ht := NewHandoffTool(def, mock, reg, nil)

	ctx := context.Background()
	_, err := ht.Execute(ctx, map[string]any{
		"task": "做一些事情",
	})

	// 不应该报错（mock 返回的 token 数量不会超预算）
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// 验证 mock 被调用了
	if mock.calls == 0 {
		t.Error("mock should have been called at least once")
	}

	t.Logf("Mock was called %d times", mock.calls)
}

func TestHandoffTool_NoBudget(t *testing.T) {
	reg := tools.NewRegistry()

	def := AgentDef{
		Name:           "no_budget_agent",
		Description:    "无预算的Agent",
		SystemPrompt:   "你是助手",
		MaxTokenBudget: 0, // 无预算限制
	}

	mock := &mockBudgetTrackingLLM{}
	ht := NewHandoffTool(def, mock, reg, nil)

	ctx := context.Background()
	_, err := ht.Execute(ctx, map[string]any{
		"task": "做一些事情",
	})

	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
}

// ============================================================
// 改进4 测试: 上下文白名单
// ============================================================

func TestFilterContext_NoWhitelist(t *testing.T) {
	// 空白名单 → 全部保留
	input := "topic: Go编程\nhistory: 之前的对话\n详细说明一下"
	out := filterContext(input, nil)
	if out != input {
		t.Errorf("no whitelist should pass through, got %q", out)
	}
}

func TestFilterContext_EmptyContext(t *testing.T) {
	out := filterContext("", []string{"topic"})
	if out != "" {
		t.Errorf("empty context should return empty, got %q", out)
	}
}

func TestFilterContext_WhitelistFilter(t *testing.T) {
	input := "topic: Go编程\nhistory: 之前讨论了很多内容\nuser_pref: 喜欢简洁\n这是一段普通文本"
	out := filterContext(input, []string{"topic", "user_pref"})

	if !strings.Contains(out, "topic: Go编程") {
		t.Error("should keep whitelisted 'topic'")
	}
	if !strings.Contains(out, "user_pref: 喜欢简洁") {
		t.Error("should keep whitelisted 'user_pref'")
	}
	if strings.Contains(out, "history: 之前讨论") {
		t.Error("should filter out non-whitelisted 'history'")
	}
	if !strings.Contains(out, "这是一段普通文本") {
		t.Error("should keep non key-value lines")
	}

	t.Logf("Filtered context: %q", out)
}

func TestFilterContext_ChineseColon(t *testing.T) {
	input := "主题：Go编程\n历史：之前的对话"
	out := filterContext(input, []string{"主题"})

	if !strings.Contains(out, "主题：Go编程") {
		t.Error("should support Chinese colon")
	}
	if strings.Contains(out, "历史：之前的对话") {
		t.Error("should filter with Chinese colon too")
	}
}

func TestFilterContext_CaseInsensitive(t *testing.T) {
	input := "Topic: Go编程\nHistory: old stuff"
	out := filterContext(input, []string{"topic"})

	if !strings.Contains(out, "Topic: Go编程") {
		t.Error("whitelist should be case-insensitive")
	}
	if strings.Contains(out, "History:") {
		t.Error("should filter out History")
	}
}

func TestExtractLineKey(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"topic: Go", "topic"},
		{"topic：Go", "topic"},
		{"this is just text", ""},
		{"", ""},
		{"  key:value  ", "key"},
		{"multi word key: value", ""}, // 含空格的 key 不识别
		{"a: b", "a"},
	}

	for _, tt := range tests {
		got := extractLineKey(tt.line)
		if got != tt.want {
			t.Errorf("extractLineKey(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestHandoffTool_ContextFilter(t *testing.T) {
	// 端到端测试：白名单过滤
	reg := tools.NewRegistry()

	def := AgentDef{
		Name:          "filtered_agent",
		Description:   "只接受特定上下文的Agent",
		SystemPrompt:  "你是助手",
		AcceptContext: []string{"topic", "format"},
	}

	// 使用一个能检查收到的消息内容的 mock
	mock := &contextCheckingLLM{}
	ht := NewHandoffTool(def, mock, reg, nil)

	ctx := context.Background()
	_, err := ht.Execute(ctx, map[string]any{
		"task":    "做点事",
		"context": "topic: Go编程\nhistory: 很长的历史记录\nformat: markdown",
	})

	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// 检查 mock 收到的消息中不包含 history
	if strings.Contains(mock.lastUserMsg, "history: 很长的历史记录") {
		t.Error("history should be filtered out by AcceptContext")
	}
	if !strings.Contains(mock.lastUserMsg, "topic: Go编程") {
		t.Error("topic should be kept by AcceptContext")
	}
	if !strings.Contains(mock.lastUserMsg, "format: markdown") {
		t.Error("format should be kept by AcceptContext")
	}
}

// contextCheckingLLM 记录收到的用户消息
type contextCheckingLLM struct {
	lastUserMsg string
}

func (m *contextCheckingLLM) Chat(ctx context.Context, messages []llm.Message, tls []llm.ToolDef) (*llm.ChatResponse, error) {
	for _, msg := range messages {
		if msg.Role == "user" {
			m.lastUserMsg = msg.Content
		}
	}
	return &llm.ChatResponse{
		Message:      llm.Message{Role: "assistant", Content: "ok"},
		FinishReason: "stop",
	}, nil
}

// ============================================================
// 辅助函数测试
// ============================================================

func TestEstimateStringTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"ab", 1},
		{"abc", 2},
		{"hello world", 6}, // 11 chars → (11+1)/2 = 6
	}

	for _, tt := range tests {
		got := estimateStringTokens(tt.input)
		if got != tt.want {
			t.Errorf("estimateStringTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestEstimateHistoryTokens(t *testing.T) {
	history := []llm.Message{
		{Role: "user", Content: "hello"}, // 3 + 4 = 7
		{Role: "assistant", Content: "hi"},  // 1 + 4 = 5
	}

	tokens := estimateHistoryTokens(history)
	if tokens <= 0 {
		t.Errorf("should have positive tokens, got %d", tokens)
	}

	t.Logf("History tokens: %d", tokens)
}
