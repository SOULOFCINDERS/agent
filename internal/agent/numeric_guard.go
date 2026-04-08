package agent

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// numericPattern 匹配回复中的数字（含小数、百分比、带逗号的大数字）
// 示例匹配：3.14, 99.9%, 1,234,567, 128GB
var numericPattern = regexp.MustCompile(`(?:[\d,]+\.?\d+)\s*(?:%|GB|MB|TB|元|美元|万|亿|kg|km|米)?`)

// mathExpressionIndicators 暗示需要计算的关键词
var mathExpressionIndicators = []string{
	"计算", "算一下", "帮我算", "算算",
	"总共多少", "合计", "平均", "总计",
	"加上", "减去", "乘以", "除以", "百分之",
	"calculate", "total", "sum", "average",
	"年利率", "月供", "利息", "复利", "收益率",
	"面积是多少", "体积是多少",
}

// NumericCheckResult 数值检查结果
type NumericCheckResult struct {
	HasRisk       bool     // 回复中有可疑的数值计算
	RiskNumbers   []string // 可疑的数字
	ShouldUseCalc bool     // 是否应该使用 calc 工具
}

// detectNumericRisk 检测回复中是否包含需要计算器验证的数值
// 核心逻辑：如果用户问了计算类问题，但 LLM 没调用 calc 工具就给了数字，则有风险
func detectNumericRisk(userMessage string, reply string, history []llm.Message) NumericCheckResult {
	result := NumericCheckResult{}

	// 1. 用户消息是否暗示需要计算
	needsCalc := false
	msgLower := strings.ToLower(userMessage)
	for _, ind := range mathExpressionIndicators {
		if strings.Contains(msgLower, strings.ToLower(ind)) {
			needsCalc = true
			break
		}
	}
	if !needsCalc {
		return result
	}

	// 2. 检查 history 中是否使用了 calc 工具
	usedCalc := false
	for _, msg := range history {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.Function.Name == "calc" {
					usedCalc = true
					break
				}
			}
		}
		if usedCalc {
			break
		}
	}

	// 3. 如果需要计算但没用 calc，检查回复中是否有具体数字
	if !usedCalc {
		numbers := numericPattern.FindAllString(reply, -1)
		// 过滤掉太简单的数字（个位数、年份等）
		var suspicious []string
		for _, n := range numbers {
			// 提取纯数字部分
			numStr := strings.ReplaceAll(n, ",", "")
			numStr = strings.TrimRight(numStr, "%GB MBTBkgkmm元美万亿米")
			numStr = strings.TrimSpace(numStr)
			if numStr == "" {
				continue
			}
			val, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				continue
			}
			// 过滤年份（2000-2099）和个位数
			if val >= 2000 && val <= 2099 {
				continue
			}
			if val < 10 {
				continue
			}
			suspicious = append(suspicious, n)
		}
		if len(suspicious) > 0 {
			result.HasRisk = true
			result.RiskNumbers = suspicious
			result.ShouldUseCalc = hasCalcTool(history)
		}
	}

	return result
}

// hasCalcTool 检查是否有 calc 工具可用
func hasCalcTool(history []llm.Message) bool {
	// 无法直接从 history 判断 toolDefs，这里用于标记是否建议使用
	// 实际判断在 loop.go 中通过 toolDefs 完成
	return true
}

// buildCalcNudge 构建强制使用计算器的提醒消息
func buildCalcNudge(userMessage string) llm.Message {
	return llm.Message{
		Role:    "user",
		Content: "[系统提醒] 你刚才在回复中直接给出了计算结果，但没有使用 calc 工具。你的心算可能不准确。请使用 calc 工具重新计算，然后基于工具返回的精确结果回答。直接调用工具，不要解释。原始问题：" + userMessage,
	}
}

// hasCalcToolDef 检查 toolDefs 中是否有 calc
func hasCalcToolDef(toolDefs []llm.ToolDef) bool {
	for _, td := range toolDefs {
		if td.Function.Name == "calc" {
			return true
		}
	}
	return false
}
