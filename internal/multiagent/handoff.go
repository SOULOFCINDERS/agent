package multiagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// HandoffTool 将一个子 Agent 封装为 Tool
//
// 当编排 Agent 决定委派任务时，它通过 tool call 调用 HandoffTool，
// HandoffTool 内部会创建子 Agent 实例、运行对话循环，并返回结果。
//
// 这是整个 Multi-Agent 的关键桥接层：
//   编排 Agent (LLM) --[tool_call]--> HandoffTool --[创建]--> 子 Agent --[LLM 循环]--> 结果
//
// 与普通 Tool 的区别：
//   普通 Tool: Execute(args) → 确定性结果
//   HandoffTool: Execute(args) → 启动一个完整的 LLM 对话循环 → 非确定性结果
type HandoffTool struct {
	def        AgentDef
	llmClient  llm.Client
	globalReg  *tools.Registry
	traceW     io.Writer
}

// NewHandoffTool 创建一个委派工具
func NewHandoffTool(def AgentDef, client llm.Client, globalReg *tools.Registry, traceW io.Writer) *HandoffTool {
	if traceW == nil {
		traceW = io.Discard
	}
	return &HandoffTool{
		def:       def,
		llmClient: client,
		globalReg: globalReg,
		traceW:    traceW,
	}
}

// Name 返回工具名（即 Agent 名）
func (h *HandoffTool) Name() string {
	return h.def.Name
}

// Execute 执行委派
// args 中必须包含 "task" 字段，描述要委派的任务
// 可选 "context" 字段，传递上下文信息
func (h *HandoffTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	task, _ := args["task"].(string)
	if task == "" {
		return nil, fmt.Errorf("handoff requires 'task' field describing what to do")
	}

	// 可选：上下文信息（编排 Agent 传递的摘要/前文）
	extraContext, _ := args["context"].(string)

	// 构建用户消息：任务 + 上下文
	var userMsg strings.Builder
	if extraContext != "" {
		userMsg.WriteString("## 背景信息\n")
		userMsg.WriteString(extraContext)
		userMsg.WriteString("\n\n")
	}
	userMsg.WriteString("## 任务\n")
	userMsg.WriteString(task)

	// 创建子 Agent 的工具子集 Registry
	subReg := h.def.SubRegistry(h.globalReg)

	// 创建子 Agent 实例（无记忆、无压缩器——子 Agent 是短命的）
	subAgent := agent.NewLoopAgent(
		h.llmClient,
		subReg,
		h.def.SystemPrompt,
		h.traceW,
		nil, // 无记忆
		nil, // 无压缩器
	)

	// 空历史：子 Agent 每次从零开始
	var history []llm.Message

	// 运行子 Agent
	reply, _, err := subAgent.Chat(ctx, userMsg.String(), history)
	if err != nil {
		return nil, fmt.Errorf("sub-agent %q failed: %w", h.def.Name, err)
	}

	return reply, nil
}

// Schema 返回 HandoffTool 的 JSON Schema（供 LLM function calling 使用）
func (h *HandoffTool) Schema() struct {
	Desc   string
	Schema json.RawMessage
} {
	return struct {
		Desc   string
		Schema json.RawMessage
	}{
		Desc: fmt.Sprintf("委派任务给「%s」: %s", h.def.Name, h.def.Description),
		Schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"task": {
					"type": "string",
					"description": "要委派的具体任务描述（越详细越好）"
				},
				"context": {
					"type": "string",
					"description": "可选的背景信息，帮助子Agent更好地完成任务"
				}
			},
			"required": ["task"]
		}`),
	}
}
