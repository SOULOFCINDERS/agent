package structured

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ---------- Middleware: LoopAgent 集成层 ----------

// Middleware 结构化输出中间件
// 提供面向 LoopAgent 的简化 API
type Middleware struct {
	engine *Engine
	trace  io.Writer
}

// NewMiddleware 创建结构化输出中间件
func NewMiddleware(config Config, trace io.Writer) *Middleware {
	if trace == nil {
		trace = io.Discard
	}
	return &Middleware{
		engine: NewEngine(config),
		trace:  trace,
	}
}

// StructuredChat 执行结构化对话
// 这是给 LoopAgent 集成用的主入口
func (m *Middleware) StructuredChat(ctx context.Context, client llm.Client, userMessage string, history []llm.Message, systemPrompt string) (*StructuredChatResult, error) {
	// 构建消息
	messages := make([]llm.Message, 0, len(history)+2)

	if len(history) == 0 && systemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: systemPrompt,
		})
	} else {
		messages = append(messages, history...)
	}

	messages = append(messages, llm.Message{
		Role:    "user",
		Content: userMessage,
	})

	m.traceLog("structured_chat_start", map[string]any{
		"schema":       m.engine.config.Schema.Name,
		"native_format": m.engine.config.UseNativeFormat,
		"max_retries":  m.engine.config.MaxRetries,
	})

	result, err := m.engine.Chat(ctx, client, messages, nil)
	if err != nil {
		m.traceLog("structured_chat_error", map[string]any{"error": err.Error()})
		return nil, err
	}

	m.traceLog("structured_chat_done", map[string]any{
		"retries":       result.Retries,
		"used_native":   result.UsedNativeFormat,
		"json_len":      len(result.RawJSON),
	})

	return result, nil
}

// QuickParse 快速结构化对话并解析到目标类型
// 一步完成：发送 → 验证 → 解析
func QuickParse[T any](ctx context.Context, client llm.Client, schema *Schema, userMessage string, systemPrompt string) (*T, error) {
	config := DefaultConfig(schema)
	engine := NewEngine(config)

	messages := []llm.Message{}
	if systemPrompt != "" {
		messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, llm.Message{Role: "user", Content: userMessage})

	result, err := engine.Chat(ctx, client, messages, nil)
	if err != nil {
		return nil, err
	}

	return ParseInto[T](result)
}

func (m *Middleware) traceLog(event string, data map[string]any) {
	if m.trace == io.Discard {
		return
	}
	entry := map[string]any{
		"at":    time.Now().Format(time.RFC3339),
		"event": event,
	}
	for k, v := range data {
		entry[k] = v
	}
	b, _ := json.Marshal(entry)
	fmt.Fprintf(m.trace, "%s\n", b)
}

// ---------- 预设 Schema 工厂 ----------

// PlanSchema 创建一个"执行计划"的 schema
// 用于让 LLM 输出结构化的 step-by-step 计划
func PlanSchema() *Schema {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"goal": {
				"type": "string",
				"description": "The overall goal to achieve"
			},
			"steps": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"action": {
							"type": "string",
							"description": "The action to take",
							"enum": ["tool_call", "think", "respond"]
						},
						"tool": {
							"type": "string",
							"description": "Tool name (if action is tool_call)"
						},
						"args": {
							"type": "object",
							"description": "Tool arguments (if action is tool_call)"
						},
						"reasoning": {
							"type": "string",
							"description": "Why this step is needed"
						}
					},
					"required": ["action", "reasoning"],
					"additionalProperties": false
				}
			},
			"estimated_steps": {
				"type": "integer",
				"description": "Estimated total number of steps"
			}
		},
		"required": ["goal", "steps", "estimated_steps"],
		"additionalProperties": false
	}`)
	s, _ := NewSchema("execution_plan", "A structured execution plan with steps", raw)
	return s
}

// ClassificationSchema 创建分类任务的 schema
func ClassificationSchema(categories []string) *Schema {
	enumJSON, _ := json.Marshal(categories)
	raw := json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"category": {
				"type": "string",
				"description": "The classification category",
				"enum": %s
			},
			"confidence": {
				"type": "number",
				"description": "Confidence score between 0 and 1"
			},
			"reasoning": {
				"type": "string",
				"description": "Brief explanation for the classification"
			}
		},
		"required": ["category", "confidence", "reasoning"],
		"additionalProperties": false
	}`, string(enumJSON)))
	s, _ := NewSchema("classification", "A classification result", raw)
	return s
}

// ExtractionSchema 创建信息抽取 schema
// fields: map[字段名]字段描述
func ExtractionSchema(name string, fields map[string]string) *Schema {
	props := make(map[string]any)
	required := make([]string, 0, len(fields))

	for fieldName, desc := range fields {
		props[fieldName] = map[string]any{
			"type":        "string",
			"description": desc,
		}
		required = append(required, fieldName)
	}

	schemaMap := map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             required,
		"additionalProperties": false,
	}

	raw, _ := json.Marshal(schemaMap)
	s, _ := NewSchema(name, "Extract structured information", raw)
	return s
}

// SentimentSchema 创建情感分析 schema
func SentimentSchema() *Schema {
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"sentiment": {
				"type": "string",
				"enum": ["positive", "negative", "neutral", "mixed"],
				"description": "Overall sentiment"
			},
			"score": {
				"type": "number",
				"description": "Sentiment score from -1 (very negative) to 1 (very positive)"
			},
			"aspects": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"aspect": { "type": "string" },
						"sentiment": {
							"type": "string",
							"enum": ["positive", "negative", "neutral"]
						}
					},
					"required": ["aspect", "sentiment"],
					"additionalProperties": false
				},
				"description": "Aspect-level sentiment analysis"
			}
		},
		"required": ["sentiment", "score"],
		"additionalProperties": false
	}`)
	s, _ := NewSchema("sentiment_analysis", "Sentiment analysis result", raw)
	return s
}
