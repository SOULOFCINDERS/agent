package agent

import (
	"regexp"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// quotePatterns 匹配常见的引用格式
// "XX曾说过：'...'"、"据XX表示"、"正如XX所说"
var quotePatterns = []*regexp.Regexp{
	// 中文引用模式
	regexp.MustCompile(`(?:曾说过|曾表示|曾指出|说过|提到过|写道|指出)[：:]\s*[「「"'']`),
	regexp.MustCompile(`(?:正如|如同).{2,10}(?:所说|所言|所述|所指)`),
	regexp.MustCompile(`根据.{2,20}(?:的说法|表示|指出|认为)`),
	regexp.MustCompile(`在(?:《.+?》|".+?"|'.+?')(?:中|一书中|里)(?:提到|指出|写道|说)`),

	// 英文引用模式
	regexp.MustCompile(`(?i)(?:once said|famously said|stated that|wrote that|noted that|argued that)[:\s]`),
	regexp.MustCompile(`(?i)according to .{2,30},`),
	regexp.MustCompile(`(?i)in (?:his|her|their) (?:book|paper|article|essay) .{2,30}, .{2,20} (?:wrote|stated|argued)`),
}

// bookTitlePattern 匹配书名号或英文书名引用
var bookTitlePattern = regexp.MustCompile(`《(.+?)》|"([A-Z][^"]{2,50})"`)

// CitationCheckResult 引用检查结果
type CitationCheckResult struct {
	HasUnverifiedQuotes bool     // 是否有未经验证的引用
	SuspiciousQuotes    []string // 可疑引用片段
	HasUnverifiedBooks  bool     // 是否有未经验证的书籍引用
	SuspiciousBooks     []string // 可疑书籍名
}

// detectUnverifiedCitations 检测回复中是否包含未经工具验证的引用
// 核心逻辑：如果回复引用了"某人说过"或"某书中写道"，但没有通过搜索验证，则标记为可疑
func detectUnverifiedCitations(reply string, history []llm.Message) CitationCheckResult {
	result := CitationCheckResult{}

	// 1. 检查是否有工具调用结果（如果搜索过，引用可能已验证）
	toolContent := collectAllToolContent(history)

	// 2. 检测引用模式
	for _, p := range quotePatterns {
		matches := p.FindAllString(reply, -1)
		for _, m := range matches {
			// 检查这段引用是否出现在工具结果中
			if !isSubstringInToolContent(m, toolContent) {
				result.HasUnverifiedQuotes = true
				result.SuspiciousQuotes = append(result.SuspiciousQuotes, truncateStr(m, 80))
			}
		}
	}

	// 3. 检测书籍引用 — 书名号内的内容
	bookMatches := bookTitlePattern.FindAllStringSubmatch(reply, -1)
	for _, bm := range bookMatches {
		bookName := bm[1] // 中文书名号
		if bookName == "" {
			bookName = bm[2] // 英文书名
		}
		if bookName == "" {
			continue
		}
		// 检查书名是否在工具结果中出现过
		if !isSubstringInToolContent(bookName, toolContent) {
			// 进一步检查：如果回复中还引用了书中的"具体内容"，则更可疑
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
	// 简单子串匹配（去除空格后）
	cleanFragment := strings.ReplaceAll(fragment, " ", "")
	cleanContent := strings.ReplaceAll(toolContent, " ", "")
	return strings.Contains(strings.ToLower(cleanContent), strings.ToLower(cleanFragment))
}

// hasSpecificBookQuote 检查回复中是否对某本书引用了具体内容（不只是提到书名）
func hasSpecificBookQuote(reply string, bookName string) bool {
	// 查找书名附近是否有引号内容（表示引用了书中的话）
	idx := strings.Index(reply, bookName)
	if idx < 0 {
		return false
	}
	// 取书名后 200 字符范围内查找引号
	end := idx + len(bookName) + 200
	runes := []rune(reply)
	if end > len(runes) {
		end = len(runes)
	}
	nearby := string(runes[idx:end])
	// 检查是否有引用标记
	return strings.ContainsAny(nearby, "\"\"''「」『』") ||
		strings.Contains(nearby, "指出") ||
		strings.Contains(nearby, "写道") ||
		strings.Contains(nearby, "提到")
}

// cleanUnverifiedCitations 为未验证的引用添加不确定性标记
func cleanUnverifiedCitations(content string, citations CitationCheckResult) string {
	if !citations.HasUnverifiedQuotes && !citations.HasUnverifiedBooks {
		return content
	}

	// 在包含未验证引用的回复末尾添加提示
	var warnings []string
	if citations.HasUnverifiedQuotes {
		warnings = append(warnings, "部分引言")
	}
	if citations.HasUnverifiedBooks {
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
