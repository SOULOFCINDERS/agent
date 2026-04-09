package benchmark

import (
	"encoding/json"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// DefaultTestSuite 返回完整的默认测试集
func DefaultTestSuite() []TestCase {
	var cases []TestCase
	cases = append(cases, hallucinationCases()...)
	cases = append(cases, toolUseCases()...)
	cases = append(cases, reasoningCases()...)
	cases = append(cases, taskCompletionCases()...)
	cases = append(cases, performanceCases()...)
	return cases
}

// ============================================================
// D1: 幻觉防线测试
// ============================================================

func hallucinationCases() []TestCase {
	webSearchTool := llm.ToolDef{
		Type: "function",
		Function: llm.FuncDef{
			Name:        "web_search",
			Description: "搜索互联网",
			Parameters: mustJSON(map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}),
		},
	}
	calcTool := llm.ToolDef{
		Type: "function",
		Function: llm.FuncDef{
			Name:        "calc",
			Description: "计算器",
			Parameters: mustJSON(map[string]any{"type": "object", "properties": map[string]any{"expr": map[string]any{"type": "string"}}}),
		},
	}

	return []TestCase{
		// ---- L0: Proactive Search ----
		{
			ID:          "H-PS-01",
			Name:        "主动搜索：未知产品名",
			Description: "用户问 MacBook NEO，应在 LLM 回复前主动注入搜索指令",
			Dimension:   DimHallucination,
			SubCategory: "proactive_search",
			UserMessage: "MacBook NEO 怎么样，值得买吗",
			ToolDefs:    []llm.ToolDef{webSearchTool},
			MockResponses: []MockResponse{
				// 第一次调用：LLM 收到主动搜索指令后，应调用 web_search
				{ToolCalls: []llm.ToolCall{{
					ID: "tc1", Type: "function",
					Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"MacBook NEO"}`},
				}}},
				// 第二次调用：基于搜索结果回复
				{Content: "MacBook NEO 是苹果最新发布的笔记本电脑，搭载 M5 芯片，售价 12999 元起。"},
			},
			Assertions: []Assertion{
				{Type: AssertGuardTriggered, Target: "proactive_search", Weight: 1.0, Critical: true},
				{Type: AssertNotContains, Target: "不存在", Weight: 0.5},
				{Type: AssertNotContains, Target: "没有这款产品", Weight: 0.5},
			},
		},
		{
			ID:          "H-PS-02",
			Name:        "主动搜索：普通问题不应触发",
			Description: "天气等通用问题不应触发主动搜索",
			Dimension:   DimHallucination,
			SubCategory: "proactive_search",
			UserMessage: "今天天气怎么样",
			ToolDefs:    []llm.ToolDef{webSearchTool},
			MockResponses: []MockResponse{
				{Content: "我无法查看实时天气，请使用天气 App 查看。"},
			},
			Assertions: []Assertion{
				{Type: AssertGuardNotTriggered, Target: "proactive_search", Weight: 1.0, Critical: true},
			},
		},

		// ---- L2: Knowledge Gap ----
		{
			ID:          "H-KG-01",
			Name:        "知识缺口：训练截止否定",
			Description: "LLM 用'截至我的训练数据'否定事实，应被拦截并重新搜索",
			Dimension:   DimHallucination,
			SubCategory: "knowledge_gap",
			UserMessage: "2026 年诺贝尔物理学奖颁给了谁",
			ToolDefs:    []llm.ToolDef{webSearchTool},
			MockResponses: []MockResponse{
				{Content: "截至我的训练数据截止时间，2026 年诺贝尔物理学奖尚未公布。"},
				// 第二次（被拦截后重新生成，这次调用工具）
				{ToolCalls: []llm.ToolCall{{
					ID: "tc1", Type: "function",
					Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"2026 诺贝尔物理学奖"}`},
				}}},
				{Content: "2026 年诺贝尔物理学奖颁给了张三，因其在量子计算领域的突破性贡献。"},
			},
			Assertions: []Assertion{
				{Type: AssertGuardTriggered, Target: "knowledge_gap", Weight: 1.0, Critical: true},
				{Type: AssertNotContains, Target: "截至我的训练", Weight: 0.5},
			},
		},

		// ---- L3: Reasoning Guard ----
		{
			ID:          "H-RG-01",
			Name:        "推理矛盾：洗车店距离判断",
			Description: "推理说'开车去'，结论却说'走路去'，应检测矛盾并修正",
			Dimension:   DimHallucination,
			SubCategory: "reasoning_guard",
			UserMessage: "洗车店离我家50米，我是开车去还是走路去",
			MockResponses: []MockResponse{
				// 第一次：推理和结论矛盾
				{Content: "## 分析\n50米距离非常近，当然开车去不划算。\n\n## 结论\n建议你走路去，50米的距离走路去更方便快捷。"},
				// 第二次：修正后一致
				{Content: "50米非常近，走路只需要不到1分钟，建议走路去。"},
			},
			Assertions: []Assertion{
				// 注意：这个case中推理和结论其实是一致的（都说走路），所以不应触发
				// 我们需要一个真正矛盾的case
				{Type: AssertContains, Target: "走路", Weight: 1.0, Critical: true},
			},
		},
		{
			ID:          "H-RG-02",
			Name:        "推理矛盾：过程说开车结论说走路",
			Description: "推理过程明确说'应该开车去'，结论却建议'走路去'",
			Dimension:   DimHallucination,
			SubCategory: "reasoning_guard",
			UserMessage: "洗车店离我家5公里，我是开车去还是走路去",
			MockResponses: []MockResponse{
				// 第一次：真正的矛盾——推理说开车，结论说走路
				{Content: "## 思考\n5公里的距离还是比较远的，当然开车去比较合适。开车大概10分钟就到了。\n\n## 结论\n建议你走路去，可以锻炼身体。"},
				// 第二次：修正后一致
				{Content: "5公里距离较远，建议开车去，大约10分钟车程。"},
			},
			Assertions: []Assertion{
				{Type: AssertGuardTriggered, Target: "reasoning_guard", Weight: 1.0, Critical: true},
				{Type: AssertContains, Target: "开车", Weight: 0.5},
			},
		},

		// ---- L4: Fabrication Guard (数值) ----
		{
			ID:          "H-FN-01",
			Name:        "数值编造：心算复杂乘法",
			Description: "用户要求计算但 LLM 未用 calc 工具直接给数字",
			Dimension:   DimHallucination,
			SubCategory: "fabrication_numeric",
			UserMessage: "帮我算一下 3456 乘以 789 等于多少",
			ToolDefs:    []llm.ToolDef{calcTool},
			MockResponses: []MockResponse{
				// 第一次：直接心算（被拦截）
				{Content: "3456 × 789 = 2,726,784"},
				// 第二次：使用 calc 工具
				{ToolCalls: []llm.ToolCall{{
					ID: "tc1", Type: "function",
					Function: llm.FunctionCall{Name: "calc", Arguments: `{"expr":"3456*789"}`},
				}}},
				{Content: "3456 × 789 = 2,726,784（已通过计算器验证）"},
			},
			Assertions: []Assertion{
				{Type: AssertGuardTriggered, Target: "numeric_guard", Weight: 1.0, Critical: true},
			},
		},
		{
			ID:          "H-FN-02",
			Name:        "数值编造：价格询问不应触发",
			Description: "'多少钱'是价格查询不是计算，不应触发数值守卫",
			Dimension:   DimHallucination,
			SubCategory: "fabrication_numeric",
			UserMessage: "MacBook Pro 多少钱",
			ToolDefs:    []llm.ToolDef{calcTool},
			MockResponses: []MockResponse{
				{Content: "MacBook Pro 14 英寸版售价 14999 元起，16 英寸版售价 19999 元起。"},
			},
			Assertions: []Assertion{
				{Type: AssertGuardNotTriggered, Target: "numeric_guard", Weight: 1.0, Critical: true},
			},
		},

		// ---- L4: Fabrication Guard (URL) ----
		{
			ID:          "H-FU-01",
			Name:        "URL 编造：虚构链接检测",
			Description: "LLM 编造了工具结果中不存在的 URL",
			Dimension:   DimHallucination,
			SubCategory: "fabrication_url",
			UserMessage: "帮我搜索 Go 语言教程",
			ToolDefs:    []llm.ToolDef{webSearchTool},
			History: []llm.Message{
				{Role: "assistant", ToolCalls: []llm.ToolCall{{
					ID: "tc1", Type: "function",
					Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"Go 语言教程"}`},
				}}},
				{Role: "tool", ToolCallID: "tc1", Content: "Go 语言官方教程：https://go.dev/tour/"},
			},
			MockResponses: []MockResponse{
				{Content: "推荐以下 Go 语言教程：\n1. [官方教程](https://go.dev/tour/)\n2. [高级教程](https://fake-golang-tutorials.com/advanced)"},
			},
			Assertions: []Assertion{
				{Type: AssertNotContains, Target: "fake-golang-tutorials.com", Weight: 1.0, Critical: true},
				{Type: AssertNotContains, Target: "fake-golang-tutorials.com", Weight: 0.5},
			},
		},

		// ---- L4: Fabrication Guard (引用) ----
		{
			ID:          "H-FC-01",
			Name:        "引用编造：未验证名人名言",
			Description: "LLM 编造了未经搜索验证的名人语录",
			Dimension:   DimHallucination,
			SubCategory: "fabrication_citation",
			UserMessage: "爱因斯坦对教育有什么看法",
			MockResponses: []MockResponse{
				{Content: `爱因斯坦曾说过："想象力比知识更重要。"他还在《相对论浅说》中指出，教育不是学习事实，而是训练思维。`},
			},
			Assertions: []Assertion{
				{Type: AssertNotContains, Target: "fake-golang-tutorials.com", Weight: 1.0, Critical: true},
				{Type: AssertContains, Target: "⚠️", Weight: 0.5},
			},
		},
	}
}

// ============================================================
// D2: 工具使用测试
// ============================================================

func toolUseCases() []TestCase {
	webSearchTool := llm.ToolDef{
		Type: "function",
		Function: llm.FuncDef{
			Name:        "web_search",
			Description: "搜索互联网获取最新信息",
			Parameters: mustJSON(map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}),
		},
	}
	calcTool := llm.ToolDef{
		Type: "function",
		Function: llm.FuncDef{
			Name:        "calc",
			Description: "数学计算器",
			Parameters: mustJSON(map[string]any{"type": "object", "properties": map[string]any{"expr": map[string]any{"type": "string"}}}),
		},
	}

	return []TestCase{
		{
			ID:          "T-TC-01",
			Name:        "工具正确性：搜索工具选择",
			Description: "实时信息问题应调用 web_search",
			Dimension:   DimToolUse,
			SubCategory: "tool_correctness",
			UserMessage: "今天美股大盘行情如何",
			ToolDefs:    []llm.ToolDef{webSearchTool, calcTool},
			MockResponses: []MockResponse{
				{ToolCalls: []llm.ToolCall{{
					ID: "tc1", Type: "function",
					Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"今天美股大盘行情"}`},
				}}},
				{Content: "今日美股三大指数集体收涨，道指涨 0.5%，纳指涨 1.2%。"},
			},
			Assertions: []Assertion{
				{Type: AssertToolCalled, Target: "web_search", Weight: 1.0, Critical: true},
				{Type: AssertToolNotCalled, Target: "calc", Weight: 0.5},
			},
		},
		{
			ID:          "T-TC-02",
			Name:        "工具正确性：计算工具选择",
			Description: "数学计算应调用 calc 而非 web_search",
			Dimension:   DimToolUse,
			SubCategory: "tool_correctness",
			UserMessage: "计算 15% 的年利率，10万元存3年复利是多少",
			ToolDefs:    []llm.ToolDef{webSearchTool, calcTool},
			MockResponses: []MockResponse{
				{ToolCalls: []llm.ToolCall{{
					ID: "tc1", Type: "function",
					Function: llm.FunctionCall{Name: "calc", Arguments: `{"expr":"100000 * (1+0.15)^3"}`},
				}}},
				{Content: "10万元按15%年利率存3年复利，最终金额为 152,087.50 元。"},
			},
			Assertions: []Assertion{
				{Type: AssertToolCalled, Target: "calc", Weight: 1.0, Critical: true},
				{Type: AssertToolNotCalled, Target: "web_search", Weight: 0.3},
			},
		},
		{
			ID:          "T-TE-01",
			Name:        "工具效率：不应重复调用",
			Description: "简单问题不应多次调用同一工具",
			Dimension:   DimToolUse,
			SubCategory: "tool_efficiency",
			UserMessage: "北京现在几点",
			ToolDefs:    []llm.ToolDef{webSearchTool},
			MockResponses: []MockResponse{
				{Content: "北京现在是下午3点15分（北京时间 UTC+8）。"},
			},
			Assertions: []Assertion{
				{Type: AssertLatencyBelow, Value: 5000, Weight: 0.5},
			},
		},
	}
}

// ============================================================
// D3: 推理质量测试
// ============================================================

func reasoningCases() []TestCase {
	return []TestCase{
		{
			ID:          "R-CC-01",
			Name:        "推理连贯性：多步逻辑",
			Description: "需要多步推理的问题，结论应与推理一致",
			Dimension:   DimReasoning,
			SubCategory: "coherence",
			UserMessage: "一个游泳池长50米宽25米深2米，能装多少升水？请用中文回答",
			MockResponses: []MockResponse{
				{Content: "## 推理过程\n1. 体积 = 长 × 宽 × 深 = 50 × 25 × 2 = 2500 立方米\n2. 1 立方米 = 1000 升\n3. 总容量 = 2500 × 1000 = 2,500,000 升\n\n## 结论\n这个游泳池能装 250 万升水。"},
			},
			Assertions: []Assertion{
				{Type: AssertContains, Target: "2500", Weight: 0.5},
				{Type: AssertContains, Target: "250", Weight: 0.5},
				{Type: AssertMatchesRegex, Target: `(?:2[,.]?500[,.]?000|250\s*万)`, Weight: 1.0, Critical: true},
			},
		},
		{
			ID:          "R-CC-02",
			Name:        "推理连贯性：常识推理",
			Description: "日常常识问题推理和结论应一致",
			Dimension:   DimReasoning,
			SubCategory: "coherence",
			UserMessage: "冬天在东北室外放一杯水，1小时后水会怎样",
			MockResponses: []MockResponse{
				{Content: "东北冬天气温通常在零下20度以下，水在0度就会结冰。1小时足够让一杯水完全结冰。所以1小时后，这杯水会结成冰。"},
			},
			Assertions: []Assertion{
				{Type: AssertContains, Target: "冰", Weight: 1.0, Critical: true},
				{Type: AssertNotContains, Target: "不会结冰", Weight: 0.5},
			},
		},
		{
			ID:          "R-CS-01",
			Name:        "因果推理：不应颠倒因果",
			Description: "回答应正确反映因果关系",
			Dimension:   DimReasoning,
			SubCategory: "causal",
			UserMessage: "为什么下雨天路面会湿",
			MockResponses: []MockResponse{
				{Content: "下雨时，雨水从天空落到地面，覆盖在路面上，所以路面会变湿。这是因为雨水（因）导致了路面湿滑（果）。"},
			},
			Assertions: []Assertion{
				{Type: AssertContains, Target: "雨水", Weight: 0.5},
				{Type: AssertNotContains, Target: "路面湿了所以下雨", Weight: 1.0, Critical: true},
			},
		},
	}
}

// ============================================================
// D4: 任务完成测试
// ============================================================

func taskCompletionCases() []TestCase {
	webSearchTool := llm.ToolDef{
		Type: "function",
		Function: llm.FuncDef{
			Name:        "web_search",
			Description: "搜索互联网",
			Parameters: mustJSON(map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}),
		},
	}

	return []TestCase{
		{
			ID:          "TC-01",
			Name:        "任务完成：直接问答",
			Description: "简单问题应直接回答，不遗漏关键信息",
			Dimension:   DimTaskCompletion,
			SubCategory: "direct_qa",
			UserMessage: "Go 语言的 goroutine 和线程有什么区别？请列出至少3个",
			MockResponses: []MockResponse{
				{Content: "goroutine 和线程的主要区别：\n1. **内存占用**：goroutine 初始栈仅 2KB，线程通常 1-8MB\n2. **调度方式**：goroutine 由 Go runtime 调度（M:N 模型），线程由 OS 内核调度\n3. **创建成本**：goroutine 创建极快（几微秒），线程创建较慢（几十微秒到毫秒级）\n4. **通信方式**：goroutine 推荐用 channel 通信，线程用共享内存+锁"},
			},
			Assertions: []Assertion{
				{Type: AssertContains, Target: "内存", Weight: 0.3},
				{Type: AssertContains, Target: "调度", Weight: 0.3},
				{Type: AssertMatchesRegex, Target: `(?:1\.|2\.|3\.)`, Weight: 0.4, Critical: true}, // 至少3个点
			},
		},
		{
			ID:          "TC-02",
			Name:        "任务完成：搜索+综合",
			Description: "需要搜索后综合回答的任务",
			Dimension:   DimTaskCompletion,
			SubCategory: "search_synthesis",
			UserMessage: "2026年有哪些值得关注的AI新进展",
			ToolDefs:    []llm.ToolDef{webSearchTool},
			MockResponses: []MockResponse{
				{ToolCalls: []llm.ToolCall{{
					ID: "tc1", Type: "function",
					Function: llm.FunctionCall{Name: "web_search", Arguments: `{"query":"2026 AI 新进展"}`},
				}}},
				{Content: "2026年AI领域的重要进展包括：\n1. GPT-5 系列模型发布\n2. 多模态Agent框架成为主流\n3. AI编程助手效率大幅提升"},
			},
			Assertions: []Assertion{
				{Type: AssertToolCalled, Target: "web_search", Weight: 0.5},
				{Type: AssertContains, Target: "2026", Weight: 0.5, Critical: true},
			},
		},
		{
			ID:          "TC-03",
			Name:        "任务完成：拒答边界",
			Description: "确实无法回答时应坦诚说明，不编造",
			Dimension:   DimTaskCompletion,
			SubCategory: "refusal",
			UserMessage: "我家后院有只猫叫什么名字",
			MockResponses: []MockResponse{
				{Content: "抱歉，我无法知道您家猫的名字。这是您的个人信息，我没有途径获取。您可以告诉我它叫什么名字吗？"},
			},
			Assertions: []Assertion{
				{Type: AssertMatchesRegex, Target: `(?:无法|不知道|抱歉|没有途径)`, Weight: 1.0, Critical: true},
				{Type: AssertNotContains, Target: "它叫小花", Weight: 0.5}, // 不应编造
			},
		},
	}
}

// ============================================================
// D5: 性能指标测试
// ============================================================

func performanceCases() []TestCase {
	return []TestCase{
		{
			ID:          "P-01",
			Name:        "性能：简单问题响应速度",
			Description: "简单问题应在合理时间内返回",
			Dimension:   DimPerformance,
			SubCategory: "latency",
			UserMessage: "1+1等于几",
			MockResponses: []MockResponse{
				{Content: "1 + 1 = 2"},
			},
			Assertions: []Assertion{
				{Type: AssertLatencyBelow, Value: 5000, Weight: 1.0}, // 5秒内（mock下应极快）
			},
		},
		{
			ID:          "P-02",
			Name:        "性能：Token 效率",
			Description: "回答不应过度冗长浪费 Token",
			Dimension:   DimPerformance,
			SubCategory: "token_efficiency",
			UserMessage: "Go 的 defer 是什么",
			MockResponses: []MockResponse{
				{Content: "Go 的 `defer` 语句用于延迟函数调用到外层函数返回之后执行。常用于资源清理（关闭文件、解锁等）。执行顺序为 LIFO（后进先出）。"},
			},
			Assertions: []Assertion{
				{Type: AssertTokensBelow, Value: 5000, Weight: 1.0},
				{Type: AssertContains, Target: "defer", Weight: 0.5},
			},
		},
	}
}

// mustJSON 将 map 序列化为 json.RawMessage
func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
