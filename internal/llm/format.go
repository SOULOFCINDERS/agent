package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ---------- Structured Output: response_format 支持 ----------
// ResponseFormat 和 ResponseJSONSchema 已在 client.go 中通过别名从 domain/conversation 引入

// chatRequestWithFormat 带 response_format 的请求体
type chatRequestWithFormat struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Tools          []ToolDef       `json:"tools,omitempty"`
	ToolChoice     string          `json:"tool_choice,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ChatWithFormat 带 response_format 的 Chat 方法
// 支持 OpenAI JSON Schema mode / JSON Object mode
func (c *OpenAICompatClient) ChatWithFormat(ctx context.Context, messages []Message, tools []ToolDef, format *ResponseFormat) (*ChatResponse, error) {
	reqBody := chatRequestWithFormat{
		Model:          c.Model,
		Messages:       messages,
		ResponseFormat: format,
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
