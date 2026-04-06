// Package llm provides the LLM client implementation compatible with the OpenAI chat completions API.
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

	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
)

// ---------- 类型别名：从 domain/conversation 引入 ----------
// 保持向后兼容，所有 llm.Message、llm.ToolDef 等引用继续工作

type Message = conv.Message
type ToolCall = conv.ToolCall
type FunctionCall = conv.FunctionCall
type ToolDef = conv.ToolDef
type FuncDef = conv.FuncDef
type ChatResponse = conv.ChatResponse
type ResponseFormat = conv.ResponseFormat
type ResponseJSONSchema = conv.ResponseJSONSchema

// ---------- Client 接口 ----------

type Client = conv.Client

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
