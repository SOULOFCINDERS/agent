package agent

import (
	"regexp"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// productNamePattern 匹配用户消息中可能的产品名/型号
// 覆盖常见模式：MacBook XXX, iPhone 17, Galaxy S26, PS6, RTX 5090 等
var productNamePattern = regexp.MustCompile(`(?i)(?:` +
	// Apple 产品线
	`macbook\s*\w+|iphone\s*\d+\w*|ipad\s*\w+|apple\s+watch\s+\w+|airpods\s*\w*|` +
	// 通用电子产品型号模式
	`galaxy\s+\w+|pixel\s+\d+\w*|surface\s+\w+|thinkpad\s+\w+|` +
	// 带型号的通用模式（品牌+字母数字组合）
	`\w+\s+(?:pro|max|ultra|neo|air|mini|plus|lite|se)\b|` +
	// GPU/处理器
	`rtx\s*\d+\w*|rx\s*\d+\w*|ryzen\s+\d+\w*|` +
	// 游戏主机
	`ps\d|xbox\s+\w+|switch\s+\d*\w*` +
	`)`)

// entityIndicators 暗示用户在问某个具体事物的关键词
var entityIndicators = []string{
	"值得买", "怎么样", "好不好", "推荐吗", "发布了吗", "上市了吗",
	"多少钱", "什么时候出", "有没有", "参数", "配置", "评测",
	"worth buying", "how about", "any good", "released",
}

// ProactiveSearchResult 主动搜索检测结果
type ProactiveSearchResult struct {
	ShouldSearch bool   // 是否应该主动搜索
	Entity       string // 检测到的实体名
	Reason       string // 触发原因
}

// detectProactiveSearch 在 LLM 回复之前，分析用户消息判断是否需要主动搜索
// 核心目标：防止 LLM "先否认再纠正" 的问题
func detectProactiveSearch(userMessage string, toolDefs []llm.ToolDef) ProactiveSearchResult {
	result := ProactiveSearchResult{}

	// 必须有 web_search 工具
	if !hasWebSearchTool(toolDefs) {
		return result
	}

	// 1. 检测产品名/型号
	if match := productNamePattern.FindString(userMessage); match != "" {
		result.ShouldSearch = true
		result.Entity = match
		result.Reason = "product_name_detected"
		return result
	}

	// 2. 检测实体问询模式（"XX 怎么样"、"XX 值得买吗"）
	msgLower := strings.ToLower(userMessage)
	for _, ind := range entityIndicators {
		if strings.Contains(msgLower, strings.ToLower(ind)) {
			// 进一步确认消息中有具体名词（不只是泛问如"天气怎么样"）
			// 需要包含：英文字母、数字、或品牌关键词
			if len([]rune(userMessage)) > 5 && hasEntitySignal(userMessage) {
				result.ShouldSearch = true
				result.Entity = extractEntityFromMessage(userMessage)
				result.Reason = "entity_query_pattern"
				return result
			}
		}
	}

	return result
}

// extractEntityFromMessage 从用户消息中提取主要实体（简化版：取问句中的主语部分）
func extractEntityFromMessage(msg string) string {
	// 移除常见的问句后缀
	suffixes := []string{
		"值得买吗", "怎么样", "好不好", "推荐吗", "好吗", "如何",
		"多少钱", "什么时候出", "发布了吗", "上市了吗", "有没有",
		"参数是什么", "配置怎么样",
	}
	entity := msg
	for _, s := range suffixes {
		entity = strings.TrimSuffix(entity, s)
	}
	// 移除常见前缀
	prefixes := []string{
		"请问", "帮我查", "帮我看看", "你觉得", "你知道",
	}
	for _, p := range prefixes {
		entity = strings.TrimPrefix(entity, p)
	}
	entity = strings.TrimSpace(entity)

	// 如果太长或太短，返回原消息的前30个字符
	runes := []rune(entity)
	if len(runes) < 2 || len(runes) > 40 {
		if len([]rune(msg)) > 30 {
			return string([]rune(msg)[:30])
		}
		return msg
	}
	return entity
}

// buildProactiveSearchMessage 构建主动搜索指令
// 注入到 history 中，在 LLM 回复之前强制它先搜索
func buildProactiveSearchMessage(entity string) llm.Message {
	return llm.Message{
		Role: "user",
		Content: "[系统指令 - 优先级最高] 在回答之前，你必须先调用 web_search 搜索「" + entity +
			"」的最新信息。不要基于内部知识做任何判断或否认。搜索完成后，再基于搜索结果回答用户的问题。" +
			"注意：绝对不要说'该产品不存在'或'没有找到相关信息'之类的话，除非搜索结果明确证实了这一点。",
	}
}


// hasEntitySignal 检测消息中是否有实体名称信号（英文字母、数字、品牌关键词等）
// 用于过滤掉"今天天气怎么样"这类不需要主动搜索的问题
func hasEntitySignal(msg string) bool {
	// 包含英文字母序列（至少2个连续字母 → 可能是品牌/型号）
	for i := 0; i < len(msg)-1; i++ {
		if (msg[i] >= 'a' && msg[i] <= 'z') || (msg[i] >= 'A' && msg[i] <= 'Z') {
			if (msg[i+1] >= 'a' && msg[i+1] <= 'z') || (msg[i+1] >= 'A' && msg[i+1] <= 'Z') {
				return true
			}
		}
	}
	// 包含数字（可能是型号如 "Mate 70"、"RTX 5090"）
	for _, r := range msg {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	// 包含已知品牌中文名
	brandKeywords := []string{"华为", "小米", "苹果", "三星", "联想", "戴尔", "惠普", "索尼", "任天堂", "微软", "英伟达", "特斯拉", "比亚迪", "理想", "蔚来", "小鹏"}
	for _, brand := range brandKeywords {
		if strings.Contains(msg, brand) {
			return true
		}
	}
	return false
}
