# DeepEval 对照测试

## 架构

```
Go Agent Framework                    Python DeepEval
┌──────────────────┐                  ┌─────────────────────┐
│  Benchmark Runner │  ──export──►    │  run_deepeval.py    │
│  (Go, 20 cases)   │                 │                     │
│                    │                 │  Faithfulness ✓     │
│  deepeval_bridge   │  ◄──import──   │  Hallucination ✓    │
│  (对比报告)         │                │  Answer Relevancy ✓ │
└──────────────────┘                  └─────────────────────┘
```

## 快速开始

### 1. 安装 DeepEval
```bash
pip install deepeval
```

### 2. 配置 LLM Judge
DeepEval 需要一个 LLM 做 Judge（默认用 OpenAI）：
```bash
# 方式1: OpenAI
export OPENAI_API_KEY=sk-xxx

# 方式2: 自定义 API（DeepSeek / Qwen / 本地）
export DEEPEVAL_BASE_URL=http://localhost:11434/v1
export DEEPEVAL_API_KEY=ollama
export DEEPEVAL_MODEL=qwen2.5:14b
```

### 3. 从 Go Benchmark 导出数据
```bash
cd /path/to/agent
go test ./internal/benchmark/ -run TestExportDeepEval -v
# 或者用 CLI:
# go run ./cmd/benchmark -export-deepeval ./tmp/deepeval_input.json
```

### 4. 运行 DeepEval
```bash
python scripts/deepeval/run_deepeval.py \
    --input ./tmp/deepeval_input.json \
    --output ./tmp/deepeval_results.json
```

### 5. 导入结果对比
```bash
go test ./internal/benchmark/ -run TestImportDeepEval -v
```

## 评测指标

| 指标 | 说明 | 与我们的对应关系 |
|---|---|---|
| Faithfulness | 回答是否忠于提供的上下文 | 对应 L4 Fabrication Guard |
| Hallucination | 是否编造了上下文中没有的信息 | 对应 L4 Fabrication Guard |
| Answer Relevancy | 回答是否与问题相关 | 对应 D4 任务完成度 |
