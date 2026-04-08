package agent

import (
	"regexp"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// conclusionMarkers 用于定位 LLM 最终结论部分的标记词
var conclusionMarkers = []string{
	"结论", "所以", "因此", "总结", "综上", "总的来说",
	"✅ 结论", "✅ 所以", "🔍 但", "答案是", "建议：", "建议是",
	"in conclusion", "therefore", "so the answer", "in summary",
}

// ReasoningCheckResult 推理一致性检查结果
type ReasoningCheckResult struct {
	HasContradiction bool   // 推理和结论是否矛盾
	ReasoningClaim   string // 推理中的关键声明
	ConclusionClaim  string // 结论中的矛盾声明
}

// detectReasoningContradiction 检测回复中推理过程与最终结论之间的矛盾
func detectReasoningContradiction(reply string) ReasoningCheckResult {
	result := ReasoningCheckResult{}

	// 1. 找到结论分界点（取最靠后的标记）
	conclusionStart := -1
	for _, marker := range conclusionMarkers {
		idx := strings.LastIndex(strings.ToLower(reply), strings.ToLower(marker))
		if idx > conclusionStart {
			conclusionStart = idx
		}
	}

	// 找不到结论标记，或标记太靠前（不是真正的总结），跳过
	if conclusionStart < 0 || conclusionStart < len(reply)/4 {
		return result
	}

	reasoning := reply[:conclusionStart]
	conclusion := reply[conclusionStart:]

	// 2. 提取两部分的关键动作
	reasoningActions := extractActionAdvice(reasoning)
	conclusionActions := extractActionAdvice(conclusion)

	// 3. 检查矛盾
	for _, ra := range reasoningActions {
		for _, ca := range conclusionActions {
			if areContradictory(ra, ca) {
				result.HasContradiction = true
				result.ReasoningClaim = ra
				result.ConclusionClaim = ca
				return result
			}
		}
	}
	return result
}

// actionPairPattern 抓取"应该/建议/当然 + 动作"的模式
var actionPairPattern = regexp.MustCompile(`(?:应该|建议|需要|必须|当然|肯定|那你|你就|那就|适合)\s*([^\n，。,\.]{2,20})`)

// directActionPattern 直接的动作短语（用于结论部分，通常没有前缀词）
var directActionPattern = regexp.MustCompile(`(开车去|走路去|步行去|打车去|骑车去|坐车去|开车过去|走路过去|步行过去|drive|walk|take a cab)`)

// extractActionAdvice 从文本中提取建议性动作
func extractActionAdvice(text string) []string {
	var actions []string

	// 1. 匹配"建议/当然 + 动作"模式
	matches := actionPairPattern.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) >= 2 {
			actions = append(actions, strings.TrimSpace(m[1]))
		}
	}

	// 2. 匹配直接动作短语（结论部分常见：直接说"走路去"而无前缀）
	directMatches := directActionPattern.FindAllString(text, -1)
	for _, dm := range directMatches {
		actions = append(actions, dm)
	}

	return actions
}

// opposites 对立动作对
var opposites = [][2]string{
	{"开车", "走路"}, {"走路", "开车"},
	{"步行", "开车"}, {"开车", "步行"},
	{"驾车", "走路"}, {"走路", "驾车"},
	{"打车", "走路"}, {"走路", "打车"},
	{"买", "不买"}, {"不买", "买"},
	{"同意", "反对"}, {"反对", "同意"},
	{"是", "不是"}, {"可以", "不可以"},
	{"能", "不能"},
	{"drive", "walk"}, {"walk", "drive"},
	{"yes", "no"}, {"no", "yes"},
}

// areContradictory 判断两个动作建议是否矛盾
func areContradictory(a, b string) bool {
	aLower := strings.ToLower(a)
	bLower := strings.ToLower(b)
	for _, pair := range opposites {
		if strings.Contains(aLower, pair[0]) && strings.Contains(bLower, pair[1]) {
			return true
		}
	}
	return false
}

// buildReasoningFixNudge 构建修正提醒
func buildReasoningFixNudge(userMessage string, contradiction ReasoningCheckResult) llm.Message {
	return llm.Message{
		Role: "user",
		Content: "[系统提醒] 你刚才的回复中，推理过程和最终结论存在矛盾。" +
			"推理中你提到了「" + contradiction.ReasoningClaim + "」，" +
			"但结论却说「" + contradiction.ConclusionClaim + "」。" +
			"请重新审视你的推理链，给出一个与推理过程一致的简洁结论。" +
			"直接给出最终答案，不需要重复推理过程。" +
			"原始问题：" + userMessage,
	}
}
