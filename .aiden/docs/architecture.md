# 架构说明

## DDD 四层架构

```
┌─────────────────────────────────────────┐
│           Presenter Layer               │  ← CLI / Web 入口
│         (presenter/cli, presenter/web)  │
├─────────────────────────────────────────┤
│           Usecase Layer                 │  ← 业务用例编排
│   (usecase/chat, usecase/planning, ...) │
├─────────────────────────────────────────┤
│            Domain Layer                 │  ← 核心领域模型
│  (domain/conversation, domain/tool, ...) │
├─────────────────────────────────────────┤
│         Infrastructure Layer            │  ← 外部依赖实现
│       (llm, tools, memory, agent, ...)  │
└─────────────────────────────────────────┘
```

## 依赖方向

- Domain 层 **零外部依赖**，只依赖标准库
- Usecase 依赖 Domain 接口 + Infrastructure 实现
- Presenter 依赖 Usecase
- Container 装配所有层，被 main.go 调用
- Infrastructure 实现 Domain 定义的接口

## 核心 Domain 包

### domain/conversation
对话领域的核心值对象和接口：
- `Message` — 消息值对象（充血模型，含 `IsToolResponse()`、`HasToolCalls()` 等行为方法）
- `ToolCall` / `FunctionCall` — 工具调用描述
- `ToolDef` / `FuncDef` — 工具定义（JSON Schema）
- `Usage` — Token 用量统计
- `ChatResponse` — LLM 响应
- `StreamDelta` / `ToolCallDelta` — 流式响应增量
- `Client` / `StreamingClient` / `FormatClient` — LLM 客户端接口（依赖倒置）

### domain/tool
工具领域：
- `Tool` — 工具接口 (`Name() + Execute()`)
- `ToolWithSchema` — 带 JSON Schema 的工具接口
- `Registry` — 工具注册表接口
- `ErrorKind` / `Error` — 工具错误分类

### domain/agentloop
Agent 循环领域：
- `StepKind` / `Step` / `Plan` — 规划步骤
- `Planner` / `Executor` — 规划器和执行器接口
- `TraceEvent` — 追踪事件

### domain/memory
记忆领域：
- `Entry` — 记忆条目值对象
- `Store` — 记忆存储接口

### domain/ctxwindow
上下文窗口领域：
- `Priority` — 消息优先级
- `ModelProfile` — 模型配置
- `Manager` / `TokenEstimator` — 窗口管理接口

## Type Alias 迁移策略

为保持向后兼容，老包（如 `internal/llm`）中的类型通过 Go type alias 指向 domain：

```go
// internal/llm/client.go
type Message = conv.Message       // 别名，非新类型
type ChatResponse = conv.ChatResponse
type Client = conv.Client
```

所有现有调用者（如 `agent.LoopAgent` 使用 `llm.Message`）无需修改 import path。

## 数据流

```
用户输入
  → main.go (参数解析)
  → container.Build() (DI 装配)
  → ChatAgent.Chat() (usecase 层)
  → LoopAgent.Chat() (agent 循环)
    → Client.Chat() (LLM 调用, infrastructure)
    → Tool.Execute() (工具执行, infrastructure)
    → ContextManager.Fit() (窗口裁剪)
    → Compressor.Compress() (历史压缩)
  → 返回结果
```
