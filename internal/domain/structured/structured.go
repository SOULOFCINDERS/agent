package structured

import (
	"context"
	"encoding/json"

	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
)

// Schema 输出结构定义
type Schema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Fields      []Field         `json:"fields"`
	Strict      bool            `json:"strict"`
	RawSchema   json.RawMessage `json:"raw_schema,omitempty"`
}

// Field schema 字段
type Field struct {
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Description string          `json:"description,omitempty"`
	Required    bool            `json:"required"`
	Properties  []Field         `json:"properties,omitempty"`
	Items       *Field          `json:"items,omitempty"`
	Enum        []string        `json:"enum,omitempty"`
	RawSchema   json.RawMessage `json:"raw_schema,omitempty"`
}

// Config 结构化输出配置
type Config struct {
	Schema          *Schema
	MaxRetries      int
	UseNativeFormat bool
	StripMarkdown   bool
}

// Extractor 结构化输出提取器接口（Domain 层定义）
type Extractor interface {
	Extract(ctx context.Context, messages []conv.Message, cfg Config) (json.RawMessage, error)
}
