## Why

当前 Agent 已具备天气、文件读取、目录浏览和检索等工具，但还缺少一个可直接回答“某地现在几点”的时间能力。新增全球时间 skill 可以让 Agent 覆盖更常见的实用问答场景，并作为后续日程规划、时区换算、多地协同等能力的基础。

## What Changes

- 新增一个 `global-time-skill` 能力，用于查询全球城市或时区的当前本地时间
- 支持显式调用形式，如 `time: Asia/Shanghai`、`time: Tokyo`
- 支持自然语言触发，如“东京现在几点”“time in London”
- 统一输出简洁、可读、可链式消费的时间结果，便于后续 `summarize` 等工具继续处理
- 为该能力补充测试、README 示例与调用说明

## Capabilities

### New Capabilities
- `global-time-skill`: 允许 Agent 根据城市名或时区标识查询该地区当前本地时间，并作为一个可注册工具参与多步任务执行

### Modified Capabilities

## Impact

- Affected code:
  - `internal/tools/`：新增时间查询工具实现
  - `internal/planner/`：增加 `time`/“几点”/“time in” 等触发规则
  - `cmd/agent/`：注册新工具
  - `README.md`：新增使用示例
  - `internal/..._test.go`：增加工具与规划器测试
- Dependencies / systems:
  - 优先使用 Go 标准库 `time` 与时区数据库，避免新增外部服务依赖
  - 若标准库别名映射不足，可增加少量城市到 IANA 时区的内置映射表
