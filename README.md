# Agent — Go LLM Agent Framework

一个从零构建、可逐步迭代的工具调用型 LLM Agent 框架，采用 DDD 四层架构设计。支持流式输出、多 Agent 协作、RAG 知识检索、MCP 外部工具协议、会话持久化、安全防护等企业级能力。

## 技术栈

| 项目 | 说明 |
|------|------|
| 语言 | Go 1.25 |
| 模块 | `github.com/SOULOFCINDERS/agent` |
| 架构 | DDD 四层 (Domain → Usecase → Presenter → Infrastructure) |
| 入口 | `cmd/agent/main.go` |
| 依赖注入 | `internal/container/` (集中式 DI) |
| 外部协议 | MCP (Model Context Protocol) via `github.com/modelcontextprotocol/go-sdk` |

---

## 快速开始

### 前置条件

- **Go 1.25+**
- LLM API（OpenAI 兼容）或使用 `--mock` 模式

### 安装与构建

```bash
git clone https://github.com/SOULOFCINDERS/agent.git
cd agent
go build ./...
```

### 环境变量

| 变量 | 说明 | 必填 |
|------|------|------|
| `LLM_BASE_URL` | LLM API 基础地址（如 `https://api.openai.com/v1`） | 是（或用 `--base-url`） |
| `LLM_API_KEY` | LLM API 密钥 | 是（或用 `--api-key`，`--mock` 模式无需） |
| `LLM_MODEL` | 模型名称（如 `gpt-4o`） | 是（或用 `--model`） |
| `FEISHU_APP_ID` | 飞书应用 ID | 仅 `--feishu` 时 |
| `FEISHU_APP_SECRET` | 飞书应用密钥 | 仅 `--feishu` 时 |
| `SERPAPI_API_KEY` | SerpAPI 搜索密钥（可选，增强 web_search） | 否 |
| `DASHSCOPE_API_KEY` | DashScope API 密钥（chromem RAG 嵌入） | 仅 chromem 后端时 |
| `LLM_SKIP_TLS` | 设为 `1` 跳过 TLS 验证（开发环境） | 否 |

---

## 命令参考

本框架提供 4 个子命令：

### `agent run` — 单次任务执行

使用规则引擎（非 LLM）执行简单任务。

```bash
agent run "calc: (1+2)*3"
agent run --trace "echo: 你好"
agent run --json "calc: 100/7"    # JSON 格式输出
agent run --root /project "read_file: main.go"
```

| 参数 | 说明 |
|------|------|
| `--trace` | 输出执行追踪到 stderr |
| `--json` | JSON 格式输出（含 output + trace） |
| `--root DIR` | 文件工具的根目录（默认 `.`） |

### `agent chat` — 交互式对话

LLM 驱动的多轮对话，支持工具调用。

```bash
# 基础用法
agent chat                         # 标准对话
agent chat --mock                  # Mock 模式（无需 API Key）
agent chat --stream                # 流式输出（逐 token 显示 + 工具状态）

# LLM 配置
agent chat --base-url https://api.openai.com/v1 --api-key sk-xxx --model gpt-4o

# 功能开关
agent chat --search                # 启用联网搜索（web_search + web_fetch）
agent chat --feishu                # 启用飞书文档工具
agent chat --memory                # 启用长期记忆
agent chat --rag                   # 启用 RAG 知识检索
agent chat --multi-agent           # 启用多 Agent 协作模式

# 资源管理
agent chat --budget 50000          # Token 预算限制
agent chat --ctx-window 128000     # 覆盖上下文窗口大小

# 知识库
agent chat --rag-load ./docs       # 启动时加载知识库目录
agent chat --rag --rag-backend chromem  # 使用 chromem-go 向量后端

# 会话管理
agent chat --resume abc123         # 恢复之前的会话
agent chat --session-dir /path     # 自定义会话存储目录
```

**对话内操作**：

| 命令 | 说明 |
|------|------|
| `quit` / `exit` / `q` | 保存会话并退出 |
| `clear` / `reset` | 重置对话历史，创建新会话 |

### `agent web` — Web 图形界面

启动内置 Web UI（暗色主题，支持流式输出、工具卡片、上下文面板）。

```bash
agent web --mock --addr :8080
agent web --stream --search --addr :3000
agent web --rag-load ./docs --addr :8080
```

| 参数 | 说明 |
|------|------|
| `--addr ADDR` | 监听地址（默认 `:8080`） |
| 其余参数 | 同 `agent chat` |

**API 端点**：

| 端点 | 方法 | 说明 |
|------|------|------|
| `/` | GET | Web UI 首页 |
| `/api/chat` | POST | 非流式对话 |
| `/api/chat/stream` | POST | SSE 流式对话 |
| `/api/sessions/clear` | POST | 清空当前会话 |
| `/api/status` | GET | 系统状态信息 |
| `/api/context` | GET | 上下文窗口 + Token 用量 |

**SSE 事件类型**：

| 事件 | 数据格式 | 说明 |
|------|----------|------|
| `session` | string | 会话 ID |
| `delta` | string | 流式文本增量 |
| `tool_start` | JSON | 工具开始执行 (`tool_call_id`, `tool_name`, `tool_args`) |
| `tool_end` | JSON | 工具执行完成 (`tool_result`/`tool_error`, `duration`) |
| `iteration` | JSON | Agent 循环轮次 (`iteration`, `max_iter`) |
| `thinking` | string | LLM 推理过程 |
| `status` | string | 状态变更（"调用工具中..."、"等待模型响应..."） |
| `context` | JSON | 上下文用量统计（每轮结束推送） |
| `done` | string | 完整回复文本 |
| `error` | string | 错误信息 |

### `agent sessions` — 会话管理

```bash
agent sessions                     # 列出所有保存的会话
agent sessions --delete abc123     # 删除指定会话
agent sessions --session-dir /path # 指定存储目录
```

---

## 完整参数列表

| 参数 | 适用命令 | 说明 | 默认值 |
|------|----------|------|--------|
| `--trace` | chat, web | 输出执行追踪到 stderr | 关 |
| `--mock` | chat, web | 使用 Mock LLM（无需外部 API） | 关 |
| `--stream` | chat, web | 启用 Streaming V2 流式输出 | 关 |
| `--root DIR` | all | 文件工具根目录 | `.` |
| `--base-url URL` | chat, web | LLM API 基础地址 | env `LLM_BASE_URL` |
| `--api-key KEY` | chat, web | LLM API 密钥 | env `LLM_API_KEY` |
| `--model NAME` | chat, web | LLM 模型名称 | env `LLM_MODEL` |
| `--search` | chat, web | 启用联网搜索工具 | 关 |
| `--feishu` | chat, web | 启用飞书文档工具 | 关 |
| `--memory` | chat, web | 启用长期记忆功能 | 关 |
| `--rag` | chat, web | 启用 RAG 知识检索 | 关 |
| `--rag-dir DIR` | chat, web | RAG 索引存储目录 | `<root>/.agent-rag` |
| `--rag-load DIR` | chat, web | 启动时加载知识库目录（自动启用 RAG） | — |
| `--rag-backend STR` | chat, web | RAG 后端：`legacy` 或 `chromem` | `legacy` |
| `--multi-agent` | chat, web | 启用多 Agent 协作模式 | 关 |
| `--budget N` | chat, web | Token 预算限制（0=无限制） | `0` |
| `--ctx-window N` | chat, web | 覆盖上下文窗口大小（tokens） | 自动检测 |
| `--mem-dir DIR` | chat, web | 记忆存储路径 | `<root>/.agent-memory` |
| `--session-dir DIR` | chat, web, sessions | 会话持久化目录 | `<root>/.agent-sessions` |
| `--addr ADDR` | web | Web 服务监听地址 | `:8080` |
| `--resume ID` | chat | 恢复之前的会话 | — |

---

## 内置工具

### 基础工具

| 工具 | 说明 |
|------|------|
| `echo` | 回显文本（用于测试） |
| `calc` | 数学表达式计算 |
| `summarize` | 文本摘要 |
| `weather` | 天气查询 |

### 文件系统工具

| 工具 | 说明 |
|------|------|
| `read_file` | 读取文件内容 |
| `write_file` | 写入/创建文件 |
| `edit_file` | 精确编辑文件（搜索替换） |
| `list_dir` | 列出目录内容 |
| `grep_repo` | 在代码仓库中搜索文本 |
| `exec_command` | 执行 Shell 命令 |

### 联网工具（`--search`）

| 工具 | 说明 |
|------|------|
| `web_search` | 网页搜索（支持 SerpAPI） |
| `web_fetch` | 获取网页内容 |

### 飞书工具（`--feishu`）

| 工具 | 说明 |
|------|------|
| `feishu_create_doc` | 创建飞书文档 |
| `feishu_read_doc` | 读取飞书文档 |

### 记忆工具（`--memory`）

| 工具 | 说明 |
|------|------|
| `save_memory` | 保存记忆条目 |
| `search_memory` | 语义搜索记忆 |
| `delete_memory` | 删除记忆条目 |

### RAG 知识库工具（`--rag`）

| 工具 | 说明 |
|------|------|
| `rag_import` | 批量导入知识库目录（支持 40+ 文件格式） |
| `rag_index` | 索引单个文件或文本 |
| `rag_query` | 语义检索知识片段 |
| `rag_list` | 列出已索引文档 |
| `rag_delete` | 删除已索引文档 |

---

## 功能详解

### Streaming V2 — 结构化流式输出

`--stream` 启用增强流式模式，不仅传递文本增量，还实时推送工具执行状态：

**CLI 效果**：
```
你: 帮我读一下 main.go 的内容

Agent:
  🔧 read_file({"path":"main.go"})
  ✅ done (45ms)
好的，main.go 的内容如下...
  ⟳ 轮次 2/10
  ⏳ 调用工具中...
  🔧 calc({"expression":"1+2"})
  ✅ done (2ms)
```

**Web UI 效果**：工具调用以卡片形式展示，包含运行状态指示（蓝色脉冲=运行中、绿色=完成、红色=错误）、参数预览、结果展示和执行耗时。

### RAG — 知识检索增强

支持两种向量后端：

| 后端 | 启用方式 | 特点 |
|------|----------|------|
| `legacy`（默认） | `--rag` | 零依赖 TF-IDF 嵌入，JSON 持久化，适合开发/测试 |
| `chromem` | `--rag --rag-backend chromem` | 内置 OpenAI/DashScope/Ollama 嵌入，gob+gzip 持久化，适合生产 |

**使用示例**：
```bash
# 启动时自动加载 docs 目录
agent chat --rag-load ./docs

# 对话中手动索引
用户: 请索引 ./src 目录下的所有 Go 文件
Agent: (调用 rag_import) ✅ 已索引 15 个文件, 127 个片段

# 基于知识回答
用户: 项目的并发模型是怎么设计的？
Agent: (调用 rag_query → 检索相关片段) 根据已索引的知识...
```

### Multi-Agent — 多 Agent 协作

`--multi-agent` 启用编排者模式，由 Orchestrator 将任务拆解分配给专用子 Agent：

```bash
agent chat --multi-agent --search --rag
```

### MCP — 外部工具协议

通过 Model Context Protocol 动态发现和调用外部工具服务：

```go
// container.Config 配置示例
cfg.MCPServers = []mcp.ServerConfig{
    {ID: "filesystem", Transport: "stdio", Command: "npx", Args: []string{"@mcp/fs-server"}},
    {ID: "github", Transport: "sse", URL: "http://localhost:3100/sse"},
}
```

支持 `stdio`（本地子进程）和 `sse`（HTTP 远程）两种传输模式。

### 上下文窗口管理

内置智能上下文窗口管理：

- **TruncationCache** — 缓存工具结果截断，最大化 LLM prefix cache 命中率
- **Cache 冷热感知** — 冷启动时更激进截断，热状态保持稳定
- **Nudge 效率提醒** — 上下文 ≥60% 时注入效率提醒，≥85% 升级为紧急提醒

### 安全防护（Guardrail）

启用方式：`container.Config{GuardrailMode: true}`

内置检查器：
- **Prompt 注入检测** — 阻断注入攻击
- **PII 检测** — 脱敏手机号/身份证/邮箱/银行卡/IP
- **敏感词过滤** — 关键词阻断或脱敏
- **长度限制** — 输入字符数上限

### 会话持久化

对话自动保存到磁盘，支持跨会话恢复：

```bash
agent chat                    # 自动创建并保存会话
agent chat --resume abc123    # 恢复会话继续对话
agent sessions                # 列出所有会话
agent sessions --delete abc   # 删除会话
```

存储路径：`<root>/.agent-sessions/`（每个会话一个 JSON 文件）

---

## 项目结构

```
agent/
├── cmd/agent/main.go              # CLI 入口 (run/chat/web/sessions)
├── internal/
│   ├── domain/                    # 领域层 — 核心概念，零外部依赖
│   │   ├── conversation/          #   消息模型 (Message, ToolCall, Client)
│   │   ├── tool/                  #   工具接口 (Tool, ErrorKind)
│   │   ├── agentloop/             #   循环模型 (Plan, Step, Planner, Executor)
│   │   ├── memory/                #   记忆模型 (Entry, Store)
│   │   ├── ctxwindow/             #   上下文窗口 (Priority, ModelProfile)
│   │   ├── structured/            #   结构化输出 (Schema, Extractor)
│   │   ├── guardrail/             #   安全检查 (Guard, Pipeline, Phase)
│   │   ├── mcp/                   #   MCP 协议 (Client, Manager, ServerConfig)
│   │   ├── session/               #   会话模型 (Session, Summary, Store)
│   │   └── rag/                   #   RAG 接口 (Embedder, Chunker, VectorStore)
│   ├── agent/                     # Agent 核心循环
│   │   ├── loop.go                #   非流式对话循环
│   │   ├── loop_stream.go         #   流式对话 (ChatStream)
│   │   ├── loop_stream_v2.go      #   增强流式 (ChatStreamV2 + 结构化事件)
│   │   ├── stream_event.go        #   StreamEvent 类型定义
│   │   └── parallel.go            #   并行工具执行
│   ├── llm/                       # LLM 客户端 (OpenAI 兼容 + 流式)
│   ├── tools/                     # 22 个内置工具实现
│   ├── web/                       # Web UI (Go 嵌入式前端)
│   │   ├── server.go              #   HTTP 路由 + SSE 流式
│   │   ├── html.go / js.go / css.go  # 前端资源
│   │   └── static.go              #   静态资源拼接
│   ├── memory/                    # 记忆存储实现
│   ├── rag/                       # RAG 引擎实现
│   ├── mcp/                       # MCP 协议实现
│   ├── multiagent/                # 多 Agent 编排
│   ├── ctxwindow/                 # 上下文窗口管理
│   ├── guardrail/                 # 安全检查实现
│   ├── session/                   # 会话持久化 (JSON 文件)
│   ├── container/                 # 依赖注入容器
│   └── ...
├── AGENTS.md                      # AI 编码代理项目说明
├── Makefile                       # 常用构建命令
└── go.mod
```

---

## 架构概览

```
CLI / Web UI (Presenter)
        ↓
    Container (DI 装配)
        ↓
    LoopAgent / Orchestrator
    ├── LLM Client (OpenAI 兼容)
    ├── Tool Registry (22 内置 + MCP 动态发现)
    ├── Context Window Manager
    ├── Guardrail Pipeline
    ├── RAG Engine
    ├── Memory Store
    └── Session Manager
```

**核心原则**：
- **Domain 层零外部依赖** — 所有领域概念在 `internal/domain/` 下，不导入外部包
- **集中式 DI** — `container.Build(cfg)` 统一组装所有组件
- **Type Alias 兼容** — Infrastructure 层通过 `type X = domain.X` 保持旧路径可用
- **充血模型** — Message 等对象自带行为方法

---

## 开发指南

### 构建与测试

```bash
go build ./...                      # 编译全部
go test ./...                       # 运行全部测试
go test -v ./internal/agent/        # 详细测试单个包
go test -cover ./...                # 带覆盖率
go run ./cmd/agent chat --mock      # Mock 模式快速验证
```

### 添加新工具

1. 在 `internal/tools/` 创建 `my_tool.go`，实现 `domain/tool.Tool` 接口
2. 在 `internal/tools/schema.go` 的 `BuiltinSchemas()` 中添加 JSON Schema
3. 在 `internal/container/container.go` 的 `buildRegistry()` 中注册
4. 编写 `my_tool_test.go`

### 添加新领域概念

1. 在 `internal/domain/` 下创建新子包，定义接口和值对象
2. 在 Infrastructure 层实现接口
3. 在 Container 中注册依赖装配

---

## 常见用法示例

```bash
# 最简启动（Mock 模式，零配置）
go run ./cmd/agent chat --mock --stream

# 生产配置：流式 + 搜索 + RAG + 记忆
export LLM_BASE_URL=https://api.openai.com/v1
export LLM_API_KEY=sk-xxx
export LLM_MODEL=gpt-4o
go run ./cmd/agent chat --stream --search --memory --rag-load ./docs

# Web 服务完整功能
go run ./cmd/agent web --stream --search --rag --memory --addr :8080

# 多 Agent + 飞书集成
export FEISHU_APP_ID=xxx
export FEISHU_APP_SECRET=xxx
go run ./cmd/agent chat --multi-agent --feishu --search --stream
```

## License

Private — Internal Use Only
