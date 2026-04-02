package multiagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/memory"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// Orchestrator 多 Agent 编排器
//
// ## 工作原理
//
// Orchestrator 本身也是一个 LoopAgent（编排 Agent），但它的工具列表
// 包含了一组 HandoffTool（委派工具），每个 HandoffTool 对应一个子 Agent。
//
// 当用户发送请求时：
//
//   1. 编排 Agent 收到用户消息
//   2. 编排 Agent 的 LLM 分析任务，决定：
//      a) 自己直接回答（简单问题）
//      b) 委派给某个子 Agent（通过 tool call）
//      c) 先委派给 A，拿到结果后再委派给 B（串行编排）
//      d) 同时委派给多个子 Agent（并行编排，利用已有的 parallel.go）
//   3. 子 Agent 执行完毕，结果作为 tool result 返回给编排 Agent
//   4. 编排 Agent 整合结果，返回最终回复
//
// ## 为什么不用静态 DAG?
//
// 静态 DAG（预定义任务流）需要提前知道任务分解方式，无法处理开放域问题。
// LLM 驱动的编排让系统能根据任务动态决定调用哪些子 Agent、以什么顺序调用。
//
// ## Token 流向
//
//   用户 → 编排 Agent (LLM call #1)
//                ↓ tool_call: research_agent(task="xxx")
//          子 Agent: research (LLM call #2, #3, ...)
//                ↓ tool_result: "搜索结果..."
//          编排 Agent (LLM call #4)
//                ↓ tool_call: writer_agent(task="用上述结果写文档")
//          子 Agent: writer (LLM call #5, #6, ...)
//                ↓ tool_result: "文档已创建..."
//          编排 Agent (LLM call #7) → 最终回复
//
type Orchestrator struct {
	orchestratorAgent *agent.LoopAgent
	agentDefs         []AgentDef
	handoffTools      []*HandoffTool
	traceW            io.Writer
}

// OrchestratorConfig 编排器配置
type OrchestratorConfig struct {
	// LLMClient 共享的 LLM 客户端（编排 Agent 和子 Agent 共用）
	LLMClient llm.Client

	// GlobalRegistry 全局工具注册表（子 Agent 从中选取子集）
	GlobalRegistry *tools.Registry

	// AgentDefs 子 Agent 定义列表
	AgentDefs []AgentDef

	// OrchestratorPrompt 编排 Agent 的 system prompt（可选，有默认值）
	OrchestratorPrompt string

	// DirectTools 编排 Agent 自己可以直接使用的工具名
	// 对于简单任务，编排 Agent 可以直接调用这些工具，无需委派
	DirectTools []string

	// MemStore 记忆存储（可选）
	MemStore *memory.Store

	// Compressor 历史压缩器（可选）
	Compressor *memory.Compressor

	// TraceWriter trace 输出
	TraceWriter io.Writer
}

// NewOrchestrator 创建多 Agent 编排器
func NewOrchestrator(cfg OrchestratorConfig) (*Orchestrator, error) {
	if cfg.LLMClient == nil {
		return nil, fmt.Errorf("LLMClient is required")
	}
	if cfg.GlobalRegistry == nil {
		return nil, fmt.Errorf("GlobalRegistry is required")
	}
	if len(cfg.AgentDefs) == 0 {
		return nil, fmt.Errorf("at least one AgentDef is required")
	}
	if cfg.TraceWriter == nil {
		cfg.TraceWriter = io.Discard
	}

	// 校验所有 Agent 定义
	for _, def := range cfg.AgentDefs {
		if err := def.Validate(); err != nil {
			return nil, fmt.Errorf("invalid agent %q: %w", def.Name, err)
		}
	}

	// 检查名称唯一性
	seen := map[string]bool{}
	for _, def := range cfg.AgentDefs {
		if seen[def.Name] {
			return nil, fmt.Errorf("duplicate agent name: %q", def.Name)
		}
		seen[def.Name] = true
	}

	// 为编排 Agent 创建工具注册表
	orchReg := tools.NewRegistry()

	// 1. 注册编排 Agent 的直接工具
	for _, name := range cfg.DirectTools {
		if t := cfg.GlobalRegistry.Get(name); t != nil {
			orchReg.Register(t)
		}
	}

	// 2. 创建 HandoffTool 并注册
	var handoffTools []*HandoffTool
	for _, def := range cfg.AgentDefs {
		ht := NewHandoffTool(def, cfg.LLMClient, cfg.GlobalRegistry, cfg.TraceWriter)
		handoffTools = append(handoffTools, ht)
		orchReg.Register(ht)
	}

	// 3. 生成编排 Agent 的 system prompt
	orchPrompt := cfg.OrchestratorPrompt
	if orchPrompt == "" {
		orchPrompt = buildDefaultOrchestratorPrompt(cfg.AgentDefs, cfg.DirectTools)
	}

	// 4. 创建编排 Agent
	orchAgent := agent.NewLoopAgent(
		cfg.LLMClient,
		orchReg,
		orchPrompt,
		cfg.TraceWriter,
		cfg.MemStore,
		cfg.Compressor,
	)

	// 5. 注入 handoff tools 的 schema
	//    因为 HandoffTool 不在 BuiltinSchemas 中，需要手动注入
	orchAgent.InjectToolDefs(buildHandoffToolDefs(handoffTools))

	return &Orchestrator{
		orchestratorAgent: orchAgent,
		agentDefs:         cfg.AgentDefs,
		handoffTools:      handoffTools,
		traceW:            cfg.TraceWriter,
	}, nil
}

// Chat 执行多 Agent 对话
func (o *Orchestrator) Chat(ctx context.Context, userMessage string, history []llm.Message) (string, []llm.Message, error) {
	o.traceLogOrch("orchestrator_start", map[string]any{
		"user_message_len": len(userMessage),
		"agents":           len(o.agentDefs),
	})

	start := time.Now()
	reply, newHistory, err := o.orchestratorAgent.Chat(ctx, userMessage, history)

	o.traceLogOrch("orchestrator_done", map[string]any{
		"elapsed": time.Since(start).String(),
		"reply_len": len(reply),
		"error":   fmt.Sprint(err),
	})

	return reply, newHistory, err
}

// ChatStream 流式版本
func (o *Orchestrator) ChatStream(ctx context.Context, userMessage string, history []llm.Message, onDelta agent.StreamWriter) (string, []llm.Message, error) {
	return o.orchestratorAgent.ChatStream(ctx, userMessage, history, onDelta)
}

// GetAgent 返回编排 Agent（用于设置 usageTracker、ctxManager 等）
func (o *Orchestrator) GetAgent() *agent.LoopAgent {
	return o.orchestratorAgent
}

// AgentDefs 返回所有子 Agent 定义
func (o *Orchestrator) AgentDefs() []AgentDef {
	return o.agentDefs
}

func (o *Orchestrator) traceLogOrch(event string, data map[string]any) {
	if o.traceW == io.Discard {
		return
	}
	entry := map[string]any{
		"at":    time.Now().Format(time.RFC3339),
		"event": event,
		"layer": "orchestrator",
	}
	for k, v := range data {
		entry[k] = v
	}
	b, _ := json.Marshal(entry)
	fmt.Fprintf(o.traceW, "%s\n", b)
}

// buildDefaultOrchestratorPrompt 生成编排 Agent 的默认 system prompt
func buildDefaultOrchestratorPrompt(agents []AgentDef, directTools []string) string {
	var b strings.Builder

	b.WriteString(`你是一个任务编排助手。你的职责是理解用户需求，并决定如何完成任务。

## 你的工作方式

你有两种方式完成任务：

### 方式一：直接回答
对于简单的问题（闲聊、常识问答），直接回答即可。
`)

	if len(directTools) > 0 {
		b.WriteString("你也可以直接使用以下工具：")
		b.WriteString(strings.Join(directTools, "、"))
		b.WriteString("\n")
	}

	b.WriteString(`
### 方式二：委派给专业 Agent
对于复杂任务，委派给以下专业 Agent：

`)

	for _, a := range agents {
		b.WriteString(fmt.Sprintf("- **%s**: %s\n", a.Name, a.Description))
	}

	b.WriteString(`
## 委派原则

1. **任务描述要具体**：在 task 参数中详细描述要做什么，越具体越好
2. **提供上下文**：如果有相关背景信息，通过 context 参数传递
3. **可以串行委派**：先委派给 A 搜索资料，拿到结果后委派给 B 写文档
4. **可以并行委派**：如果两个子任务互不依赖，可以同时委派
5. **整合结果**：收到子 Agent 结果后，整理成清晰的最终回复

## 注意事项

- 不要过度委派：简单问题直接回答
- 不要重复委派：同一任务不要发给多个 Agent
- 委派失败时，尝试换一个 Agent 或自己回答
`)

	return b.String()
}

// buildHandoffToolDefs 为 handoff tools 构建 LLM tool definitions
func buildHandoffToolDefs(hts []*HandoffTool) []llm.ToolDef {
	var defs []llm.ToolDef
	for _, ht := range hts {
		s := ht.Schema()
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.FuncDef{
				Name:        ht.Name(),
				Description: s.Desc,
				Parameters:  s.Schema,
			},
		})
	}
	return defs
}
