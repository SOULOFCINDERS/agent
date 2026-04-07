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
| `internal/tools/` | Infrastructure | 具体工具实现 (echo, calc, read_file...) | domain/tool |
| `internal/memory/` | Infrastructure | JSON 文件持久化记忆存储 | domain/memory |
| `internal/agent/` | Infrastructure | LoopAgent 对话循环实现 | domain/* + llm + tools |
| `internal/ctxwindow/` | Infrastructure | 上下文窗口裁剪实现 | domain/ctxwindow |
| `internal/guardrail/` | Infrastructure | 安全检查实现 (PII/关键词/注入/长度) | domain/guardrail |
| `internal/domain/mcp/` | Domain | MCP 协议接口：Client、Manager、ServerConfig、DiscoveredTool | 仅标准库 |
| `internal/mcp/` | Infrastructure | MCP 实现：Stdio/SSE Client、MCPManager、MCPToolAdapter | domain/mcp + domain/tool |
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
