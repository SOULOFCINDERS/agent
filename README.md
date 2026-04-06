# Agent — Go LLM Agent Framework

一个从零开始、可逐步迭代的工具调用型 LLM Agent 框架，采用 DDD 四层架构设计。

## 技术栈

| 项目 | 说明 |
|------|------|
| 语言 | Go 1.22 |
| 模块 | `github.com/SOULOFCINDERS/agent` |
| 架构 | DDD 四层 (Domain → Usecase → Presenter → Infrastructure) |
| 入口 | `cmd/agent/main.go` |
| 依赖注入 | `internal/container/` (集中式 DI) |

## 快速开始

### 前置条件

- Go 1.22+
- 设置环境变量 `OPENAI_API_KEY`（或使用 `--mock` 模式）

### 安装与构建

```bash
git clone https://github.com/SOULOFCINDERS/agent.git
cd agent
go build ./...
```

### 运行

```bash
# 单次任务执行
go run ./cmd/agent run "calc: (1+2)*3"

# 带 trace 输出
go run ./cmd/agent run --trace "echo: 你好"

# 交互式对话 (Mock 模式，无需 API Key)
go run ./cmd/agent chat --mock

# 交互式对话 (真实 LLM)
go run ./cmd/agent chat

# 流式输出
go run ./cmd/agent chat --stream

# Web 服务
go run ./cmd/agent web --port 8080
```

### 测试

```bash
# 全部测试
go test ./...

# 单个包测试
go test ./internal/llm/

# 带覆盖率
go test -cover ./...

# 详细输出
go test -v ./internal/agent/
```

## 项目结构

```
agent/
├── cmd/agent/              # 应用入口
│   └── main.go             # CLI 入口 (run/chat/web 子命令)
├── internal/
│   ├── domain/             # 领域层 — 核心业务概念，零外部依赖
│   │   ├── conversation/   #   会话模型 (Message, ToolDef, Client)
│   │   ├── tool/           #   工具模型 (ErrorKind, Error)
│   │   ├── agentloop/      #   Agent 循环模型 (Plan, Step, Planner, Executor)
│   │   ├── memory/         #   记忆模型 (Entry)
│   │   ├── ctxwindow/      #   上下文窗口模型 (Strategy)
│   │   └── structured/     #   结构化输出模型 (Schema, Config, Extractor)
│   ├── usecase/            # 用例层 — 编排业务流程
│   │   ├── chat/           #   对话用例
│   │   ├── planning/       #   规划用例
│   │   ├── multiagent/     #   多 Agent 协作用例
│   │   └── structuredoutput/ # 结构化输出用例
│   ├── presenter/          # 展示层 — 对外接口适配
│   │   ├── cli/            #   CLI 配置
│   │   └── web/            #   Web 服务
│   ├── container/          # 依赖注入容器
│   ├── agent/              # Agent 核心循环 (LoopAgent)
│   ├── llm/                # LLM 客户端 (OpenAI 兼容)
│   ├── tools/              # 工具注册表与内置工具
│   ├── memory/             # 记忆存储实现
│   ├── planner/            # 规划器实现
│   ├── executor/           # 执行器实现
│   ├── multiagent/         # 多 Agent 编排实现
│   ├── ctxwindow/          # 上下文窗口管理实现
│   ├── structured/         # 结构化输出引擎实现
│   └── web/                # Web 服务实现
├── docs/                   # 文档资源
│   └── agent-flow.svg      # 架构流程图
├── .aiden/docs/            # AI Agent 知识库
│   ├── architecture.md     # DDD 架构详解
│   ├── tools-guide.md      # 工具开发指南
│   └── terminology.md      # 术语表
├── AGENTS.md               # AI 编码代理项目说明
├── Makefile                # 常用构建命令
└── go.mod
```

## 架构概览

本项目采用 DDD 四层架构，依赖方向严格单向：

```
Presenter / CLI / Web
        ↓
     Usecase（编排业务流程）
        ↓
     Domain（核心领域模型，零依赖）
        ↑
  Infrastructure（LLM / Tools / Memory 等实现）
```

**核心设计原则：**

- **Domain 层零外部依赖**：所有领域概念定义在 `internal/domain/` 下，不导入任何外部包
- **Type Alias 向后兼容**：基础设施层通过 `type X = domain.X` 保持旧导入路径可用
- **充血模型**：Message 等领域对象自带行为方法（如 `IsToolResponse()`, `HasToolCalls()`）
- **集中式 DI**：`container.Build(cfg)` 统一组装所有依赖

### 数据流

```
用户输入 → CLI/Web → Usecase → Agent(LoopAgent) → LLM Client
                                    ↕
                              Tool Registry → 内置工具 (echo/calc/read_file/...)
                                    ↕
                              Memory Store → 上下文压缩
```

## 内置工具

| 工具 | 说明 |
|------|------|
| `echo` | 回显文本 |
| `calc` | 数学表达式计算 |
| `read_file` | 读取文件内容 |
| `write_file` | 写入文件 |
| `list_dir` | 列出目录内容 |
| `grep_repo` | 在代码仓库中搜索 |
| `summarize` | 文本摘要 |
| `http_get` | HTTP GET 请求 |
| `http_post` | HTTP POST 请求 |
| `shell` | 执行 Shell 命令 |

## 开发指南

### 添加新工具

1. 在 `internal/tools/` 下创建新文件
2. 实现 `Execute(ctx, args) (string, error)` 函数
3. 在 `registry.go` 注册工具
4. 在 `schema.go` 添加 JSON Schema

详见 [.aiden/docs/tools-guide.md](.aiden/docs/tools-guide.md)

### 添加新领域概念

1. 在 `internal/domain/` 下创建新子包
2. 定义接口和值对象
3. 在基础设施层添加 Type Alias：`type X = domain.X`
4. 在 `container/` 中注册依赖组装

详见 [.aiden/docs/architecture.md](.aiden/docs/architecture.md)

## 文档资源

- [AGENTS.md](AGENTS.md) — AI 编码代理项目说明
- [.aiden/docs/architecture.md](.aiden/docs/architecture.md) — DDD 架构详解
- [.aiden/docs/tools-guide.md](.aiden/docs/tools-guide.md) — 工具开发指南
- [.aiden/docs/terminology.md](.aiden/docs/terminology.md) — 术语表

## License

Private — Internal Use Only
