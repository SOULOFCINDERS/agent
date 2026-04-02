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

// ---- 测试 ----

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
	// 这个测试验证完整的编排流程：
	// 用户 → 编排Agent → 委派 research_agent → 子Agent回复 → 编排Agent整合 → 最终回复

	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())

	// 使用 mock LLM，但子 Agent 和编排 Agent 共用同一个
	// 这里用一个更智能的 mock 来区分编排层和子Agent层
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

	// 通过 system prompt 内容判断当前是编排 Agent 还是子 Agent
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
		// 编排 Agent
		if last.Role == "user" {
			// 委派给 helper_agent
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
			// 收到子 Agent 结果，生成最终回复
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
		// 子 Agent：直接回答
		return &llm.ChatResponse{
			Message: llm.Message{
				Role:    "assistant",
				Content: "我已经完成了任务，结果是：一切顺利。",
			},
			FinishReason: "stop",
		}, nil
	}

	// 默认
	return &llm.ChatResponse{
		Message: llm.Message{Role: "assistant", Content: "ok"},
		FinishReason: "stop",
	}, nil
}

func TestSubRegistry(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())
	reg.Register(tools.NewCalcTool())

	// 指定工具子集
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
			t.Logf("%s: %d tools, prompt %d chars", def.Name, len(def.ToolNames), len(def.SystemPrompt))
		})
	}
}
