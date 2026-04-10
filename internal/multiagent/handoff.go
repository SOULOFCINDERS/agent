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

// 默认结果 token 上限（约 2000 字符）
const defaultMaxResultTokens = 1000

// HandoffTool 将一个子 Agent 封装为 Tool
//
// 当编排 Agent 决定委派任务时，它通过 tool call 调用 HandoffTool，
// HandoffTool 内部会创建子 Agent 实例、运行对话循环，并返回结果。
//
// 上下文污染防护措施：
//   - 改进1: compactResult 对超长结果进行摘要/截断
//   - 改进3: 注入 UsageTracker 实现 token 预算控制
//   - 改进4: filterContext 按白名单过滤上下文
type HandoffTool struct {
	def       AgentDef
	llmClient llm.Client
	globalReg *tools.Registry
	traceW    io.Writer

	// maxResultTokens 子 Agent 返回结果的最大 token 数
	// 超过此限制会被截断并附加摘要标记
	// 0 表示使用默认值 defaultMaxResultTokens
	maxResultTokens int
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

// SetMaxResultTokens 设置结果截断阈值（0 表示使用默认值）
func (h *HandoffTool) SetMaxResultTokens(n int) {
	h.maxResultTokens = n
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

	// --- 改进4: 上下文白名单过滤 ---
	extraContext = filterContext(extraContext, h.def.AcceptContext)

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

	// --- 改进3: 注入 UsageTracker 实现 token 预算控制 ---
	if h.def.MaxTokenBudget > 0 {
		ut := llm.NewUsageTracker(h.def.MaxTokenBudget)
		subAgent.SetUsageTracker(ut)
	}

	// 空历史：子 Agent 每次从零开始
	var history []llm.Message

	// 运行子 Agent
	reply, _, err := subAgent.Chat(ctx, userMsg.String(), history)
	if err != nil {
		// 如果是 budget exceeded，仍然返回部分结果
		if strings.Contains(err.Error(), "budget") && reply != "" {
			reply = reply + "\n\n[注意: 子Agent已达到token预算上限，结果可能不完整]"
		} else {
			return nil, fmt.Errorf("sub-agent %q failed: %w", h.def.Name, err)
		}
	}

	// --- 改进1: 结果摘要/截断 ---
	reply = compactResult(reply, h.effectiveMaxResultTokens())

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

// effectiveMaxResultTokens 返回实际生效的结果 token 上限
func (h *HandoffTool) effectiveMaxResultTokens() int {
	if h.maxResultTokens > 0 {
		return h.maxResultTokens
	}
	return defaultMaxResultTokens
}

// ============================================================
// 改进1: compactResult — 子 Agent 结果摘要/截断
// ============================================================

// compactResult 对超长的子 Agent 结果进行截断
//
// 策略：
//   - 如果结果 token 数 <= maxTokens，原样返回
//   - 否则，保留前 60% + 后 20% 的内容，中间用省略标记替代
//   - 这比简单截断更好：保留了开头（通常是结论）和结尾（通常是总结）
//
// 注意：此处不调用 LLM 做摘要，避免额外开销。如果需要 LLM 摘要，
// 应在 Orchestrator 层通过 Compressor 处理。
func compactResult(result string, maxTokens int) string {
	if maxTokens <= 0 {
		return result
	}

	tokens := estimateStringTokens(result)
	if tokens <= maxTokens {
		return result
	}

	// 按字符比例截断（2 chars ≈ 1 token）
	maxChars := maxTokens * 2
	if maxChars >= len(result) {
		return result
	}

	// 保留前 60% + 后 20%，中间省略
	headChars := maxChars * 60 / 100
	tailChars := maxChars * 20 / 100

	// 确保不越界
	if headChars+tailChars >= len(result) {
		return result
	}

	head := result[:headChars]
	tail := result[len(result)-tailChars:]

	omitted := tokens - maxTokens
	return fmt.Sprintf("%s\n\n... [已省略约 %d tokens] ...\n\n%s", head, omitted, tail)
}

// estimateStringTokens 估算字符串的 token 数（2 字符 ≈ 1 token）
func estimateStringTokens(s string) int {
	n := len(s)
	if n == 0 {
		return 0
	}
	return (n + 1) / 2
}

// ============================================================
// 改进4: filterContext — 上下文白名单过滤
// ============================================================

// filterContext 按白名单过滤上下文内容
//
// 上下文被按行解析，每行格式为 "key: value" 或 "key：value"（支持中文冒号）。
// 只有 key 在 allowedKeys 中的行才会被保留。
// 不符合 key-value 格式的行（如纯文本段落）始终保留。
//
// 如果 allowedKeys 为空，返回原始上下文（向后兼容）。
func filterContext(rawContext string, allowedKeys []string) string {
	if rawContext == "" || len(allowedKeys) == 0 {
		return rawContext
	}

	// 构建白名单 set
	allowed := make(map[string]bool, len(allowedKeys))
	for _, k := range allowedKeys {
		allowed[strings.TrimSpace(strings.ToLower(k))] = true
	}

	lines := strings.Split(rawContext, "\n")
	var filtered []string

	for _, line := range lines {
		key := extractLineKey(line)
		if key == "" {
			// 非 key-value 格式行，保留
			filtered = append(filtered, line)
			continue
		}
		if allowed[strings.ToLower(key)] {
			filtered = append(filtered, line)
		}
		// 不在白名单中的 key-value 行被过滤掉
	}

	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

// extractLineKey 从 "key: value" 或 "key：value" 格式中提取 key
// 如果不是 key-value 格式，返回空字符串
func extractLineKey(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}

	// 尝试英文冒号
	if idx := strings.Index(trimmed, ":"); idx > 0 && idx < 40 {
		key := strings.TrimSpace(trimmed[:idx])
		// key 不应包含空格（简单启发式：如果 key 中有空格则可能不是 key-value 格式）
		if !strings.Contains(key, " ") && !strings.Contains(key, "\t") {
			return key
		}
	}

	// 尝试中文冒号
	if idx := strings.Index(trimmed, "："); idx > 0 && idx < 60 {
		key := strings.TrimSpace(trimmed[:idx])
		if !strings.Contains(key, " ") && !strings.Contains(key, "\t") {
			return key
		}
	}

	return ""
}
