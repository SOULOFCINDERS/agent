package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// StreamChat 模拟流式输出：逐字符发送
func (m *MockClient) StreamChat(ctx context.Context, messages []Message, tools []ToolDef) (*StreamReader, error) {
	// 先用普通 Chat 获取完整响应
	resp, err := m.Chat(ctx, messages, tools)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		if len(resp.Message.ToolCalls) > 0 {
			// 工具调用：发送完整的 tool_calls 事件
			mockWriteToolCallSSE(pw, resp)
		} else {
			// 文本回复：逐字模拟流式
			runes := []rune(resp.Message.Content)
			chunkSize := 3

			for i := 0; i < len(runes); i += chunkSize {
				select {
				case <-ctx.Done():
					return
				default:
				}

				end := i + chunkSize
				if end > len(runes) {
					end = len(runes)
				}
				chunk := string(runes[i:end])
				mockWriteDeltaSSE(pw, chunk, "")
				time.Sleep(15 * time.Millisecond)
			}
			// 最后一个 delta 带 finish_reason
			mockWriteDeltaSSE(pw, "", "stop")
		}

		fmt.Fprintf(pw, "data: [DONE]\n\n")
	}()

	return &StreamReader{
		reader: bufio.NewReaderSize(pr, 4096),
		closer: pr,
	}, nil
}

func mockWriteDeltaSSE(w io.Writer, content string, finishReason string) {
	chunk := sseChunk{}
	choice := struct {
		Delta struct {
			Role      string          `json:"role,omitempty"`
			Content   string          `json:"content,omitempty"`
			ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	}{}
	choice.Delta.Content = content
	if finishReason != "" {
		choice.FinishReason = finishReason
	}
	chunk.Choices = append(chunk.Choices, choice)
	b, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func mockWriteToolCallSSE(w io.Writer, resp *ChatResponse) {
	chunk := sseChunk{}
	choice := struct {
		Delta struct {
			Role      string          `json:"role,omitempty"`
			Content   string          `json:"content,omitempty"`
			ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	}{}

	for i, tc := range resp.Message.ToolCalls {
		choice.Delta.ToolCalls = append(choice.Delta.ToolCalls, ToolCallDelta{
			Index: i,
			ID:    tc.ID,
			Type:  tc.Type,
		})
		choice.Delta.ToolCalls[i].Function.Name = tc.Function.Name
		choice.Delta.ToolCalls[i].Function.Arguments = tc.Function.Arguments
	}
	choice.FinishReason = "tool_calls"
	chunk.Choices = append(chunk.Choices, choice)
	b, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", b)
}
