package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ---------- Streaming 类型 ----------

// StreamDelta 表示一个流式增量
type StreamDelta struct {
	Content      string          // 文本增量
	ToolCalls    []ToolCallDelta // 工具调用增量
	Done         bool            // 是否结束
	FinishReason string          // stop / tool_calls
	Usage        *Usage          // 最后一个 chunk 可能包含 usage
}

// ToolCallDelta 工具调用的增量（SSE 中分多次传递）
type ToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function,omitempty"`
}

// StreamReader 用于逐 chunk 读取流式响应
type StreamReader struct {
	reader  *bufio.Reader
	closer  io.Closer
	done    bool
}

// Recv 读取下一个 delta，返回 io.EOF 表示流结束
func (sr *StreamReader) Recv() (*StreamDelta, error) {
	if sr.done {
		return nil, io.EOF
	}

	for {
		line, err := sr.reader.ReadBytes('\n')
		if err != nil {
			sr.done = true
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("read stream: %w", err)
		}

		line = bytes.TrimSpace(line)

		// 空行分隔事件
		if len(line) == 0 {
			continue
		}

		// SSE 格式: "data: {...}"
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		data := bytes.TrimPrefix(line, []byte("data: "))

		// "[DONE]" 表示流结束
		if string(data) == "[DONE]" {
			sr.done = true
			return &StreamDelta{Done: true}, nil
		}

		// 解析 chunk
		var chunk sseChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			continue // 跳过无法解析的行
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		delta := &StreamDelta{
			Content:      choice.Delta.Content,
			FinishReason: choice.FinishReason,
		}

		if choice.FinishReason == "stop" || choice.FinishReason == "tool_calls" {
			delta.Done = true
			sr.done = true
		}

		// 处理 tool_calls delta
		if len(choice.Delta.ToolCalls) > 0 {
			delta.ToolCalls = choice.Delta.ToolCalls
		}

		// 解析 usage（流式最后一个 chunk 可能包含）
		if chunk.Usage != nil {
			delta.Usage = &Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}

		return delta, nil
	}
}

// Close 释放底层连接
func (sr *StreamReader) Close() error {
	sr.done = true
	if sr.closer != nil {
		return sr.closer.Close()
	}
	return nil
}

// Collect 收集所有 delta 直到结束，拼接为完整的 ChatResponse
// 用于工具调用场景（需要完整参数）
func (sr *StreamReader) Collect() (*ChatResponse, error) {
	var contentBuf strings.Builder
	toolCallMap := make(map[int]*ToolCall) // index -> accumulated tool call
	var finishReason string
	var lastUsage *Usage

	for {
		delta, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if delta.Content != "" {
			contentBuf.WriteString(delta.Content)
		}

		if delta.FinishReason != "" {
			finishReason = delta.FinishReason
		}
		if delta.Usage != nil {
			lastUsage = delta.Usage
		}

		// 累积 tool call deltas
		for _, tcd := range delta.ToolCalls {
			tc, ok := toolCallMap[tcd.Index]
			if !ok {
				tc = &ToolCall{
					ID:   tcd.ID,
					Type: tcd.Type,
				}
				tc.Function.Name = tcd.Function.Name
				toolCallMap[tcd.Index] = tc
			} else {
				if tcd.ID != "" {
					tc.ID = tcd.ID
				}
				if tcd.Type != "" {
					tc.Type = tcd.Type
				}
				if tcd.Function.Name != "" {
					tc.Function.Name = tcd.Function.Name
				}
			}
			tc.Function.Arguments += tcd.Function.Arguments
		}

		if delta.Done {
			break
		}
	}

	msg := Message{
		Role:    "assistant",
		Content: contentBuf.String(),
	}

	// 按 index 排序组装 tool calls
	if len(toolCallMap) > 0 {
		maxIdx := 0
		for idx := range toolCallMap {
			if idx > maxIdx {
				maxIdx = idx
			}
		}
		for i := 0; i <= maxIdx; i++ {
			if tc, ok := toolCallMap[i]; ok {
				msg.ToolCalls = append(msg.ToolCalls, *tc)
			}
		}
	}

	resp := &ChatResponse{
		Message:      msg,
		FinishReason: finishReason,
	}
	if lastUsage != nil {
		resp.Usage = *lastUsage
	}
	return resp, nil
}

// ---------- SSE 内部解析类型 ----------

type sseChunk struct {
	Choices []struct {
		Delta struct {
			Role      string          `json:"role,omitempty"`
			Content   string          `json:"content,omitempty"`
			ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

// ---------- StreamChat 实现 ----------

// StreamChat 发送流式请求，返回 StreamReader
func (c *OpenAICompatClient) StreamChat(ctx context.Context, messages []Message, tools []ToolDef) (*StreamReader, error) {
	reqBody := streamChatRequest{
		Model:    c.Model,
		Messages: messages,
		Stream:   true,
		StreamOptions: &streamOptions{IncludeUsage: true},
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
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LLM API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return &StreamReader{
		reader: bufio.NewReaderSize(resp.Body, 4096),
		closer: resp.Body,
	}, nil
}

type streamChatRequest struct {
	Model         string         `json:"model"`
	Messages      []Message      `json:"messages"`
	Tools         []ToolDef      `json:"tools,omitempty"`
	ToolChoice    string         `json:"tool_choice,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}
