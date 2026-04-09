package agent

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ============================================================
// Fabrication Guard — 统一的"编造内容"检测层
// 合并原 numeric_guard / fabricated_url / citation_guard
//
// LLM 的"编造"本质上是同一类问题，只是编的东西不同：
//   - 编数字（心算出错）
//   - 编链接（虚构 URL）
//   - 编引用（伪造名人名言/书籍出处）
//
// 统一为 FabricationCheckResult，一次扫描，分别处理。
// ============================================================

// ---- 公共数据结构 ----

// FabricationCheckResult 统一的编造内容检查结果
type FabricationCheckResult struct {
	// 数值编造
	NumericRisk   bool     // 回复中有可疑的数值计算（没用 calc 就给数字）
	RiskNumbers   []string // 可疑的数字
	ShouldUseCalc bool     // 是否应该使用 calc 工具重算

	// URL 编造
	FabricatedURLs []string // 工具结果中未出现过的 URL

	// 引用编造
	HasUnverifiedQuotes bool     // 有未经验证的引用
	SuspiciousQuotes    []string // 可疑引用片段
	HasUnverifiedBooks  bool     // 有未经验证的书籍引用
	SuspiciousBooks     []string // 可疑书籍名
}

// HasFabrication 是否检测到任何编造内容
func (r FabricationCheckResult) HasFabrication() bool {
	return r.NumericRisk || len(r.FabricatedURLs) > 0 || r.HasUnverifiedQuotes || r.HasUnverifiedBooks
}

// NeedsRegeneration 是否需要打回重新生成（目前仅数值编造需要）
func (r FabricationCheckResult) NeedsRegeneration() bool {
	return r.NumericRisk && r.ShouldUseCalc
}

// NeedsContentFix 是否需要修正输出内容（URL/引用编造只需修正，不需重新生成）
func (r FabricationCheckResult) NeedsContentFix() bool {
	return len(r.FabricatedURLs) > 0 || r.HasUnverifiedQuotes || r.HasUnverifiedBooks
}

// ---- 统一检测入口 ----

// DetectFabrication 统一检测回复中的编造内容
// userMessage: 用户原始消息（用于判断是否为计算类问题）
// reply:       LLM 的回复
// history:     对话历史（用于检查工具调用和工具结果）
// toolDefs:    可用工具列表
func DetectFabrication(userMessage string, reply string, history []llm.Message, toolDefs []llm.ToolDef) FabricationCheckResult {
	result := FabricationCheckResult{}

	// ① 数值编造检测
	if hasCalcToolDef(toolDefs) {
		numResult := checkNumericFabrication(userMessage, reply, history)
		result.NumericRisk = numResult.NumericRisk
		result.RiskNumbers = numResult.RiskNumbers
		result.ShouldUseCalc = numResult.ShouldUseCalc
	}

	// ② URL 编造检测
	result.FabricatedURLs = checkFabricatedURLs(reply, history)

	// ③ 引用编造检测
	citResult := checkCitationFabrication(reply, history)
	result.HasUnverifiedQuotes = citResult.HasUnverifiedQuotes
	result.SuspiciousQuotes = citResult.SuspiciousQuotes
	result.HasUnverifiedBooks = citResult.HasUnverifiedBooks
	result.SuspiciousBooks = citResult.SuspiciousBooks

	return result
}

// FixFabricatedContent 修正回复中的编造内容（URL + 引用）
// 不包含数值编造——数值需要打回重新生成，不是简单修正
func FixFabricatedContent(content string, result FabricationCheckResult) string {
	// 修正虚构 URL
	if len(result.FabricatedURLs) > 0 {
		content = cleanFabricatedURLs(content, result.FabricatedURLs)
	}
	// 修正未验证引用
	if result.HasUnverifiedQuotes || result.HasUnverifiedBooks {
		content = addCitationDisclaimer(content, result)
	}
	return content
}

// ============================================================
// ① 数值编造检测
// ============================================================

// numericPattern 匹配回复中的数字（含小数、百分比、带逗号的大数字）
var numericPattern = regexp.MustCompile(`(?:[\d,]+\.?\d+)\s*(?:%|GB|MB|TB|元|美元|万|亿|kg|km|米)?`)

// checkNumericFabrication 检测回复中的数值编造（后置守卫）
// 简化策略：不再用关键词做意图路由（已移入 system prompt），
// 仅做后置检查——如果回复包含计算结果数字但未使用 calc 工具，标记为可疑。
func checkNumericFabrication(userMessage string, reply string, history []llm.Message) FabricationCheckResult {
	result := FabricationCheckResult{}

	// 1. 检查 history 中是否已使用了 calc 工具
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

	// 如果已使用 calc，无需进一步检查
	if usedCalc {
		return result
	}

	// 2. 检查回复中是否有具体的计算结果数字
	numbers := numericPattern.FindAllString(reply, -1)
	var suspicious []string
	for _, n := range numbers {
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
		result.NumericRisk = true
		result.RiskNumbers = suspicious
		result.ShouldUseCalc = true
	}

	return result
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

// buildCalcNudge 构建强制使用计算器的提醒消息
func buildCalcNudge(userMessage string) llm.Message {
	return llm.Message{
		Role:    "user",
		Content: "[系统提醒] 你刚才在回复中直接给出了计算结果，但没有使用 calc 工具。你的心算可能不准确。请使用 calc 工具重新计算，然后基于工具返回的精确结果回答。直接调用工具，不要解释。原始问题：" + userMessage,
	}
}

// ============================================================
// ② URL 编造检测
// ============================================================

// urlPattern 匹配回复中的 HTTP/HTTPS URL
var urlPattern = regexp.MustCompile(`https?://[^\s\)\]\}>，。、；！？"'` + "`" + `]+`)

// collectToolURLs 从 history 中所有 tool role 消息中提取出现过的 URL
func collectToolURLs(history []llm.Message) map[string]bool {
	urls := make(map[string]bool)
	for _, msg := range history {
		if msg.Role != "tool" {
			continue
		}
		found := urlPattern.FindAllString(msg.Content, -1)
		for _, u := range found {
			u = strings.TrimRight(u, ".,;:!?。，；：！？)")
			urls[u] = true
			urls[strings.TrimRight(u, "/")] = true
		}
	}
	return urls
}

func checkFabricatedURLs(content string, history []llm.Message) []string {
	replyURLs := urlPattern.FindAllString(content, -1)
	if len(replyURLs) == 0 {
		return nil
	}

	toolURLs := collectToolURLs(history)
	if len(toolURLs) == 0 {
		return nil
	}

	var fabricated []string
	for _, u := range replyURLs {
		u = strings.TrimRight(u, ".,;:!?。，；：！？)")
		normalized := strings.TrimRight(u, "/")

		if toolURLs[u] || toolURLs[normalized] {
			continue
		}

		matched := false
		for toolURL := range toolURLs {
			if strings.HasPrefix(toolURL, normalized) || strings.HasPrefix(normalized, toolURL) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		if isCommonURL(normalized) {
			continue
		}

		fabricated = append(fabricated, u)
	}
	return fabricated
}

// isCommonURL 判断是否为常见的通用 URL（不算虚构）
func isCommonURL(u string) bool {
	commonPrefixes := []string{
		"https://www.google.com",
		"https://google.com",
		"https://github.com",
		"https://stackoverflow.com",
		"https://en.wikipedia.org",
		"https://zh.wikipedia.org",
		"https://docs.python.org",
		"https://go.dev",
		"https://golang.org",
		"https://developer.mozilla.org",
	}
	lower := strings.ToLower(u)
	for _, prefix := range commonPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// cleanFabricatedURLs 从回复内容中移除虚构的 URL
func cleanFabricatedURLs(content string, fabricated []string) string {
	for _, u := range fabricated {
		mdPattern := regexp.MustCompile(`\[([^\]]*)\]\(` + regexp.QuoteMeta(u) + `\)`)
		content = mdPattern.ReplaceAllString(content, "$1")
		content = strings.ReplaceAll(content, u, "[链接已移除：无法验证来源]")
	}
	return content
}

// ============================================================
// ③ 引用编造检测
// ============================================================

// quotePatterns 匹配常见的引用格式
var quotePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:曾说过|曾表示|曾指出|说过|提到过|写道|指出)[：:]\s*[「「"'']`),
	regexp.MustCompile(`(?:正如|如同).{2,10}(?:所说|所言|所述|所指)`),
	regexp.MustCompile(`根据.{2,20}(?:的说法|表示|指出|认为)`),
	regexp.MustCompile(`在(?:《.+?》|".+?"|'.+?')(?:中|一书中|里)(?:提到|指出|写道|说)`),
	regexp.MustCompile(`(?i)(?:once said|famously said|stated that|wrote that|noted that|argued that)[:\s]`),
	regexp.MustCompile(`(?i)according to .{2,30},`),
	regexp.MustCompile(`(?i)in (?:his|her|their) (?:book|paper|article|essay) .{2,30}, .{2,20} (?:wrote|stated|argued)`),
}

// bookTitlePattern 匹配书名号或英文书名引用
var bookTitlePattern = regexp.MustCompile(`《(.+?)》|"([A-Z][^"]{2,50})"`)

func checkCitationFabrication(reply string, history []llm.Message) FabricationCheckResult {
	result := FabricationCheckResult{}

	toolContent := collectAllToolContent(history)

	// 检测引用模式
	for _, p := range quotePatterns {
		matches := p.FindAllString(reply, -1)
		for _, m := range matches {
			if !isSubstringInToolContent(m, toolContent) {
				result.HasUnverifiedQuotes = true
				result.SuspiciousQuotes = append(result.SuspiciousQuotes, truncateStr(m, 80))
			}
		}
	}

	// 检测书籍引用
	bookMatches := bookTitlePattern.FindAllStringSubmatch(reply, -1)
	for _, bm := range bookMatches {
		bookName := bm[1]
		if bookName == "" {
			bookName = bm[2]
		}
		if bookName == "" {
			continue
		}
		if !isSubstringInToolContent(bookName, toolContent) {
			if hasSpecificBookQuote(reply, bookName) {
				result.HasUnverifiedBooks = true
				result.SuspiciousBooks = append(result.SuspiciousBooks, bookName)
			}
		}
	}

	return result
}

// collectAllToolContent 从 history 中收集所有工具返回的文本内容
func collectAllToolContent(history []llm.Message) string {
	var sb strings.Builder
	for _, msg := range history {
		if msg.Role == "tool" {
			sb.WriteString(msg.Content)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// isSubstringInToolContent 检查文本片段是否在工具结果中出现过
func isSubstringInToolContent(fragment string, toolContent string) bool {
	if toolContent == "" {
		return false
	}
	cleanFragment := strings.ReplaceAll(fragment, " ", "")
	cleanContent := strings.ReplaceAll(toolContent, " ", "")
	return strings.Contains(strings.ToLower(cleanContent), strings.ToLower(cleanFragment))
}

// hasSpecificBookQuote 检查回复中是否对某本书引用了具体内容
func hasSpecificBookQuote(reply string, bookName string) bool {
	idx := strings.Index(reply, bookName)
	if idx < 0 {
		return false
	}
	// 使用字节切片而非 rune 切片，因为 strings.Index 返回字节偏移
	end := idx + len(bookName) + 600 // 用字节长度，中文约3字节/字，600字节≈200中文字符
	if end > len(reply) {
		end = len(reply)
	}
	nearby := reply[idx:end]
	return strings.ContainsAny(nearby, "\"\"''「」『』") ||
		strings.Contains(nearby, "指出") ||
		strings.Contains(nearby, "写道") ||
		strings.Contains(nearby, "提到")
}

// addCitationDisclaimer 为未验证的引用添加不确定性标记
func addCitationDisclaimer(content string, result FabricationCheckResult) string {
	if !result.HasUnverifiedQuotes && !result.HasUnverifiedBooks {
		return content
	}

	var warnings []string
	if result.HasUnverifiedQuotes {
		warnings = append(warnings, "部分引言")
	}
	if result.HasUnverifiedBooks {
		warnings = append(warnings, "部分书籍引用内容")
	}

	disclaimer := "\n\n> ⚠️ 注意：以上回答中" + strings.Join(warnings, "和") + "未经在线搜索验证，可能不完全准确。如需确认，建议搜索原始出处。"
	return content + disclaimer
}

// truncateStr 截断字符串
func truncateStr(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
