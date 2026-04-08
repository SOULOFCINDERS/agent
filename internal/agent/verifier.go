package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// VerificationResult 验证结果
type VerificationResult struct {
	Passed     bool              `json:"passed"`      // 是否通过验证
	Issues     []VerifyIssue     `json:"issues"`      // 发现的问题列表
	Suggestion string            `json:"suggestion"`  // 修改建议（仅当 Passed=false）
	Duration   time.Duration     `json:"-"`           // 验证耗时
}

// VerifyIssue 单个验证问题
type VerifyIssue struct {
	Type    string `json:"type"`    // 问题类型：factual_error, unsupported_claim, fabricated_url, hallucination, inconsistency
	Claim   string `json:"claim"`   // 有问题的原文片段
	Reason  string `json:"reason"`  // 为什么有问题
}

// Verifier 独立验证子 Agent
// 借鉴 Claude Code 的 Verification Agent 模式：
// 用一个独立的 LLM 调用对主 Agent 的回复进行对抗性审查
type Verifier struct {
	llmClient llm.Client      // 可复用主 Agent 的 client，也可用独立的
	enabled   bool
}

// NewVerifier 创建验证器
func NewVerifier(client llm.Client) *Verifier {
	return &Verifier{
		llmClient: client,
		enabled:   true,
	}
}

// SetEnabled 启用/禁用验证器
func (v *Verifier) SetEnabled(enabled bool) {
	v.enabled = enabled
}

// IsEnabled 返回是否启用
func (v *Verifier) IsEnabled() bool {
	return v.enabled
}

// NeedsVerification 判断当前回复是否需要验证
// 不是所有回复都需要：简单问候、纯工具操作结果等可以跳过
// 参考 Claude Code：只在"非平凡操作"（3+ 文件编辑、后端/API 变更等）时启用
func NeedsVerification(userMessage string, reply string, history []llm.Message) bool {
	// 高优先级：包含 URL 的一定要验证（不管长度）
	if strings.Contains(reply, "http://") || strings.Contains(reply, "https://") {
		return true
	}

	// 高优先级：调用了多个工具的一定要验证（确保回复与工具结果一致）
	toolCallCount := 0
	for _, msg := range history {
		if msg.Role == "tool" {
			toolCallCount++
		}
	}
	if toolCallCount >= 2 {
		return true
	}

	// 回复太短的不需要验证（简单问候、确认等）
	if len([]rune(reply)) < 80 {
		return false
	}

	// 包含事实性断言的需要验证（有数字、日期、产品名等）
	factIndicators := []string{
		"发布", "售价", "配置", "搭载", "支持", "采用", "推出",
		"根据", "据", "官方", "官网", "数据显示",
		"released", "features", "supports", "according to",
		"价格", "版本", "更新", "上市",
	}
	replyLower := strings.ToLower(reply)
	for _, ind := range factIndicators {
		if strings.Contains(replyLower, strings.ToLower(ind)) {
			return true
		}
	}

	return false
}

// buildVerificationPrompt 构建验证 Prompt
// 核心设计：验证器以"挑刺者"视角审查，只关注事实准确性
func buildVerificationPrompt(userMessage string, reply string, toolResults []toolEvidence) string {
	var sb strings.Builder

	sb.WriteString(`你是一个严格的事实验证助手。你的唯一任务是检查另一个 AI 助手的回复是否存在幻觉或错误。

## 你的检查清单

1. **事实一致性**：回复中的每个事实性断言是否都能在工具结果（搜索结果、网页内容）中找到依据？
2. **无中生有**：回复是否包含工具结果中完全没提到的信息（如编造的参数、价格、日期）？
3. **URL 真实性**：回复中的所有链接是否都来自工具返回的结果？
4. **逻辑一致性**：回复的各部分之间是否自相矛盾？
5. **过度确定**：对于工具结果中不确定或模糊的信息，回复是否错误地表述为确定事实？

## 输出格式（严格 JSON）

` + "```json" + `
{
  "passed": true/false,
  "issues": [
    {
      "type": "factual_error|unsupported_claim|fabricated_url|hallucination|inconsistency",
      "claim": "回复中有问题的原文片段",
      "reason": "为什么这是有问题的"
    }
  ],
  "suggestion": "如果 passed=false，给出修改建议；如果 passed=true，留空字符串"
}
` + "```" + `

如果一切正确，返回 {"passed": true, "issues": [], "suggestion": ""}

## 待验证的内容

**用户问题：**
`)
	sb.WriteString(userMessage)
	sb.WriteString("\n\n**AI 助手的回复：**\n")
	sb.WriteString(reply)

	if len(toolResults) > 0 {
		sb.WriteString("\n\n**工具调用结果（真实数据来源）：**\n")
		for i, ev := range toolResults {
			sb.WriteString(fmt.Sprintf("\n--- 工具 #%d: %s ---\n", i+1, ev.toolName))
			// 截断过长的工具结果，避免超出上下文
			content := ev.content
			runes := []rune(content)
			if len(runes) > 2000 {
				content = string(runes[:2000]) + "\n...[已截断]"
			}
			sb.WriteString(content)
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("\n\n**工具调用结果：** 无（AI 助手没有调用任何工具）\n")
		sb.WriteString("\n⚠️ 注意：没有工具结果意味着所有事实性断言都无法验证，应标记为 unsupported_claim。\n")
	}

	return sb.String()
}

// toolEvidence 从 history 中提取的工具调用证据
type toolEvidence struct {
	toolName string
	content  string
}

// extractToolEvidence 从对话历史中提取所有工具调用及其结果
func extractToolEvidence(history []llm.Message) []toolEvidence {
	var evidence []toolEvidence

	// 构建 toolCallID → toolName 映射
	callIDToName := make(map[string]string)
	for _, msg := range history {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				callIDToName[tc.ID] = tc.Function.Name
			}
		}
	}

	for _, msg := range history {
		if msg.Role == "tool" {
			name := callIDToName[msg.ToolCallID]
			if name == "" {
				name = "unknown"
			}
			evidence = append(evidence, toolEvidence{
				toolName: name,
				content:  msg.Content,
			})
		}
	}
	return evidence
}

// Verify 对主 Agent 的回复进行独立验证
// 返回验证结果；如果验证失败且有修改建议，主 Agent 可据此修正
func (v *Verifier) Verify(ctx context.Context, userMessage string, reply string, history []llm.Message) (*VerificationResult, error) {
	if !v.enabled {
		return &VerificationResult{Passed: true}, nil
	}

	start := time.Now()

	// 提取工具调用证据
	evidence := extractToolEvidence(history)

	// 构建验证 Prompt
	verifyPrompt := buildVerificationPrompt(userMessage, reply, evidence)

	// 独立的 LLM 调用（不共享主 Agent 的 history）
	messages := []llm.Message{
		{
			Role:    "system",
			Content: "你是一个事实验证专家。只输出 JSON，不要输出其他内容。",
		},
		{
			Role:    "user",
			Content: verifyPrompt,
		},
	}

	resp, err := v.llmClient.Chat(ctx, messages, nil) // 不给工具，纯推理
	if err != nil {
		return nil, fmt.Errorf("verification LLM call failed: %w", err)
	}

	result := &VerificationResult{
		Passed:   true, // 默认通过（解析失败时不阻断）
		Duration: time.Since(start),
	}

	// 解析 JSON 结果
	content := resp.Message.Content
	// 尝试提取 JSON 块（LLM 可能会包裹在 ```json...``` 中）
	if idx := strings.Index(content, "{"); idx >= 0 {
		if endIdx := strings.LastIndex(content, "}"); endIdx > idx {
			content = content[idx : endIdx+1]
		}
	}

	if err := json.Unmarshal([]byte(content), result); err != nil {
		// JSON 解析失败，不阻断主流程，标记为通过但记录警告
		result.Passed = true
		result.Suggestion = fmt.Sprintf("[verifier parse error: %v]", err)
	}

	result.Duration = time.Since(start)
	return result, nil
}

// ApplyCorrection 根据验证结果修正回复
// 策略：将验证失败的问题 + 原始回复 + 工具结果发给 LLM，让它自行修正
func (v *Verifier) ApplyCorrection(ctx context.Context, userMessage string, originalReply string, vResult *VerificationResult, history []llm.Message) (string, error) {
	if vResult.Passed || len(vResult.Issues) == 0 {
		return originalReply, nil
	}

	// 构建修正 Prompt
	var sb strings.Builder
	sb.WriteString("你之前的回复被事实验证器发现了以下问题，请修正后重新回复：\n\n")
	sb.WriteString("## 发现的问题\n")
	for i, issue := range vResult.Issues {
		sb.WriteString(fmt.Sprintf("%d. [%s] \"%s\" — %s\n", i+1, issue.Type, issue.Claim, issue.Reason))
	}
	if vResult.Suggestion != "" {
		sb.WriteString("\n## 修改建议\n")
		sb.WriteString(vResult.Suggestion)
		sb.WriteString("\n")
	}
	sb.WriteString("\n## 你的原始回复\n")
	sb.WriteString(originalReply)
	sb.WriteString("\n\n请根据上述问题修正回复。只修改有问题的部分，保持其他内容不变。对于无法验证的事实，请添加\"据搜索结果\"等限定语或移除不确定的信息。")

	// 追加修正请求到 history
	correctionMsg := llm.Message{
		Role:    "user",
		Content: sb.String(),
	}

	corrHistory := make([]llm.Message, len(history))
	copy(corrHistory, history)
	corrHistory = append(corrHistory, correctionMsg)

	resp, err := v.llmClient.Chat(ctx, corrHistory, nil)
	if err != nil {
		// 修正失败，返回原始回复（降级策略）
		return originalReply, nil
	}

	corrected := resp.Message.Content
	if len(strings.TrimSpace(corrected)) < 10 {
		return originalReply, nil
	}

	return corrected, nil
}
