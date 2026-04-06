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
			Desc: "计算四则运算表达式，支持加减乘除和括号",
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
			Desc: "联网搜索，根据关键词搜索互联网内容。当用户询问最新信息、新闻、实时数据、或你不确定的事实时使用此工具",
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
	}
}
