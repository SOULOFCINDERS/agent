package agent

import (
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// knowledgeGapPatterns 模型在"拒绝承认自身不知道的信息"时常用的措辞
// 这些模式表明模型在用训练数据截止日期做否定判断，而非调用工具验证
var knowledgeGapPatterns = []string{
	"截至我的知识",
	"截至我所知",
	"我的训练数据",
	"我的知识截止",
	"知识截止日期",
	"训练截止",
	"尚未发布",
	"还没有发布",
	"不存在该",
	"没有这款",
	"没有这个产品",
	"并不存在",
	"目前没有",
	"目前不存在",
	"没有找到相关信息",
	"没有相关信息",
	"i don't have information",
	"no information available",
	"does not exist",
	"hasn't been released",
	"has not been announced",
	"not aware of",
	"as of my knowledge",
	"as of my training",
	"my training data",
	"my knowledge cutoff",
}

// detectKnowledgeGap 检测 LLM 回复是否在用"训练截止"作为拒绝依据
// 如果检测到，返回 true 和匹配的模式
func detectKnowledgeGap(content string) (bool, string) {
	lower := strings.ToLower(content)
	for _, pattern := range knowledgeGapPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true, pattern
		}
	}
	return false, ""
}

// hasWebSearchTool 检查 toolDefs 中是否包含 web_search
func hasWebSearchTool(toolDefs []llm.ToolDef) bool {
	for _, td := range toolDefs {
		if td.Function.Name == "web_search" {
			return true
		}
	}
	return false
}

// buildSearchNudge 构建搜索提醒消息，注入到 history 中替代 LLM 的否定回复
func buildSearchNudge(userMessage string) llm.Message {
	return llm.Message{
		Role:    "user",
		Content: "[系统提醒] 你刚才的回复基于过时的训练数据。你的训练数据有截止日期，不能用来判断某个产品或事件是否存在。请立即调用 web_search 工具搜索来获取关于「" + userMessage + "」的最新信息，然后根据搜索结果重新回答。直接搜索，不要解释。",
	}
}
