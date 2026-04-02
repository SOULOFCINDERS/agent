package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MockClient 是一个用于演示和测试的 Mock LLM
// 它模拟 LLM 的 Function Calling 行为：分析用户输入 → 选择工具 → 处理结果
type MockClient struct {
	tools    []ToolDef
	callID   int
	// pending 表示是否有工具调用结果等待处理
	// 如果上一轮返回了 tool_calls，这一轮收到 tool result 后应生成最终回复
}

func NewMockClient() *MockClient {
	return &MockClient{}
}

func (m *MockClient) Chat(ctx context.Context, messages []Message, tools []ToolDef) (*ChatResponse, error) {
	m.tools = tools

	// 找到最新的用户消息或工具结果
	lastMsg := messages[len(messages)-1]

	// 如果上一条是 tool result，生成最终回复
	if lastMsg.Role == "tool" {
		return m.generateFinalAnswer(messages)
	}

	// 如果是用户消息，分析是否需要调用工具
	if lastMsg.Role == "user" {
		return m.analyzeAndRespond(lastMsg.Content)
	}

	return &ChatResponse{
		Message:      Message{Role: "assistant", Content: "你好！有什么可以帮你的？"},
		FinishReason: "stop",
	}, nil
}

func (m *MockClient) analyzeAndRespond(input string) (*ChatResponse, error) {
	lo := strings.ToLower(input)

	// 匹配工具调用意图
	type toolMatch struct {
		name string
		args map[string]any
	}

	var match *toolMatch

	switch {
	case strings.Contains(lo, "列出") || strings.Contains(lo, "list") || strings.Contains(lo, "ls") || strings.Contains(lo, "目录"):
		path := "."
		// 尝试提取路径
		for _, w := range strings.Fields(input) {
			if strings.Contains(w, "/") || w == "." || w == ".." {
				path = w
				break
			}
		}
		match = &toolMatch{name: "list_dir", args: map[string]any{"path": path}}

	case strings.Contains(lo, "读") || strings.Contains(lo, "read") || strings.Contains(lo, "查看"):
		path := ""
		lines := 20
		for _, w := range strings.Fields(input) {
			if strings.Contains(w, ".") && (strings.Contains(w, "/") || !strings.Contains(w, "。")) {
				path = w
			}
		}
		if path == "" {
			path = "README.md"
		}
		match = &toolMatch{name: "read_file", args: map[string]any{"path": path, "lines": lines}}

	case strings.Contains(lo, "搜索") || strings.Contains(lo, "search") || strings.Contains(lo, "grep") || strings.Contains(lo, "查找"):
		pattern := ""
		// 提取引号内的内容作为 pattern
		if idx := strings.Index(input, "\""); idx >= 0 {
			end := strings.Index(input[idx+1:], "\"")
			if end >= 0 {
				pattern = input[idx+1 : idx+1+end]
			}
		}
		if pattern == "" {
			// 取最后一个词
			fields := strings.Fields(input)
			if len(fields) > 0 {
				pattern = fields[len(fields)-1]
			}
		}
		match = &toolMatch{name: "grep_repo", args: map[string]any{"pattern": pattern}}

	case strings.Contains(lo, "计算") || strings.Contains(lo, "calc") || looksLikeExpr(lo):
		expr := input
		// 尝试提取表达式
		for _, prefix := range []string{"计算", "算一下", "calc", "calculate"} {
			if idx := strings.Index(lo, prefix); idx >= 0 {
				expr = strings.TrimSpace(input[idx+len(prefix):])
				break
			}
		}
		match = &toolMatch{name: "calc", args: map[string]any{"expr": expr}}

	case strings.Contains(lo, "天气") || strings.Contains(lo, "weather"):
		location := "Beijing"
		for _, city := range []string{"北京", "上海", "深圳", "杭州", "成都", "广州"} {
			if strings.Contains(input, city) {
				location = city
				break
			}
		}
		// english cities
		for _, city := range []string{"Beijing", "Shanghai", "Shenzhen", "Tokyo", "London", "NewYork"} {
			if strings.Contains(lo, strings.ToLower(city)) {
				location = city
				break
			}
		}
		match = &toolMatch{name: "weather", args: map[string]any{"location": location}}

	case strings.Contains(lo, "搜索") || strings.Contains(lo, "search") || strings.Contains(lo, "查一下") || strings.Contains(lo, "搜一下") || strings.Contains(lo, "最新"):
		// 提取搜索关键词
		query := input
		for _, prefix := range []string{"搜索", "搜一下", "查一下", "帮我搜", "search"} {
			if idx := strings.Index(lo, prefix); idx >= 0 {
				query = strings.TrimSpace(input[idx+len(prefix):])
				break
			}
		}
		if query == "" {
			query = input
		}
		match = &toolMatch{name: "web_search", args: map[string]any{"query": query}}
	}

	if match != nil {
		m.callID++
		argsJSON, _ := json.Marshal(match.args)
		return &ChatResponse{
			Message: Message{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{
						ID:   fmt.Sprintf("call_%d", m.callID),
						Type: "function",
						Function: FunctionCall{
							Name:      match.name,
							Arguments: string(argsJSON),
						},
					},
				},
			},
			FinishReason: "tool_calls",
		}, nil
	}

	// 不需要工具，直接回复
	return &ChatResponse{
		Message: Message{
			Role:    "assistant",
			Content: fmt.Sprintf("你好！你说的是「%s」。\n\n我可以帮你：\n• 列出目录（试试：列出当前目录）\n• 读取文件（试试：读一下 README.md）\n• 搜索代码（试试：搜索 \"func main\"）\n• 计算表达式（试试：计算 (1+2)*3）\n• 查天气（试试：北京天气）", input),
		},
		FinishReason: "stop",
	}, nil
}

func (m *MockClient) generateFinalAnswer(messages []Message) (*ChatResponse, error) {
	// 收集所有最近的 tool results
	var toolResults []string
	var toolName string
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "tool" {
			toolResults = append([]string{msg.Content}, toolResults...)
		} else if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			toolName = msg.ToolCalls[0].Function.Name
			break
		}
	}

	result := strings.Join(toolResults, "\n")

	// 根据工具类型生成不同的总结
	var reply string
	switch toolName {
	case "list_dir":
		files := strings.Split(result, "\n")
		reply = fmt.Sprintf("📂 目录下共有 **%d** 个文件/目录：\n\n```\n%s\n```", len(files), result)
	case "read_file":
		lines := strings.Split(result, "\n")
		preview := result
		if len(lines) > 10 {
			preview = strings.Join(lines[:10], "\n") + "\n..."
		}
		reply = fmt.Sprintf("📄 文件内容（%d 行）：\n\n```\n%s\n```", len(lines), preview)
	case "grep_repo":
		if result == "no matches" {
			reply = "🔍 没有找到匹配的结果。"
		} else {
			matches := strings.Split(result, "\n")
			reply = fmt.Sprintf("🔍 找到 **%d** 条匹配：\n\n```\n%s\n```", len(matches), result)
		}
	case "calc":
		reply = fmt.Sprintf("🧮 计算结果：**%s**", result)
	case "weather":
		reply = fmt.Sprintf("🌤️ %s", result)
	case "web_search":
		reply = fmt.Sprintf("🔍 搜索结果：\n\n%s", result)
	case "web_fetch":
		lines := strings.Split(result, "\n")
		preview := result
		if len(lines) > 15 {
			preview = strings.Join(lines[:15], "\n") + "\n..."
		}
		reply = fmt.Sprintf("🌐 网页内容：\n\n%s", preview)
	default:
		reply = fmt.Sprintf("执行结果：\n%s", result)
	}

	return &ChatResponse{
		Message:      Message{Role: "assistant", Content: reply},
		FinishReason: "stop",
	}, nil
}

func looksLikeExpr(s string) bool {
	hasDigit := false
	hasOp := false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			hasDigit = true
		}
		if r == '+' || r == '*' || r == '/' {
			hasOp = true
		}
	}
	return hasDigit && hasOp && len(s) < 50
}
