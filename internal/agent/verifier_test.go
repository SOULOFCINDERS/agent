package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// mockVerifierClient 模拟 LLM 客户端，返回预设的验证结果
type mockVerifierClient struct {
	response string
}

func (m *mockVerifierClient) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Message: llm.Message{
			Role:    "assistant",
			Content: m.response,
		},
	}, nil
}

func TestNeedsVerification_ShortReply(t *testing.T) {
	// 短回复不需要验证
	if NeedsVerification("你好", "你好！有什么可以帮助你的吗？", nil) {
		t.Error("short reply should not need verification")
	}
}

func TestNeedsVerification_FactualContent(t *testing.T) {
	reply := "MacBook Neo 搭载了 M5 Ultra 芯片，售价 19999 元起，已于 2026 年 3 月正式发布。它支持 128GB 统一内存，采用了全新的液态金属散热系统。"
	if !NeedsVerification("告诉我MacBook Neo的信息", reply, nil) {
		t.Error("factual reply should need verification")
	}
}

func TestNeedsVerification_WithToolCalls(t *testing.T) {
	history := []llm.Message{
		{Role: "tool", Content: "搜索结果1"},
		{Role: "tool", Content: "搜索结果2"},
	}
	reply := "这是一个关于某个话题的比较长的回复，包含了很多详细的信息和分析，让我们来看看具体的内容吧。"
	if !NeedsVerification("查一下XX", reply, history) {
		t.Error("reply with 2+ tool calls should need verification")
	}
}

func TestNeedsVerification_WithURL(t *testing.T) {
	reply := "你可以在这里查看详情：https://www.apple.com/macbook-neo 这是官方的产品页面，里面有非常详细的信息。"
	if !NeedsVerification("查一下", reply, nil) {
		t.Error("reply with URL should need verification")
	}
}

func TestExtractToolEvidence(t *testing.T) {
	history := []llm.Message{
		{Role: "system", Content: "你是助手"},
		{Role: "user", Content: "搜索MacBook"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "call_1", Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"MacBook Neo"}`}},
		}},
		{Role: "tool", Content: "MacBook Neo 是苹果最新的笔记本电脑", ToolCallID: "call_1"},
		{Role: "assistant", Content: "根据搜索结果..."},
	}

	evidence := extractToolEvidence(history)
	if len(evidence) != 1 {
		t.Fatalf("expected 1 evidence, got %d", len(evidence))
	}
	if evidence[0].toolName != "web_search" {
		t.Errorf("expected tool name 'web_search', got '%s'", evidence[0].toolName)
	}
	if evidence[0].content != "MacBook Neo 是苹果最新的笔记本电脑" {
		t.Errorf("unexpected content: %s", evidence[0].content)
	}
}

func TestVerifier_Passed(t *testing.T) {
	// 模拟验证通过
	mockResp := VerificationResult{
		Passed: true,
		Issues: []VerifyIssue{},
	}
	respJSON, _ := json.Marshal(mockResp)
	client := &mockVerifierClient{response: string(respJSON)}
	v := NewVerifier(client)

	result, err := v.Verify(context.Background(), "什么是Go语言", "Go是Google开发的编程语言", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected verification to pass")
	}
	if len(result.Issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(result.Issues))
	}
}

func TestVerifier_Failed(t *testing.T) {
	// 模拟验证失败
	mockResp := VerificationResult{
		Passed: false,
		Issues: []VerifyIssue{
			{
				Type:   "unsupported_claim",
				Claim:  "售价 9999 元",
				Reason: "搜索结果中没有提到具体价格",
			},
		},
		Suggestion: "移除价格信息或标注为未确认",
	}
	respJSON, _ := json.Marshal(mockResp)
	client := &mockVerifierClient{response: string(respJSON)}
	v := NewVerifier(client)

	result, err := v.Verify(context.Background(), "MacBook Neo多少钱", "MacBook Neo 售价 9999 元", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed {
		t.Error("expected verification to fail")
	}
	if len(result.Issues) != 1 {
		t.Errorf("expected 1 issue, got %d", len(result.Issues))
	}
	if result.Issues[0].Type != "unsupported_claim" {
		t.Errorf("expected issue type 'unsupported_claim', got '%s'", result.Issues[0].Type)
	}
}

func TestVerifier_Disabled(t *testing.T) {
	client := &mockVerifierClient{response: "should not be called"}
	v := NewVerifier(client)
	v.SetEnabled(false)

	result, err := v.Verify(context.Background(), "test", "test reply", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("disabled verifier should always pass")
	}
}

func TestVerifier_JSONWrappedInCodeBlock(t *testing.T) {
	// LLM 可能会把 JSON 包裹在 ```json...``` 中
	wrappedResp := "```json\n" + `{"passed": true, "issues": [], "suggestion": ""}` + "\n```"
	client := &mockVerifierClient{response: wrappedResp}
	v := NewVerifier(client)

	result, err := v.Verify(context.Background(), "test", "some factual reply about products and releases", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("expected verification to pass")
	}
}

func TestVerifier_MalformedJSON(t *testing.T) {
	// JSON 解析失败时应该默认通过（不阻断主流程）
	client := &mockVerifierClient{response: "I think everything looks good!"}
	v := NewVerifier(client)

	result, err := v.Verify(context.Background(), "test", "some reply about the latest product features and specifications", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Error("malformed JSON should default to passed=true (fail-open)")
	}
}

func TestApplyCorrection_PassedSkips(t *testing.T) {
	client := &mockVerifierClient{response: "corrected text"}
	v := NewVerifier(client)

	vResult := &VerificationResult{Passed: true}
	corrected, err := v.ApplyCorrection(context.Background(), "test", "original reply", vResult, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if corrected != "original reply" {
		t.Error("passed result should return original reply unchanged")
	}
}

func TestBuildVerificationPrompt_WithEvidence(t *testing.T) {
	evidence := []toolEvidence{
		{toolName: "web_search", content: "MacBook Neo launched March 2026"},
		{toolName: "web_fetch", content: "Apple.com product page content"},
	}
	prompt := buildVerificationPrompt("MacBook Neo信息", "MacBook Neo已发布", evidence)

	if !containsStr(prompt, "web_search") {
		t.Error("prompt should mention tool names")
	}
	if !containsStr(prompt, "MacBook Neo launched March 2026") {
		t.Error("prompt should include tool evidence content")
	}
	if !containsStr(prompt, "MacBook Neo信息") {
		t.Error("prompt should include user question")
	}
}

func TestBuildVerificationPrompt_NoEvidence(t *testing.T) {
	prompt := buildVerificationPrompt("question", "reply", nil)
	if !containsStr(prompt, "无（AI 助手没有调用任何工具）") {
		t.Error("prompt should indicate no tools were called")
	}
	if !containsStr(prompt, "unsupported_claim") {
		t.Error("prompt should warn about unsupported claims when no evidence")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
