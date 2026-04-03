package structured

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ---------- 配置 ----------

// Config 结构化输出配置
type Config struct {
	// Schema 目标输出 schema
	Schema *Schema

	// MaxRetries 输出不符合 schema 时的最大重试次数
	// 默认 3
	MaxRetries int

	// UseNativeFormat 是否使用 LLM 原生 response_format（OpenAI JSON Schema mode）
	// 如果 LLM 不支持，自动降级为 prompt-based
	UseNativeFormat bool

	// StripMarkdown 是否自动去除 LLM 返回的 markdown 代码块包裹
	// 很多 LLM 会用 ```json ... ``` 包裹 JSON 输出
	StripMarkdown bool
}

// DefaultConfig 返回默认配置
func DefaultConfig(schema *Schema) Config {
	return Config{
		Schema:          schema,
		MaxRetries:      3,
		UseNativeFormat: true,
		StripMarkdown:   true,
	}
}

// ---------- ResponseFormat 构建 ----------

// ResponseFormat OpenAI 兼容的 response_format 结构
type ResponseFormat struct {
	Type       string              `json:"type"`                  // "json_schema"
	JSONSchema *ResponseJSONSchema `json:"json_schema,omitempty"` // schema 定义
}

// ResponseJSONSchema response_format 中的 json_schema 部分
type ResponseJSONSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Strict      bool            `json:"strict"`
	Schema      json.RawMessage `json:"schema"`
}

// BuildResponseFormat 构建 OpenAI 兼容的 response_format
func BuildResponseFormat(schema *Schema) *ResponseFormat {
	return &ResponseFormat{
		Type: "json_schema",
		JSONSchema: &ResponseJSONSchema{
			Name:        schema.Name,
			Description: schema.Description,
			Strict:      schema.Strict,
			Schema:      schema.Raw,
		},
	}
}

// BuildJSONObjectFormat 构建简单的 json_object response_format
// 用于不支持 json_schema 但支持 json_object 的模型
func BuildJSONObjectFormat() *ResponseFormat {
	return &ResponseFormat{
		Type: "json_object",
	}
}

// ---------- Prompt 注入 ----------

// schemaPromptSuffix 生成 prompt 后缀，指导 LLM 输出 JSON
func schemaPromptSuffix(schema *Schema) string {
	prettySchema, _ := json.MarshalIndent(json.RawMessage(schema.Raw), "", "  ")
	return fmt.Sprintf(
		"\n\n## Output Format\n"+
			"You MUST respond with valid JSON that conforms to the following JSON Schema.\n"+
			"Do NOT include any text before or after the JSON. Do NOT wrap it in markdown code blocks.\n"+
			"Schema:\n```json\n%s\n```\n"+
			"Respond ONLY with the JSON object.",
		string(prettySchema),
	)
}

// retryPromptForErrors 生成重试提示，包含具体的验证错误
func retryPromptForErrors(result *ValidationResult) string {
	var sb strings.Builder
	sb.WriteString("Your previous response did not conform to the required JSON Schema. Errors:\n")
	for _, e := range result.Errors {
		sb.WriteString(fmt.Sprintf("- %s\n", e.Error()))
	}
	sb.WriteString("\nPlease fix these errors and respond with valid JSON only.")
	return sb.String()
}

// ---------- 输出处理 ----------

// StripMarkdownCodeBlock 去除 markdown 代码块包裹
// 处理: ```json\n{...}\n``` 或 ```\n{...}\n```
func StripMarkdownCodeBlock(s string) string {
	s = strings.TrimSpace(s)

	// 检查 ```json 开头
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimSpace(s)
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSpace(s)
	}

	// 去除尾部 ```
	if strings.HasSuffix(s, "```") {
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}

	return s
}

// ---------- 核心引擎 ----------

// Engine 结构化输出引擎
type Engine struct {
	config Config
}

// NewEngine 创建结构化输出引擎
func NewEngine(config Config) *Engine {
	if config.MaxRetries <= 0 {
		config.MaxRetries = 3
	}
	return &Engine{config: config}
}

// StructuredChatResult 结构化输出结果
type StructuredChatResult struct {
	// RawJSON 原始 JSON 字符串（已验证）
	RawJSON string

	// Parsed 解析后的 map 对象
	Parsed map[string]any

	// Retries 重试次数
	Retries int

	// UsedNativeFormat 是否使用了原生 response_format
	UsedNativeFormat bool

	// History 更新后的对话历史
	History []llm.Message
}

// Chat 执行结构化输出对话
// 自动处理 response_format 注入、输出验证、重试逻辑
func (e *Engine) Chat(ctx context.Context, client llm.Client, messages []llm.Message, tools []llm.ToolDef) (*StructuredChatResult, error) {
	schema := e.config.Schema
	if schema == nil {
		return nil, fmt.Errorf("structured output: schema is nil")
	}

	// 判断是否使用原生 response_format
	usedNative := false
	workingMessages := make([]llm.Message, len(messages))
	copy(workingMessages, messages)

	if e.config.UseNativeFormat {
		// 尝试使用原生格式
		if sc, ok := client.(*llm.OpenAICompatClient); ok {
			_ = sc // client 支持原生格式
			usedNative = true
		}
	}

	if !usedNative {
		// 降级为 prompt-based：在最后一条 user message 或 system prompt 中注入 schema
		workingMessages = injectSchemaPrompt(workingMessages, schema)
	}

	// 尝试获取 + 验证 + 重试
	for attempt := 0; attempt <= e.config.MaxRetries; attempt++ {
		var resp *llm.ChatResponse
		var err error

		if usedNative {
			// 使用带 response_format 的 Chat 方法
			rf := BuildResponseFormat(schema)
			if fc, ok := client.(FormatClient); ok {
				resp, err = fc.ChatWithFormat(ctx, workingMessages, tools, rf)
			} else {
				// 降级为 prompt-based
				usedNative = false
				workingMessages = injectSchemaPrompt(messages, schema)
				resp, err = client.Chat(ctx, workingMessages, tools)
			}
		} else {
			resp, err = client.Chat(ctx, workingMessages, tools)
		}

		if err != nil {
			return nil, fmt.Errorf("structured output: LLM call failed (attempt %d): %w", attempt+1, err)
		}

		// 如果 LLM 返回了工具调用，直接返回（结构化输出不适用于工具调用轮次）
		if len(resp.Message.ToolCalls) > 0 {
			return nil, fmt.Errorf("structured output: unexpected tool_calls in response, structured output requires direct text reply")
		}

		// 提取 JSON
		output := resp.Message.Content
		if e.config.StripMarkdown {
			output = StripMarkdownCodeBlock(output)
		}

		// 验证
		vr := schema.Validate([]byte(output))
		if vr.Valid {
			// 解析为 map
			var parsed map[string]any
			if err := json.Unmarshal([]byte(output), &parsed); err != nil {
				// 验证通过但解析失败（不应该发生），继续重试
				workingMessages = append(workingMessages, resp.Message)
				workingMessages = append(workingMessages, llm.Message{
					Role:    "user",
					Content: "Your response was not valid JSON. Please respond with valid JSON only.",
				})
				continue
			}

			// 构建更新后的 history
			newHistory := make([]llm.Message, len(messages))
			copy(newHistory, messages)
			newHistory = append(newHistory, resp.Message)

			return &StructuredChatResult{
				RawJSON:          output,
				Parsed:           parsed,
				Retries:          attempt,
				UsedNativeFormat: usedNative,
				History:          newHistory,
			}, nil
		}

		// 验证失败，准备重试
		if attempt < e.config.MaxRetries {
			workingMessages = append(workingMessages, resp.Message)
			workingMessages = append(workingMessages, llm.Message{
				Role:    "user",
				Content: retryPromptForErrors(vr),
			})
		} else {
			// 最后一次重试也失败了
			errMsgs := make([]string, len(vr.Errors))
			for i, e := range vr.Errors {
				errMsgs[i] = e.Error()
			}
			return nil, fmt.Errorf("structured output: validation failed after %d attempts. Errors: %s",
				e.config.MaxRetries+1, strings.Join(errMsgs, "; "))
		}
	}

	return nil, fmt.Errorf("structured output: unexpected exit from retry loop")
}

// ParseInto 将结构化输出解析到指定的 Go 结构体
func ParseInto[T any](result *StructuredChatResult) (*T, error) {
	var target T
	if err := json.Unmarshal([]byte(result.RawJSON), &target); err != nil {
		return nil, fmt.Errorf("parse structured output into %T: %w", target, err)
	}
	return &target, nil
}

// injectSchemaPrompt 在消息列表中注入 schema 描述 prompt
func injectSchemaPrompt(messages []llm.Message, schema *Schema) []llm.Message {
	result := make([]llm.Message, len(messages))
	copy(result, messages)

	suffix := schemaPromptSuffix(schema)

	// 策略：如果有 system prompt，追加到 system prompt 末尾
	// 否则，在用户消息前插入一条 system prompt
	for i, msg := range result {
		if msg.Role == "system" {
			result[i].Content = msg.Content + suffix
			return result
		}
	}

	// 没有 system prompt，插入到开头
	sysMsg := llm.Message{
		Role:    "system",
		Content: "You are a helpful assistant." + suffix,
	}
	result = append([]llm.Message{sysMsg}, result...)
	return result
}

// ---------- 扩展 Client 接口 ----------

// FormatClient 支持 response_format 的 LLM Client 扩展接口
type FormatClient interface {
	llm.Client
	ChatWithFormat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef, format *ResponseFormat) (*llm.ChatResponse, error)
}
