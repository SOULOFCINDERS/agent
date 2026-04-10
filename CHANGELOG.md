# Changelog

## [v0.6.0] - 2026-04-10 — 子Agent上下文污染防护

本版本针对多 Agent 编排场景，实现了 **4 项子 Agent 上下文污染防护机制**，从结果截断、历史窗口、Token 预算、上下文白名单四个维度防止上下文膨胀和信息泄漏。

### 🛡️ 改进1: 子Agent结果摘要截断 (`handoff.go`)

- 新增 `compactResult()` 函数，对超长子 Agent 返回结果自动截断
- **截断策略**：保留前 60% + 后 20%，中间插入 `[已省略约 N tokens]` 标记
- 保留开头（通常是结论）和结尾（通常是总结），信息损失最小化
- 零 LLM 开销，纯字符串操作
- `HandoffTool.SetMaxResultTokens()` 支持动态配置，默认 1000 tokens

### 🪟 改进2: 编排器历史窗口管理 (`orchestrator.go`)

- `OrchestratorConfig` 新增 `MaxHistoryTokens` 配置项
- 新增 `compactOrchestratorHistory()` 函数，每轮对话前自动检查历史 token 总量
- **压缩策略**：从最老的 `role="tool"` 消息开始截断，保留前 200 字符 + `[上下文已压缩]`
- **保护规则**：
  - `system` 消息永远不压缩
  - 最近 4 条消息永远不压缩（保证当前轮次上下文完整性）
- `Chat()` / `ChatStream()` / `ChatStreamV2()` 三个入口统一应用
- `SetMaxHistoryTokens()` 支持运行时动态调整

### 💰 改进3: 子Agent Token 预算控制 (`agent_def.go` + `handoff.go`)

- `AgentDef` 新增 `MaxTokenBudget` 字段（`int64`, 0 = 无限制）
- `HandoffTool.Execute()` 在创建子 Agent 时注入独立的 `llm.UsageTracker`
- 当子 Agent 累计消耗 token 超预算，`LoopAgent` 自动停止循环
- 超预算时返回已有的部分结果 + `[注意: 子Agent已达到token预算上限]` 警告标记
- `Validate()` 校验 `MaxTokenBudget >= 0`
- 预置模板已配置合理预算：
  | Agent | 预算 | 理由 |
  |-------|------|------|
  | Research | 50K | 搜索+摘要，中等开销 |
  | Code | 80K | 代码分析需要读取大量文件 |
  | Writer | 40K | 文档生成相对可控 |

### 🔒 改进4: 上下文传递白名单 (`handoff.go`)

- `AgentDef` 新增 `AcceptContext []string` 字段
- 新增 `filterContext()` 函数，按行解析 `key: value` 格式
- 只有 key 在白名单中的行才会传递给子 Agent
- **兼容规则**：
  - 支持中英文冒号（`:` / `：`）
  - key 匹配大小写不敏感
  - 非 key-value 格式的纯文本行始终保留（不误伤自由文本）
  - 空白名单 = 全部通过（向后兼容）
- `extractLineKey()` 辅助函数：启发式提取行首 key

### 📈 测试覆盖

| 改进 | 新增测试 | 覆盖场景 |
|------|----------|----------|
| 结果截断 | 5 | 短结果/长结果/零限制/边界/端到端 |
| 历史窗口 | 5 | 无需压缩/压缩ToolResult/保护最近消息/跳过System/配置生效 |
| Token预算 | 2 | 有预算/无预算 |
| 上下文白名单 | 7 | 无白名单/空上下文/过滤/中文冒号/大小写/extractLineKey/端到端 |
| 辅助函数 | 2 | estimateStringTokens/estimateHistoryTokens |
| **合计** | **21** | |

multiagent 包总测试：**40 个**（含子测试），全部 PASS。
全量 `go test ./...`：**15 个 package**，零失败。

### 📁 文件变更统计

```
修改文件 (4):
  internal/multiagent/agent_def.go           181 行 (+32)
  internal/multiagent/handoff.go             282 行 (+181)
  internal/multiagent/orchestrator.go        372 行 (+123)
  internal/multiagent/orchestrator_test.go   834 行 (+494)

总计: +782 行 / -48 行 = 净增 734 行
```

### Git Commits

```
3f56de3 feat(multiagent): 子Agent上下文污染防护4项改进
```

---

## [v0.5.0] - 2026-04-10 — 记忆系统量化评估 & 高级压缩

本版本对 Agent 记忆系统进行了全面增强，覆盖 **冲突检测 → 高级压缩 → 量化评估** 完整链路。

### 📊 新增：量化评估体系 (`5a22b11`)

**嵌入式指标采集 (metrics.go, 523 行)**
- `MemoryMetrics` 采集器，支持 Search / Compress / Conflict / Compact 四类指标
- 检索指标：延迟 P50/P99、平均结果数、零结果率、TopScore
- 压缩指标：压缩率、Token 节省量、增量压缩占比
- 冲突指标：类型分布（显式/语义/待确认）、自动解决率
- 合并指标：聚类数、合并条数、平均簇大小
- JSON 持久化 + `ReportString()` 可读报告
- 零额外 LLM 开销

**离线评估框架 (eval.go, 614 行)**
- 标准 IR 指标：`Recall@K`, `Precision@K`, `MRR`, `nDCG`
- 检索评估：基于标注 query-relevantIDs 数据集
- 压缩评估：真实压缩 + 关键词匹配信息保留率
- 冲突检测评估：`Precision` / `Recall` / `F1`
- JSON 格式评估数据集加载 + 报告导出

**埋点集成（3 个文件，+48 行）**
- `store.go`：`Search()` 自动采集延迟/结果数/TopScore；`Add()` P0 冲突上报
- `compressor.go`：`Compress()` 采集压缩率/Token 节省/是否增量
- `compactor.go`：`Compact()` 采集聚类数/合并数/新建数

### 🗜️ 新增：高级压缩能力 (`d2ab125`)

**增量压缩 + Token-based 动态触发 (compressor.go)**
- 增量压缩：检测 `[对话历史摘要]` 标记，只压缩新消息
- Token 预算：`MaxTokens` 触发阈值 + `TargetTokens` 目标预算
- 双触发机制：消息数 OR Token 数，任一超标即触发
- 动态窗口：`dynamicWindowSize()` 根据 Token 预算自适应保留轮数
- 完全向后兼容：仅设置 `MaxMessages` 时行为与原版相同

**分层摘要 (hierarchical.go, 379 行)**
- L0（原始消息）→ L1（每 N 轮 chunk 摘要）→ L2（全局 session 摘要）
- L1 结构化输出：摘要 + 关键实体
- L2 增量更新：合并新 L1 chunk 到已有 session 摘要
- JSON 文件持久化

**摘要质量验证 (verifier.go, 321 行)**
- LLM 提取关键事实 → 关键词匹配覆盖率检查 → 低覆盖时 LLM 增强
- 中英文双模关键词提取（英文分词 + 中文 bigram）
- 默认关闭（`Enabled: false`），按需启用

**长期记忆合并 (compactor.go, 310 行)**
- TF-IDF 向量化 → 贪心聚类（余弦相似度阈值）→ LLM 合并
- 支持 `DryRun` 预览模式
- 软删除模式：`SupersededBy` 标记被替代记忆

### 🔍 新增：记忆冲突检测 (`cf1d3a9`)

**冲突检测流水线 (conflict.go, 184 行)**
- P0：Topic 精确匹配 + 否定模式检测（15 种中英文模式）
- P1：TF-IDF embedding 语义相似度（阈值 0.6）
- P2：置信度裁决（差值 > 0.2 自动决策）
- P3：无法自动裁决 → 标记 NeedConfirm

**增强 (store.go, domain/memory.go)**
- `Entry` 新增 `SupersededBy`, `Confidence`, `Embedding` 字段
- `DecayConfidence()` 半衰期 90 天 + 访问频率加成
- `RelevantSummary()` 时效标注（🟢🟡🔴）
- `IsActive()` 软删除过滤

---

### 📈 测试覆盖

| 模块 | 测试文件 | 测试数 |
|------|----------|--------|
| metrics | metrics_test.go | 9 |
| eval | eval_test.go | 13 |
| compressor | compressor_test.go | 16 |
| hierarchical | hierarchical_test.go | 5 |
| verifier | verifier_test.go | 5 |
| compactor | compactor_test.go | 5 |
| conflict | conflict_test.go | 7 |
| store_conflict | store_conflict_test.go | 6 |
| store+metrics | (in eval_test.go) | 1 |
| **合计** | **8 个文件** | **78** (含子测试) |

全量 `go test ./...` 通过（15 个 package，零失败）。

### 📁 文件变更统计

```
新增文件 (10):
  internal/memory/metrics.go           523 行
  internal/memory/metrics_test.go      209 行
  internal/memory/eval.go              614 行
  internal/memory/eval_test.go         456 行
  internal/memory/hierarchical.go      379 行
  internal/memory/hierarchical_test.go 209 行
  internal/memory/verifier.go          321 行
  internal/memory/verifier_test.go     161 行
  internal/memory/compactor.go         310 行
  internal/memory/compactor_test.go    186 行

修改文件 (5):
  internal/memory/store.go             +25 行（metrics 埋点）
  internal/memory/compressor.go        +12 行（metrics 埋点）+ 342 行重写
  internal/memory/compactor.go         +11 行（metrics 埋点）
  internal/domain/memory/memory.go     +54 行（新字段）
  internal/memory/conflict.go          184 行（新增）

总计: ~4,800 行新增代码
```
