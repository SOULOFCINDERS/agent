# 术语表

| 术语 | 含义 |
|------|------|
| LoopAgent | 核心对话循环 Agent，负责 LLM 调用 → 工具执行 → 结果反馈的迭代循环 |
| Orchestrator | 多 Agent 编排器，管理多个 LoopAgent 协作完成复杂任务 |
| ToolDef | 工具定义，包含函数名、描述、参数 JSON Schema，发送给 LLM |
| ToolCall | LLM 返回的工具调用指令，包含 ID、函数名、参数 JSON |
| ChatResponse | LLM 的完整响应，包含 Message + FinishReason + Usage |
| StreamDelta | 流式响应的增量片段，逐 token 返回 |
| ContextManager | 上下文窗口管理器，负责在 LLM 调用前裁剪超长历史 |
| Compressor | 历史压缩器，使用 LLM 摘要替代旧对话，节省 token |
| UsageTracker | Token 用量追踪器，支持预算限制 |
| Registry | 工具注册表，管理所有可用工具的注册和查找 |
| Container | DI 容器，集中管理所有依赖的创建和装配 |
| Type Alias | Go 类型别名（`type X = Y`），用于在老包中引用 domain 类型而不破坏兼容 |
| 充血模型 | Domain 值对象带有行为方法（如 `Message.IsToolResponse()`），而非仅暴露数据 |
| StreamWriter | 流式输出回调函数类型 `func(delta string)`，用于逐 token 输出 |
| Plan/Step | 规划执行模式中的任务计划和步骤，由 Planner 生成、Executor 执行 |
| PreCheck/PostCheck | LoopAgent 循环中 LLM 调用前后的检查点（token 预算、上下文裁剪等） |
