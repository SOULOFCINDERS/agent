# 工具开发指南

## 已有工具列表

| 工具名 | 文件 | 功能 | 参数 |
|--------|------|------|------|
| `echo` | `tools/echo.go` | 回显文本 | `text: string` |
| `calc` | `tools/calc.go` | 四则运算计算器 | `expr: string` |
| `read_file` | `tools/read_file.go` | 读取文件内容 | `path, lines?, summarize?` |
| `list_dir` | `tools/list_dir.go` | 列出目录 | `path?` |
| `grep_repo` | `tools/grep_repo.go` | 正则搜索代码 | `pattern, path?, max_matches?` |
| `summarize` | `tools/summarize.go` | 文本统计摘要 | `input: string` |
| `weather` | `tools/weather.go` | 查询天气 | `location: string` |
| `web_search` | `tools/web_search.go` | 联网搜索 | `query, max_results?` |
| `web_fetch` | `tools/web_fetch.go` | 抓取网页内容 | `url, max_chars?` |
| `exec_command` | `tools/exec_command.go` | 安全执行 shell 命令 | `command, timeout?` |
| `save_memory` | `tools/memory_tool.go` | 保存记忆 | `topic, content, keywords?` |
| `search_memory` | `tools/memory_tool.go` | 搜索记忆 | `query, limit?` |
| `delete_memory` | `tools/memory_tool.go` | 删除记忆 | `id: string` |
| `feishu_read_doc` | `tools/feishu_doc.go` | 读取飞书文档 | `url: string` |
| `feishu_create_doc` | `tools/feishu_doc.go` | 创建飞书文档 | `title, content` |

## 添加新工具步骤

### 1. 创建工具文件

在 `internal/tools/` 下创建 `my_tool.go`：

```go
package tools

import (
    "context"
    "encoding/json"
)

type MyTool struct {
    // 依赖注入字段
}

func NewMyTool() *MyTool {
    return &MyTool{}
}

func (t *MyTool) Name() string { return "my_tool" }

func (t *MyTool) Execute(ctx context.Context, argsJSON string) (string, error) {
    var args struct {
        Param string `json:"param"`
    }
    if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
        return "", err
    }
    // 实现逻辑
    return "result", nil
}
```

### 2. 添加 JSON Schema

在 `internal/tools/schema.go` 的 `BuiltinSchemas()` 中添加：

```go
"my_tool": {
    Desc: "工具描述",
    Schema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "param": {"type": "string", "description": "参数说明"}
        },
        "required": ["param"]
    }`),
},
```

### 3. 注册到 Container

在 `internal/container/container.go` 的 `buildRegistry()` 中添加：

```go
reg.Register(tools.NewMyTool())
```

### 4. 写测试

创建 `internal/tools/my_tool_test.go`。

## 工具错误处理

工具执行错误通过 `domain/tool.ErrorKind` 分类：

| ErrorKind | 含义 | LLM 行为 |
|-----------|------|----------|
| `ErrRetryable` | 参数格式错误等 | LLM 会修正参数重试 |
| `ErrNotRetryable` | 权限/资源不存在 | LLM 不会重试 |
| `ErrTimeout` | 执行超时 | LLM 可能稍后重试 |
| `ErrPanic` | 内部错误 | LLM 改用其他方式 |
| `ErrUnknownTool` | 工具不存在 | LLM 选择其他工具 |
