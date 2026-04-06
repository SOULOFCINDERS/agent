## 1. Tool Implementation

- [ ] 1.1 实现 `global time` 工具，支持 IANA 时区输入
- [ ] 1.2 增加常见城市到 IANA 时区的别名映射
- [ ] 1.3 统一输出为单行、可链式消费的时间字符串

## 2. Planning Integration

- [ ] 2.1 在 Planner 中增加 `time:` 前缀触发规则
- [ ] 2.2 在 Planner 中增加“几点”“time in <city>”等自然语言识别规则
- [ ] 2.3 为无法识别的位置输入设计清晰的报错路径

## 3. Registration and Verification

- [ ] 3.1 在 CLI 初始化流程中注册该工具
- [ ] 3.2 增加工具单元测试与 Planner 识别测试
- [ ] 3.3 增加 README 示例并验证 `go test ./...` 与示例命令通过
