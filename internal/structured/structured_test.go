package structured

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ---------- Schema 测试 ----------

func TestNewSchema(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"age":  {"type": "integer"}
		},
		"required": ["name", "age"]
	}`)

	s, err := NewSchema("test", "test schema", raw)
	if err != nil {
		t.Fatalf("NewSchema failed: %v", err)
	}
	if s.Name != "test" {
		t.Errorf("expected name 'test', got %q", s.Name)
	}
}

func TestNewSchemaInvalidJSON(t *testing.T) {
	_, err := NewSchema("bad", "", json.RawMessage(`{broken`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestNewSchemaFromStruct(t *testing.T) {
	type Person struct {
		Name string `json:"name" desc:"Person's name"`
		Age  int    `json:"age" desc:"Person's age"`
		Tags []string `json:"tags,omitempty" desc:"Optional tags"`
	}

	s, err := NewSchemaFromStruct("person", "A person", Person{})
	if err != nil {
		t.Fatalf("NewSchemaFromStruct failed: %v", err)
	}

	if s.Name != "person" {
		t.Errorf("expected name 'person', got %q", s.Name)
	}

	// 验证 schema 包含正确字段
	var parsed map[string]any
	json.Unmarshal(s.Raw, &parsed)
	props := parsed["properties"].(map[string]any)

	if _, ok := props["name"]; !ok {
		t.Error("schema missing 'name' property")
	}
	if _, ok := props["age"]; !ok {
		t.Error("schema missing 'age' property")
	}

	// required 应该包含 name 和 age（没有 omitempty），tags 有 omitempty 不在 required 中
	required := parsed["required"].([]any)
	reqSet := make(map[string]bool)
	for _, r := range required {
		reqSet[r.(string)] = true
	}
	if !reqSet["name"] || !reqSet["age"] {
		t.Error("required should contain 'name' and 'age'")
	}
	if reqSet["tags"] {
		t.Error("'tags' should NOT be required (has omitempty)")
	}
}

// ---------- Validation 测试 ----------

func TestValidateSimpleObject(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"score": {"type": "number"}
		},
		"required": ["name", "score"],
		"additionalProperties": false
	}`)
	s, _ := NewSchema("test", "", raw)

	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"valid", `{"name":"Alice","score":95.5}`, true},
		{"missing required", `{"name":"Alice"}`, false},
		{"wrong type", `{"name":123,"score":95.5}`, false},
		{"additional prop", `{"name":"Alice","score":95.5,"extra":"x"}`, false},
		{"invalid json", `not json`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.Validate([]byte(tt.input))
			if result.Valid != tt.valid {
				t.Errorf("Validate(%s) = %v, want %v. Errors: %v", tt.input, result.Valid, tt.valid, result.Errors)
			}
		})
	}
}

func TestValidateEnum(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"color": {"type": "string", "enum": ["red", "green", "blue"]}
		},
		"required": ["color"]
	}`)
	s, _ := NewSchema("test", "", raw)

	valid := s.Validate([]byte(`{"color":"red"}`))
	if !valid.Valid {
		t.Error("expected valid for enum value 'red'")
	}

	invalid := s.Validate([]byte(`{"color":"yellow"}`))
	if invalid.Valid {
		t.Error("expected invalid for non-enum value 'yellow'")
	}
}

func TestValidateNestedObject(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"user": {
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"email": {"type": "string"}
				},
				"required": ["name", "email"]
			}
		},
		"required": ["user"]
	}`)
	s, _ := NewSchema("test", "", raw)

	valid := s.Validate([]byte(`{"user":{"name":"Alice","email":"a@b.com"}}`))
	if !valid.Valid {
		t.Errorf("expected valid, got errors: %v", valid.Errors)
	}

	invalid := s.Validate([]byte(`{"user":{"name":"Alice"}}`))
	if invalid.Valid {
		t.Error("expected invalid: missing required 'email' in nested object")
	}
}

func TestValidateArray(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"items": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"id": {"type": "integer"},
						"name": {"type": "string"}
					},
					"required": ["id", "name"]
				}
			}
		},
		"required": ["items"]
	}`)
	s, _ := NewSchema("test", "", raw)

	valid := s.Validate([]byte(`{"items":[{"id":1,"name":"a"},{"id":2,"name":"b"}]}`))
	if !valid.Valid {
		t.Errorf("expected valid, errors: %v", valid.Errors)
	}

	// array item 缺少 required 字段
	invalid := s.Validate([]byte(`{"items":[{"id":1}]}`))
	if invalid.Valid {
		t.Error("expected invalid: array item missing 'name'")
	}
}

func TestValidateTypeChecks(t *testing.T) {
	tests := []struct {
		schemaType string
		value      string
		valid      bool
	}{
		{"string", `{"v":"hello"}`, true},
		{"string", `{"v":123}`, false},
		{"integer", `{"v":42}`, true},
		{"integer", `{"v":42.5}`, false},
		{"number", `{"v":3.14}`, true},
		{"boolean", `{"v":true}`, true},
		{"boolean", `{"v":"true"}`, false},
		{"array", `{"v":[1,2,3]}`, true},
		{"array", `{"v":"not array"}`, false},
		{"object", `{"v":{"a":1}}`, true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%v", tt.schemaType, tt.valid), func(t *testing.T) {
			raw := json.RawMessage(fmt.Sprintf(`{
				"type": "object",
				"properties": {"v": {"type": "%s"}},
				"required": ["v"]
			}`, tt.schemaType))
			s, _ := NewSchema("test", "", raw)
			result := s.Validate([]byte(tt.value))
			if result.Valid != tt.valid {
				t.Errorf("type=%s value=%s: got valid=%v, want %v. Errors: %v",
					tt.schemaType, tt.value, result.Valid, tt.valid, result.Errors)
			}
		})
	}
}

// ---------- StripMarkdown 测试 ----------

func TestStripMarkdownCodeBlock(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\n{\"a\":1}\n```", `{"a":1}`},
		{"  ```json\n{\"a\":1}\n```  ", `{"a":1}`},
		{`plain text`, `plain text`},
	}

	for _, tt := range tests {
		result := StripMarkdownCodeBlock(tt.input)
		if result != tt.expected {
			t.Errorf("StripMarkdownCodeBlock(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// ---------- ResponseFormat 构建测试 ----------

func TestBuildResponseFormat(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`)
	s, _ := NewSchema("test_schema", "A test", raw)

	rf := BuildResponseFormat(s)
	if rf.Type != "json_schema" {
		t.Errorf("expected type 'json_schema', got %q", rf.Type)
	}
	if rf.JSONSchema.Name != "test_schema" {
		t.Errorf("expected name 'test_schema', got %q", rf.JSONSchema.Name)
	}
	if !rf.JSONSchema.Strict {
		t.Error("expected strict=true")
	}

	// 验证可序列化
	b, err := json.Marshal(rf)
	if err != nil {
		t.Fatalf("marshal response_format: %v", err)
	}
	if !strings.Contains(string(b), "json_schema") {
		t.Error("serialized format should contain 'json_schema'")
	}
}

// ---------- Engine 集成测试 (Mock) ----------

// mockStructuredClient 模拟支持结构化输出的 LLM
type mockStructuredClient struct {
	responses []string // 按顺序返回
	callIdx   int
}

func (m *mockStructuredClient) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.ChatResponse, error) {
	if m.callIdx >= len(m.responses) {
		return nil, fmt.Errorf("no more mock responses")
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return &llm.ChatResponse{
		Message:      llm.Message{Role: "assistant", Content: resp},
		FinishReason: "stop",
	}, nil
}

func TestEngineFirstAttemptSuccess(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"answer": {"type": "string"},
			"confidence": {"type": "number"}
		},
		"required": ["answer", "confidence"],
		"additionalProperties": false
	}`)
	s, _ := NewSchema("qa", "", raw)

	client := &mockStructuredClient{
		responses: []string{`{"answer":"42","confidence":0.95}`},
	}

	engine := NewEngine(Config{
		Schema:          s,
		MaxRetries:      3,
		UseNativeFormat: false,
		StripMarkdown:   true,
	})

	result, err := engine.Chat(context.Background(), client, []llm.Message{
		{Role: "user", Content: "What is the answer?"},
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Retries != 0 {
		t.Errorf("expected 0 retries, got %d", result.Retries)
	}
	if result.Parsed["answer"] != "42" {
		t.Errorf("expected answer='42', got %v", result.Parsed["answer"])
	}
}

func TestEngineRetryOnInvalidOutput(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "integer"}
		},
		"required": ["name", "age"],
		"additionalProperties": false
	}`)
	s, _ := NewSchema("person", "", raw)

	client := &mockStructuredClient{
		responses: []string{
			`{"name":"Alice"}`,           // 第1次：缺少 age
			`{"name":"Alice","age":30}`,  // 第2次：正确
		},
	}

	engine := NewEngine(Config{
		Schema:          s,
		MaxRetries:      3,
		UseNativeFormat: false,
		StripMarkdown:   true,
	})

	result, err := engine.Chat(context.Background(), client, []llm.Message{
		{Role: "user", Content: "Tell me about Alice"},
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Retries != 1 {
		t.Errorf("expected 1 retry, got %d", result.Retries)
	}
	if result.Parsed["name"] != "Alice" {
		t.Errorf("expected name='Alice', got %v", result.Parsed["name"])
	}
}

func TestEngineMaxRetriesExceeded(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {"x": {"type": "string"}},
		"required": ["x"],
		"additionalProperties": false
	}`)
	s, _ := NewSchema("test", "", raw)

	client := &mockStructuredClient{
		responses: []string{
			`{"wrong":1}`,
			`{"wrong":2}`,
			`{"wrong":3}`,
			`{"wrong":4}`, // 超过 MaxRetries=3 (总共 4 次尝试: 1 初始 + 3 重试)
		},
	}

	engine := NewEngine(Config{
		Schema:     s,
		MaxRetries: 3,
	})

	_, err := engine.Chat(context.Background(), client, []llm.Message{
		{Role: "user", Content: "test"},
	}, nil)

	if err == nil {
		t.Error("expected error after max retries")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("error should mention validation failure, got: %v", err)
	}
}

func TestEngineStripMarkdown(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {"result": {"type": "string"}},
		"required": ["result"],
		"additionalProperties": false
	}`)
	s, _ := NewSchema("test", "", raw)

	client := &mockStructuredClient{
		responses: []string{"```json\n{\"result\":\"hello\"}\n```"},
	}

	engine := NewEngine(Config{
		Schema:        s,
		MaxRetries:    1,
		StripMarkdown: true,
	})

	result, err := engine.Chat(context.Background(), client, []llm.Message{
		{Role: "user", Content: "test"},
	}, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Parsed["result"] != "hello" {
		t.Errorf("expected result='hello', got %v", result.Parsed["result"])
	}
}

func TestEngineToolCallsReturnsError(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`)
	s, _ := NewSchema("test", "", raw)

	// 模拟 LLM 返回工具调用而不是文本
	client := &mockToolCallClient{}

	engine := NewEngine(Config{
		Schema:     s,
		MaxRetries: 1,
	})

	_, err := engine.Chat(context.Background(), client, []llm.Message{
		{Role: "user", Content: "test"},
	}, nil)

	if err == nil {
		t.Error("expected error for tool_calls response")
	}
	if !strings.Contains(err.Error(), "tool_calls") {
		t.Errorf("error should mention tool_calls, got: %v", err)
	}
}

type mockToolCallClient struct{}

func (m *mockToolCallClient) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Message: llm.Message{
			Role: "assistant",
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Type: "function", Function: llm.FunctionCall{Name: "test", Arguments: "{}"}},
			},
		},
		FinishReason: "tool_calls",
	}, nil
}

// ---------- 预设 Schema 测试 ----------

func TestPlanSchema(t *testing.T) {
	s := PlanSchema()
	if s.Name != "execution_plan" {
		t.Errorf("expected name 'execution_plan', got %q", s.Name)
	}

	valid := s.Validate([]byte(`{
		"goal": "Build a web app",
		"steps": [
			{"action": "think", "reasoning": "Plan the architecture"},
			{"action": "tool_call", "tool": "write_file", "args": {"path": "main.go"}, "reasoning": "Create entry point"}
		],
		"estimated_steps": 5
	}`))
	if !valid.Valid {
		t.Errorf("PlanSchema validation failed: %v", valid.Errors)
	}
}

func TestClassificationSchema(t *testing.T) {
	s := ClassificationSchema([]string{"spam", "ham", "uncertain"})
	if s.Name != "classification" {
		t.Errorf("expected name 'classification', got %q", s.Name)
	}

	valid := s.Validate([]byte(`{"category":"spam","confidence":0.92,"reasoning":"Contains suspicious links"}`))
	if !valid.Valid {
		t.Errorf("classification validation failed: %v", valid.Errors)
	}

	// 无效分类
	invalid := s.Validate([]byte(`{"category":"phishing","confidence":0.8,"reasoning":"test"}`))
	if invalid.Valid {
		t.Error("expected invalid for non-enum category")
	}
}

func TestExtractionSchema(t *testing.T) {
	s := ExtractionSchema("contact_info", map[string]string{
		"name":  "Person's full name",
		"email": "Email address",
		"phone": "Phone number",
	})

	valid := s.Validate([]byte(`{"name":"John Doe","email":"john@example.com","phone":"123-456-7890"}`))
	if !valid.Valid {
		t.Errorf("extraction validation failed: %v", valid.Errors)
	}
}

func TestSentimentSchema(t *testing.T) {
	s := SentimentSchema()

	valid := s.Validate([]byte(`{
		"sentiment": "positive",
		"score": 0.8,
		"aspects": [
			{"aspect": "price", "sentiment": "positive"},
			{"aspect": "delivery", "sentiment": "negative"}
		]
	}`))
	if !valid.Valid {
		t.Errorf("sentiment validation failed: %v", valid.Errors)
	}
}

// ---------- Middleware 测试 ----------

func TestMiddlewareStructuredChat(t *testing.T) {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"summary": {"type": "string"},
			"keywords": {"type": "array", "items": {"type": "string"}}
		},
		"required": ["summary", "keywords"],
		"additionalProperties": false
	}`)
	s, _ := NewSchema("summary", "", raw)

	client := &mockStructuredClient{
		responses: []string{`{"summary":"A good book","keywords":["fiction","adventure"]}`},
	}

	mw := NewMiddleware(Config{
		Schema:     s,
		MaxRetries: 2,
	}, nil)

	result, err := mw.StructuredChat(
		context.Background(),
		client,
		"Summarize this book",
		nil,
		"You are a book reviewer",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Parsed["summary"] != "A good book" {
		t.Errorf("expected summary='A good book', got %v", result.Parsed["summary"])
	}
	keywords := result.Parsed["keywords"].([]any)
	if len(keywords) != 2 {
		t.Errorf("expected 2 keywords, got %d", len(keywords))
	}
}

// ---------- ParseInto 泛型测试 ----------

func TestParseInto(t *testing.T) {
	type Answer struct {
		Text       string  `json:"text"`
		Confidence float64 `json:"confidence"`
	}

	result := &StructuredChatResult{
		RawJSON: `{"text":"hello","confidence":0.95}`,
		Parsed:  map[string]any{"text": "hello", "confidence": 0.95},
	}

	answer, err := ParseInto[Answer](result)
	if err != nil {
		t.Fatalf("ParseInto failed: %v", err)
	}
	if answer.Text != "hello" {
		t.Errorf("expected text='hello', got %q", answer.Text)
	}
	if answer.Confidence != 0.95 {
		t.Errorf("expected confidence=0.95, got %f", answer.Confidence)
	}
}

// ---------- injectSchemaPrompt 测试 ----------

func TestInjectSchemaPromptWithSystem(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	s, _ := NewSchema("test", "", raw)

	messages := []llm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
	}

	result := injectSchemaPrompt(messages, s)

	// system prompt 应该被追加了 schema 信息
	if !strings.Contains(result[0].Content, "Output Format") {
		t.Error("system prompt should contain 'Output Format'")
	}
	if !strings.Contains(result[0].Content, "JSON Schema") {
		t.Error("system prompt should contain 'JSON Schema'")
	}
	// 原消息不应被修改
	if strings.Contains(messages[0].Content, "Output Format") {
		t.Error("original messages should not be modified")
	}
}

func TestInjectSchemaPromptWithoutSystem(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)
	s, _ := NewSchema("test", "", raw)

	messages := []llm.Message{
		{Role: "user", Content: "Hello"},
	}

	result := injectSchemaPrompt(messages, s)

	// 应该插入了一条 system message
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Error("first message should be system")
	}
	if !strings.Contains(result[0].Content, "Output Format") {
		t.Error("injected system prompt should contain 'Output Format'")
	}
}
