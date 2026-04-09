package tools

import "encoding/json"

// ToolWithSchema 已通过别名从 domain/tool 引入（见 registry.go）

// BuiltinSchemas 返回所有内置工具的 schema 定义
// 不需要每个工具都实现接口，这里集中定义
func BuiltinSchemas() map[string]struct {
	Desc   string
	Schema json.RawMessage
} {
	return map[string]struct {
		Desc   string
		Schema json.RawMessage
	}{
		"echo": {
			Desc: "回显文本内容",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "要回显的文本"}
				},
				"required": ["text"]
			}`),
		},
		"calc": {
			Desc: "计算四则运算表达式，支持加减乘除和括号。\n\n【强制使用规则】当用户消息包含以下任一关键词时，你必须调用此工具：计算、算一下、帮我算、算算、总共多少、合计、平均、总计、加上、减去、乘以、除以、百分之、年利率、月供、利息、复利、收益率、面积、体积、calculate、total、sum、average。\n\n任何涉及数字运算的场景（利息、面积、价格合计、百分比、汇率换算等）都必须使用此工具。你的心算能力不可靠，即使是简单的加减乘除也必须用工具计算。绝不能心算或估算。",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"expr": {"type": "string", "description": "数学表达式，如 (1+2)*3"}
				},
				"required": ["expr"]
			}`),
		},
		"read_file": {
			Desc: "读取指定文件的内容，可限制行数",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path":      {"type": "string", "description": "文件路径（相对于 root）"},
					"lines":     {"type": "integer", "description": "最多读取的行数，默认20"},
					"summarize": {"type": "boolean", "description": "是否同时返回摘要信息"}
				},
				"required": ["path"]
			}`),
		},
		"list_dir": {
			Desc: "列出指定目录下的文件和子目录",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "目录路径（相对于 root），默认为当前目录"}
				}
			}`),
		},
		"grep_repo": {
			Desc: "在代码仓库中用正则表达式搜索匹配的代码行",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"pattern":     {"type": "string", "description": "正则表达式"},
					"path":        {"type": "string", "description": "搜索起始目录，默认为仓库根目录"},
					"max_matches": {"type": "integer", "description": "最大匹配数，默认20"}
				},
				"required": ["pattern"]
			}`),
		},
		"summarize": {
			Desc: "对输入文本进行统计摘要（行数、字节数、关键词等）",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"input": {"type": "string", "description": "要摘要的文本"}
				},
				"required": ["input"]
			}`),
		},
		"weather": {
			Desc: "查询指定城市的当前天气",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"location": {"type": "string", "description": "城市名称，如 Beijing、Shanghai"}
				},
				"required": ["location"]
			}`),
		},
		"web_search": {
			Desc: "联网搜索互联网上的最新信息。\n\n【强制使用规则】以下场景必须调用此工具：(1) 用户提到任何具体产品名称或型号（MacBook、iPhone、Galaxy、RTX、PS6 等）；(2) 用户消息包含品牌名（华为、小米、苹果、三星、特斯拉等）+ 评价/询价意图（怎么样、值得买、多少钱、发布了吗等）；(3) 用户询问的事实可能在你的训练截止日期之后发生；(4) 用户问题涉及时间（今年、最近、上个月等）。\n\n你的训练数据可能已过时。搜索结果永远比内部知识更可靠。绝不要基于内部知识否认某事物的存在。",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query":       {"type": "string", "description": "搜索关键词"},
					"max_results": {"type": "integer", "description": "最多返回结果数，默认5，最大10"}
				},
				"required": ["query"]
			}`),
		},
		"exec_command": {
			Desc: "Execute a shell command safely within the project directory. Only whitelisted commands are allowed (ls, cat, head, tail, wc, find, grep, date, echo, pwd, sort, uniq, cut, tr, file, du, df, env, which, uname). Dangerous patterns like pipes, redirects, and path traversal are blocked.",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "The shell command to execute, e.g. 'ls -la' or 'grep -r TODO .'"},
					"timeout": {"type": "number", "description": "Timeout in seconds (default: 10, max: 30)"}
				},
				"required": ["command"]
			}`),
		},
		"web_fetch": {
			Desc: "抓取指定URL的网页内容，提取正文纯文本。用于在web_search找到相关链接后获取详细内容",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url":       {"type": "string", "description": "要抓取的网页URL"},
					"max_chars": {"type": "integer", "description": "最多返回字符数，默认6000"}
				},
				"required": ["url"]
			}`),
		},
			"write_file": {
			Desc: "创建新文件或覆写已有文件。自动创建不存在的父目录。路径限制在项目根目录内，禁止写入敏感系统文件。",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path":    {"type": "string", "description": "文件路径（相对于项目根目录）"},
					"content": {"type": "string", "description": "要写入的完整文件内容"},
					"create_dirs": {"type": "boolean", "description": "是否自动创建父目录（默认 true）"}
				},
				"required": ["path", "content"]
			}`),
		},
		"edit_file": {
			Desc: "精确编辑已有文件。支持多种编辑模式：(1) 搜索替换 old_text→new_text；(2) 行号插入 line+insert；(3) 行号替换 line+new_text；(4) 行号删除 line+delete；(5) 末尾追加 append。每次只执行一个编辑操作，多处修改需多次调用。",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path":     {"type": "string", "description": "文件路径（相对于项目根目录）"},
					"old_text": {"type": "string", "description": "要被替换的原文（精确匹配，包括空格和换行）"},
					"new_text": {"type": "string", "description": "替换后的新内容"},
					"line":     {"type": "integer", "description": "行号（用于行号编辑模式，从 1 开始）"},
					"insert":   {"type": "string", "description": "在指定行前插入的内容"},
					"delete":   {"type": "integer", "description": "从指定行开始删除的行数"},
					"append":   {"type": "string", "description": "追加到文件末尾的内容"}
				},
				"required": ["path"]
			}`),
		},
}
}
