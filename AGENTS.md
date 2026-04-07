# AGENTS.md — AI Agent Framework (Go)

> 本文件面向 AI 编码代理，提供项目上下文、约束和操作说明。

## 项目信息

- **项目类型**: Go CLI + Web 应用（LLM Agent Framework）
- **Go 版本**: 1.25
- **模块路径**: `github.com/SOULOFCINDERS/agent`
- **架构模式**: DDD 四层架构（Domain → Usecase → Presenter → Infrastructure）
- **入口**: `cmd/agent/main.go`

## 构建与测试命令

```bash
go build ./...          # 编译全部
go test ./...           # 运行全部测试
go test ./internal/llm/ # 测试单个包
go run ./cmd/agent chat --mock  # 启动 Mock 模式对话
go run ./cmd/agent run "calc: (1+2)*3"  # 单次任务执行
go run ./cmd/agent web --mock --addr :8080  # 启动 Web UI
```

## 目录结构

| 路径 | 层级 | 职责 | 允许依赖 |
|------|------|------|----------|
| `internal/domain/conversation/` | Domain | 核心值对象：Message, ToolCall, Client 接口 | 仅标准库 |
| `internal/domain/tool/` | Domain | Tool 接口、ErrorKind | 仅标准库 + domain/conversation |
| `internal/domain/agentloop/` | Domain | Step, Plan, Planner/Executor 接口 | 仅标准库 + domain/* |
| `internal/domain/memory/` | Domain | Entry 值对象、Store 接口 | 仅标准库 |
| `internal/domain/ctxwindow/` | Domain | Priority, ModelProfile, Manager 接口 | 仅 domain/conversation |
| `internal/domain/structured/` | Domain | Schema, Extractor 接口 | 仅 domain/conversation |
| `internal/usecase/chat/` | Usecase | 对话循环编排 | domain/* + infrastructure |
| `internal/usecase/planning/` | Usecase | 规划执行编排 | domain/* + infrastructure |
| `internal/usecase/multiagent/` | Usecase | 多 Agent 编排 | domain/* + infrastructure |
| `internal/usecase/structuredoutput/` | Usecase | 结构化输出提取 | domain/* + infrastructure |
| `internal/presenter/cli/` | Presenter | CLI 交互层 | usecase/* + domain/* |
| `internal/presenter/web/` | Presenter | Web HTTP 服务 | usecase/* + domain/* |
| `internal/domain/guardrail/` | Domain | Guard/Pipeline 接口、Action/Phase 值对象 | 仅标准库 |
| `internal/container/` | Container | DI 依赖装配 | 全部内部包 |
| `internal/llm/` | Infrastructure | OpenAI 兼容 LLM 客户端实现 | domain/conversation |
| `internal/tools/` | Infrastructure | 具体工具实现 (echo, calc, read_file, write_file, edit_file...) | domain/tool |
| `internal/memory/` | Infrastructure | JSON 文件持久化记忆存储 | domain/memory |
| `internal/agent/` | Infrastructure | LoopAgent 对话循环实现 | domain/* + llm + tools |
| `internal/ctxwindow/` | Infrastructure | 上下文窗口裁剪实现 | domain/ctxwindow |
| `internal/guardrail/` | Infrastructure | 安全检查实现 (PII/关键词/注入/长度) | domain/guardrail |
| `internal/domain/mcp/` | Domain | MCP 协议接口：Client、Manager、ServerConfig、DiscoveredTool | 仅标准库 |
| `internal/mcp/` | Infrastructure | MCP 实现：Stdio/SSE Client、MCPManager、MCPToolAdapter | domain/mcp + domain/tool |
| `internal/domain/session/` | Domain | 会话持久化接口：Session、Summary、Store | 仅标准库 |
| `internal/domain/rag/` | Domain | RAG 接口：Embedder、Chunker、VectorStore + 值对象 | 仅标准库 |
| `internal/session/` | Infrastructure | 会话持久化实现：JSONStore（文件存储）、Manager | domain/session + domain/conversation |
| `internal/rag/` | Infrastructure | RAG 实现：Engine、TextChunker、TFIDFEmbedder、APIEmbedder、MemoryVectorStore | domain/rag |
| `internal/structured/` | Infrastructure | 结构化输出引擎实现 | domain/structured |
| `cmd/agent/` | Entry | CLI 入口，参数解析 | container + presenter |

## 架构规则（MUST 遵守）

1. **MUST** Domain 层不依赖任何 Infrastructure 实现，只能依赖标准库和其他 domain 包
2. **MUST** 新类型优先定义在 `domain/` 下，老包通过 `type X = domain.X` 别名保持兼容
3. **MUST** 所有新工具实现 `domain/tool.Tool` 接口并注册到 `tools.Registry`
4. **MUST** DI 装配集中在 `internal/container/container.go`，不在 `main.go` 中创建业务对象
5. **MUST** 每个包有清晰的 package doc comment 说明职责
6. **MUST NOT** 在 domain 层引入 `net/http`、`os`、`io` 等 I/O 相关标准库
7. **MUST NOT** 跨层直接依赖（如 Presenter 直接调用 Infrastructure 的内部方法）

## 代码风格

- **命名**: Go 标准命名规范（PascalCase 导出、camelCase 未导出）
- **注释**: 所有导出类型和函数必须有 GoDoc 注释
- **错误处理**: 使用 `fmt.Errorf("context: %w", err)` wrap 错误
- **并发安全**: 共享状态使用 `sync.Mutex` 或 `sync/atomic`
- **测试文件**: `*_test.go` 放在同包目录下
- **行长**: 建议不超过 120 字符

## 关键设计模式

### Type Alias 迁移策略
Domain 层定义 canonical 类型，老包通过 type alias 保持向后兼容：
```go
// internal/llm/client.go
import conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
type Message = conv.Message  // alias, not new type
```

### 充血模型
Domain 值对象暴露行为方法：
```go
// Message 有行为方法
func (m Message) IsToolResponse() bool
func (m Message) HasToolCalls() bool
func (m Message) IsSystemMessage() bool
```

### Container DI 装配
所有对象创建集中在 `container.Build(cfg)`，main.go 只做参数解析和调用 container：
```go
app, err := container.Build(cfg)
chatAgent := app.ChatAgent()
```


### Guardrail 安全检查
Harness 层在 3 个阶段执行安全检查，通过 `gd.Pipeline` 链式调用：
- **PhaseInput** — 用户输入进入 LLM 前
- **PhaseOutput** — LLM 输出返回用户前
- **PhaseToolResult** — 工具执行结果反馈 LLM 前

内置 Guard:
- `PromptInjectionGuard` — 检测 prompt 注入攻击 → Block
- `PIIGuard` — 检测手机/身份证/邮箱/银行卡/IP → Redact
- `KeywordGuard` — 敏感词过滤 → Block 或 Redact
- `LengthGuard` — 输入长度限制 → Block

启用方式：`container.Config{GuardrailMode: true}`

### 会话持久化 (Conversation Persistence)
支持将多轮对话完整保存到磁盘，并在后续会话中恢复。每个 Session 独立存储为 JSON 文件。

**核心组件**：
- `Session` — 包含 ID、标题、消息历史、元数据的聚合根
- `JSONStore` — 基于文件系统的持久化实现（每个会话一个 `.json` 文件）
- `Manager` — 协调会话生命周期，负责 `conv.Message` 与 `session.Message` 的转换

**CLI 用法**：
```bash
agent chat                    # 自动创建新会话并保存
agent chat --resume <ID>      # 恢复之前的会话继续对话
agent sessions                # 列出所有保存的会话
agent sessions --delete <ID>  # 删除指定会话
```

**行为**：
- 每轮对话自动保存（无需手动操作）
- 标题从首条用户消息自动生成（截断至 40 字符）
- `clear`/`reset` 会创建新会话
- 退出时显示会话 ID，方便后续恢复

**存储路径**：`<root>/.agent-sessions/`（可通过 `--session-dir` 覆盖）

### MCP (Model Context Protocol) 集成
MCP 是 Anthropic 提出的开放协议，允许 Agent 动态发现和调用外部工具。本项目使用官方 Go SDK（`github.com/modelcontextprotocol/go-sdk`）。

**架构流程**：
```
Container.Build()
  -> MCPManager.AddServer()       // 连接 MCP Servers (stdio/SSE)
  -> MCPManager.DiscoverAllTools() // 发现工具, 名称资格化为 serverID/toolName
  -> MCPToolAdapter               // 桥接到 tool.Tool 接口
  -> Registry.Register()          // 注册到工具表
  -> LoopAgent.InjectToolDefs()   // 注入 LLM function calling 定义
```

**传输模式**：
- `stdio`: 通过子进程 stdin/stdout 通信（适合本地工具）
- `sse`/`http`: 通过 HTTP Streamable 传输（适合远程服务）

**启用方式**：在 `container.Config` 中配置 `MCPServers` 字段即可。

## 新增功能指南

### 添加新工具
1. 在 `internal/tools/` 创建新文件（如 `my_tool.go`）
2. 实现 `domain/tool.Tool` 接口：`Name() string` + `Execute(ctx, args) (string, error)`
3. 在 `internal/tools/schema.go` 的 `BuiltinSchemas()` 中添加 JSON Schema
4. 在 `internal/container/container.go` 的 `buildRegistry()` 中注册
5. 写测试 `my_tool_test.go`

### 添加新 Domain 概念
1. 在 `internal/domain/` 下创建新包
2. 定义接口和值对象（零外部依赖）
3. 在 Infrastructure 层实现接口
4. 在 Usecase 层编排调用

## 文档资源
- 架构设计文档: @docs/architecture.md
- DDD 重构记录: 参见飞书文档 RxaKdvZvVoGNTbxbPFFc9uHInzc
- 调用链路图: @docs/agent-flow.svg

### Context Engine Phase 1 优化
基于 Claude Code context engine 的 6 层设计理念，在已有的 2 层裁剪系统（Manager.Fit + SmartManager.SmartFit）上增加 3 项低成本高收益改进：

**1. TruncationCache（决策冻结）** — `internal/ctxwindow/manager.go`
- 按 `tool_call_id` 缓存截断后的内容，同一工具结果只截断一次
- 后续 `Fit()` 调用直接复用缓存，保证 prompt 前缀稳定性
- 最大化 LLM API prefix cache 命中率，减少计费和延迟
- `ClearTruncationCache()` 在新会话时清空

**2. Cache 冷热感知** — `internal/ctxwindow/manager.go`
- `UpdateLastAssistantTime()` 记录最近 assistant 回复时间
- `CacheTemperature()` 判断 prefix cache 冷热（默认 5 分钟阈值）
- 冷启动时 `effectiveToolResultMaxTokens()` 返回更低的截断上限（默认 50%）
- 热状态下保持正常截断策略，优先维护 prompt 结构稳定

**3. Nudge 效率提醒** — `internal/ctxwindow/nudge.go` + `internal/agent/loop.go`
- 上下文使用率 ≥60% 时注入 `[CONTEXT EFFICIENCY]` 提醒
- 使用率 ≥85% 时升级为 `[CONTEXT CRITICAL]` 紧急提醒
- Nudge 作为临时 system 消息，在最终回复前自动清除，不持久化
- 通过 `SetNudgeEnabled(bool)` 控制开关

**配置参数**：
- `ManagerConfig.ColdThreshold` — cache 冷热阈值（默认 5 分钟）
- `ManagerConfig.ColdAggressiveRatio` — 冷启动截断比例（默认 0.5）
- `NudgeThreshold` / `NudgeCriticalThreshold` — Nudge 触发阈值常量

### RAG (检索增强生成)
为 Agent 提供外部知识检索能力，支持索引文件/文本并在对话中基于语义相似度检索相关片段注入 prompt。

**架构**：
```
Domain 层:  internal/domain/rag/rag.go    — Embedder/Chunker/VectorStore 接口 + 值对象
Infrastructure:
  internal/rag/chunker.go   — TextChunker (递归段落→句子→字符切分)
  internal/rag/embedder.go  — TFIDFEmbedder (零依赖) / APIEmbedder (OpenAI 兼容)
  internal/rag/store.go     — MemoryVectorStore (内存余弦搜索 + JSON 持久化)
  internal/rag/engine.go    — Engine 编排器 (索引/查询/格式化)
Tools:
  internal/tools/rag_tool.go   — 4 个 RAG 工具实现
  internal/tools/rag_schema.go — JSON Schema 定义
```

**Domain 接口** (`internal/domain/rag/rag.go`)：
- `Embedder` — 文本向量化（`Embed`, `EmbedBatch`, `Dimension`）
- `Chunker` — 文本切块（`Split(text, chunkSize, overlap)`）
- `VectorStore` — 向量存储（`AddDocument`, `Query`, `Delete`, `Save/Load`）
- 值对象：`Document`, `Chunk`, `QueryResult`, `IndexStats`

**Embedding 策略**：
| 实现 | 场景 | 依赖 |
|------|------|------|
| `TFIDFEmbedder` | 默认，轻量本地 | 零外部依赖，hash-to-dim + L2 归一化 |
| `APIEmbedder` | 生产环境 | OpenAI 兼容 API（支持 DashScope/Qwen/Ollama）|

**5 个 RAG 工具**：
| 工具名 | 功能 | 关键参数 |
|--------|------|----------|
| `rag_import` | 批量导入知识库目录 | `path`(目录/glob), `recursive`, `extensions`, `glob` |
| `rag_index` | 索引单个文件或文本 | `file` / `content`+`title` |
| `rag_query` | 语义检索 | `query`, `top_k`(默认5) |
| `rag_list` | 列出已索引文档 | 无 |
| `rag_delete` | 删除已索引文档 | `doc_id` |

**CLI 用法**：
```bash
# 启用 RAG（使用默认 TF-IDF 嵌入）
agent chat --rag

# 启动时自动加载知识库目录
agent chat --rag-load ./docs

# 使用 chromem-go 后端 + DashScope 嵌入
export DASHSCOPE_API_KEY=sk-xxx
agent chat --rag --rag-backend chromem

# 指定索引目录
agent chat --rag --rag-dir /path/to/index

# 对话中 Agent 自动使用 RAG 工具：
#   用户: "请索引 ./docs 目录下的 README.md"
#   Agent: 调用 rag_index → 文件被切块、嵌入、存储
#   用户: "Go 的并发模型是什么？"
#   Agent: 调用 rag_query → 检索相关片段 → 结合知识回答
```

**Container 集成**：
- `Config.RAGMode bool` — 启用 RAG
- `Config.RAGDir string` — 索引持久化目录（默认 `~/.agent-rag`）
- `Config.EmbeddingModel string` — 预留 API 嵌入模型名
- `App.RAGEngine *rag.Engine` — 注入的 RAG 引擎实例

**知识库批量导入**：
- `IndexDirectory()` — 递归扫描目录，按扩展名过滤，跳过隐藏目录/node_modules/vendor
- `IndexGlob()` — 按 glob 模式匹配文件（如 `./docs/*.md`）
- 默认支持 40+ 种文本格式：`.txt .md .go .py .js .ts .java .json .yaml .html .sql` 等
- 自动跳过已索引文件、空文件、超大文件（默认 1MB 上限）
- 导入选项：`Recursive`(递归), `Extensions`(扩展名过滤), `GlobPattern`(模式匹配), `MaxFileSize`(大小限制)

**向量存储后端**：
| 后端 | 启用方式 | 特点 |
|------|----------|------|
| Legacy (默认) | `--rag` | 零依赖 TF-IDF，JSON 持久化，适合开发测试 |
| chromem-go | `--rag --rag-backend chromem` | 内置 OpenAI/DashScope/Ollama 嵌入，gob+gzip 持久化，生产推荐 |

**System Prompt 规则**（RAG 启用时追加）：
- 用户要求索引文件/文本时调用 `rag_index`
- 回答问题前先调用 `rag_query` 检索相关知识
- 检索到相关内容时标注"基于已索引知识"

**测试**：`go test ./internal/rag/... -v`（18 个测试覆盖全部组件 + 批量导入）

### Web UI (图形界面)

**架构**: Go 嵌入式前端，所有 HTML/JS/CSS 通过 Go 字符串编译进二进制。

| 文件 | 职责 |
|------|------|
| `internal/web/server.go` | HTTP 路由、SSE 流式、会话管理、API 端点 |
| `internal/web/html.go` | HTML 模板（header + 上下文面板 + 聊天容器 + 输入区） |
| `internal/web/js.go` | 前端 JS（SSE 客户端、消息渲染、Markdown 解析、上下文用量更新） |
| `internal/web/css.go` | 样式（暗色主题、响应式、进度条动画） |
| `internal/web/static.go` | `init()` 拼接静态资源 |

**API 端点**：
| 端点 | 方法 | 功能 |
|------|------|------|
| `/api/chat` | POST | 非流式对话 |
| `/api/chat/stream` | POST | SSE 流式对话 |
| `/api/sessions/clear` | POST | 清空会话 |
| `/api/status` | GET | 系统状态（会话数、记忆数） |
| `/api/context` | GET | 上下文窗口 + Token 用量详情 |

**上下文用量面板**（v2 新增）：
- 上下文窗口进度条：实时显示已用/总容量 tokens，颜色随使用率变化（绿→黄→橙→红）
- Token 用量网格：总计/输入/输出/调用次数
- 预算显示：设置 token 预算时显示使用百分比
- SSE `context` 事件：每次对话完成后自动推送上下文信息，无需额外轮询
- 可折叠：顶栏图表按钮切换显示/隐藏

**SSE 事件类型**：
| 事件 | 数据 | 说明 |
|------|------|------|
| `session` | session_id | 会话标识 |
| `delta` | text | 流式文本增量 |
| `context` | JSON | 上下文窗口 + Token 用量（每次对话结束推送） |
| `done` | full_text | 对话完成 |
| `error` | error_msg | 错误信息 |
