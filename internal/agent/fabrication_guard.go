package agent

import (
	"regexp"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// urlPattern 匹配回复中的 HTTP/HTTPS URL
var urlPattern = regexp.MustCompile(`https?://[^\s\)\]\}>，。、；！？"'` + "`" + `]+`)

// collectToolURLs 从 history 中所有 tool role 消息中提取出现过的 URL
// 这些是工具实际返回的真实 URL
func collectToolURLs(history []llm.Message) map[string]bool {
	urls := make(map[string]bool)
	for _, msg := range history {
		if msg.Role != "tool" {
			continue
		}
		found := urlPattern.FindAllString(msg.Content, -1)
		for _, u := range found {
			// 标准化：去掉尾部标点
			u = strings.TrimRight(u, ".,;:!?。，；：！？)")
			urls[u] = true
			// 也加入去掉尾部斜杠的版本
			urls[strings.TrimRight(u, "/")] = true
		}
	}
	return urls
}

// detectFabricatedURLs 检测回复中是否包含工具结果中未出现过的 URL（可能是虚构的）
// 返回虚构的 URL 列表
func detectFabricatedURLs(content string, history []llm.Message) []string {
	// 提取回复中的所有 URL
	replyURLs := urlPattern.FindAllString(content, -1)
	if len(replyURLs) == 0 {
		return nil
	}

	// 收集所有工具返回中出现过的真实 URL
	toolURLs := collectToolURLs(history)
	if len(toolURLs) == 0 {
		// 如果没有任何工具调用，回复中的 URL 可能是模型常识中的（如 github.com）
		// 此时不做拦截，避免误报
		return nil
	}

	var fabricated []string
	for _, u := range replyURLs {
		u = strings.TrimRight(u, ".,;:!?。，；：！？)")
		normalized := strings.TrimRight(u, "/")

		// 检查是否在工具结果中出现过
		if toolURLs[u] || toolURLs[normalized] {
			continue
		}

		// 检查是否是工具 URL 的前缀/子串匹配（工具返回长 URL，回复引用短版本）
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

		// 白名单：一些常见的通用 URL 不算虚构
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

// cleanFabricatedURLs 从回复内容中移除虚构的 URL，替换为警告标记
func cleanFabricatedURLs(content string, fabricated []string) string {
	for _, u := range fabricated {
		// 替换 markdown 链接格式 [text](fabricated_url)
		// 只保留文本部分
		mdPattern := regexp.MustCompile(`\[([^\]]*)\]\(` + regexp.QuoteMeta(u) + `\)`)
		content = mdPattern.ReplaceAllString(content, "$1")

		// 替换裸 URL
		content = strings.ReplaceAll(content, u, "[链接已移除：无法验证来源]")
	}
	return content
}
