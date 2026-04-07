package ctxwindow

import "fmt"

// ---- Phase 1: Nudge 提醒注入 ----

// NudgeThreshold 触发 Nudge 提醒的上下文使用率阈值（默认 60%）
const NudgeThreshold = 0.60

// NudgeCriticalThreshold 触发紧急 Nudge 的上下文使用率阈值（默认 85%）
const NudgeCriticalThreshold = 0.85

// NudgeMessage 根据上下文使用率生成效率提醒消息
// 返回空字符串表示不需要注入 Nudge
//
// 设计理念（参考 Claude Code 的 contextEfficiencyNudge）：
//   - 在上下文使用率 >60% 时，提醒 LLM 注意输出效率
//   - 在上下文使用率 >85% 时，发出紧急提醒，要求极简输出
//   - 这不是硬性裁剪，而是软性引导 LLM 自主节省 context 空间
func NudgeMessage(status WindowStatus) string {
	if status.UsagePercent < NudgeThreshold {
		return ""
	}

	if status.UsagePercent >= NudgeCriticalThreshold {
		return fmt.Sprintf(
			"[CONTEXT CRITICAL] Context window is %.0f%% full (%d/%d tokens, %d remaining). "+
				"You MUST be extremely concise: "+
				"(1) Use minimal tool call arguments — only essential params. "+
				"(2) Give short, direct answers — no explanations unless asked. "+
				"(3) Avoid repeating information already in context. "+
				"(4) If a task requires extensive output, break it into smaller steps.",
			status.UsagePercent*100,
			status.EstimatedTokens,
			status.MaxInputTokens,
			status.RemainingTokens,
		)
	}

	return fmt.Sprintf(
		"[CONTEXT EFFICIENCY] Context window is %.0f%% full (%d/%d tokens, %d remaining). "+
			"Please be concise in your responses and tool calls to preserve context space. "+
			"Prefer short answers and avoid unnecessary repetition.",
		status.UsagePercent*100,
		status.EstimatedTokens,
		status.MaxInputTokens,
		status.RemainingTokens,
	)
}
