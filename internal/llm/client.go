package llm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ---------- 通用类型 ----------

// ToolDef 描述一个工具，供 LLM function calling 使用
type ToolDef struct {
	Type     string   `json:"type"` // "function"
	Function FuncDef  `json:"function"`
}

type FuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Message 表示一条对话消息
type Message struct {
	Role       string      `json:"role"`                  // system / user / assistant / tool
	Content    string      `json:"content,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`  // assistant 返回的工具调用
	ToolCallID string      `json:"tool_call_id,omitempty"` // role=tool 时必填
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ChatResponse 表示 LLM 返回
type ChatResponse struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"` // "stop" / "tool_calls"
	Usage        Usage   `json:"usage"`
}

// ---------- Client 接口 ----------

type Client interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDef) (*ChatResponse, error)
}

// ---------- OpenAI 兼容实现 ----------

type OpenAICompatClient struct {
	BaseURL string
	APIKey  string
	Model   string
	client  *http.Client
}

// NewOpenAICompatClient 创建一个兼容 OpenAI 协议的客户端
// 支持 DeepSeek / Qwen / SiliconFlow / Ollama 等
func NewOpenAICompatClient(baseURL, apiKey, model string) *OpenAICompatClient {
	if baseURL == "" {
		baseURL = os.Getenv("LLM_BASE_URL")
	}
	if apiKey == "" {
		apiKey = os.Getenv("LLM_API_KEY")
	}
	if model == "" {
		model = os.Getenv("LLM_MODEL")
	}
	// 默认值：Ollama 本地
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	if apiKey == "" {
		apiKey = "ollama"
	}
	if model == "" {
		model = "qwen2.5:14b"
	}

	httpClient := &http.Client{Timeout: 120 * time.Second}
	// 如果设置了 LLM_SKIP_TLS=1，跳过 TLS 验证（仅限开发环境）
	if os.Getenv("LLM_SKIP_TLS") == "1" {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	return &OpenAICompatClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		client:  httpClient,
	}
}

// openAI 请求体
type chatRequest struct {
	Model      string    `json:"model"`
	Messages   []Message `json:"messages"`
	Tools      []ToolDef `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
}

// openAI 响应体
type chatResponseRaw struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *OpenAICompatClient) Chat(ctx context.Context, messages []Message, tools []ToolDef) (*ChatResponse, error) {
	reqBody := chatRequest{
		Model:    c.Model,
		Messages: messages,
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
		reqBody.ToolChoice = "auto"
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var raw chatResponseRaw
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w (body: %s)", err, string(respBody))
	}

	if raw.Error != nil {
		return nil, fmt.Errorf("LLM API error: %s", raw.Error.Message)
	}

	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("LLM returned no choices")
	}

	choice := raw.Choices[0]
	chatResp := &ChatResponse{
		Message:      choice.Message,
		FinishReason: choice.FinishReason,
	}
	if raw.Usage != nil {
		chatResp.Usage = Usage{
			PromptTokens:     raw.Usage.PromptTokens,
			CompletionTokens: raw.Usage.CompletionTokens,
			TotalTokens:      raw.Usage.TotalTokens,
		}
	}
	return chatResp, nil
}
