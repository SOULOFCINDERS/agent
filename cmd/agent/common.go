package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/container"
	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// 包级 RAG 加载选项（由 parseCommonFlags 设置）
var ragLoadDir string
var ragBackend string

// parseCommonFlags 解析 chat / web 共用的命令行参数
func parseCommonFlags(args []string) container.Config {
	// 重置包级 RAG 选项
	ragLoadDir = ""
	ragBackend = ""

	cfg := container.Config{
		Root: ".",
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--trace":
			cfg.Trace = true
		case a == "--mock":
			cfg.Mock = true
		case a == "--stream":
			cfg.StreamMode = true
		case a == "--multi-agent":
			cfg.MultiAgentMode = true
		case a == "--search":
			cfg.SearchMode = true
		case a == "--feishu":
			cfg.FeishuMode = true
		case a == "--memory":
			cfg.MemoryMode = true
		case a == "--verify":
			cfg.VerifyMode = true
		case a == "--rag":
			cfg.RAGMode = true
		case a == "--rag-dir":
			if i+1 < len(args) {
				i++
				cfg.RAGDir = args[i]
			}
		case strings.HasPrefix(a, "--rag-dir="):
			cfg.RAGDir = strings.TrimPrefix(a, "--rag-dir=")
		case a == "--rag-load":
			i++
			if i < len(args) {
				ragLoadDir = args[i]
				cfg.RAGMode = true // 自动启用 RAG
			}
		case strings.HasPrefix(a, "--rag-load="):
			ragLoadDir = strings.TrimPrefix(a, "--rag-load=")
			cfg.RAGMode = true
		case a == "--rag-backend":
			i++
			if i < len(args) {
				ragBackend = args[i]
			}
		case strings.HasPrefix(a, "--rag-backend="):
			ragBackend = strings.TrimPrefix(a, "--rag-backend=")
		case a == "--budget":
			if i+1 < len(args) {
				i++
				if v, err := strconv.ParseInt(args[i], 10, 64); err == nil {
					cfg.Budget = v
				}
			}
		case strings.HasPrefix(a, "--budget="):
			if v, err := strconv.ParseInt(strings.TrimPrefix(a, "--budget="), 10, 64); err == nil {
				cfg.Budget = v
			}
		case a == "--ctx-window":
			if i+1 < len(args) {
				i++
				if v, err := strconv.Atoi(args[i]); err == nil {
					cfg.CtxWindow = v
				}
			}
		case a == "--mem-dir":
			if i+1 < len(args) {
				i++
				cfg.MemDir = args[i]
			}
		case strings.HasPrefix(a, "--mem-dir="):
			cfg.MemDir = strings.TrimPrefix(a, "--mem-dir=")
		case a == "--session-dir":
			if i+1 < len(args) {
				i++
				cfg.SessionDir = args[i]
			}
		case strings.HasPrefix(a, "--session-dir="):
			cfg.SessionDir = strings.TrimPrefix(a, "--session-dir=")
		case a == "--root":
			if i+1 < len(args) {
				i++
				cfg.Root = args[i]
			}
		case strings.HasPrefix(a, "--root="):
			cfg.Root = strings.TrimPrefix(a, "--root=")
		case a == "--base-url":
			if i+1 < len(args) {
				i++
				cfg.BaseURL = args[i]
			}
		case strings.HasPrefix(a, "--base-url="):
			cfg.BaseURL = strings.TrimPrefix(a, "--base-url=")
		case a == "--api-key":
			if i+1 < len(args) {
				i++
				cfg.APIKey = args[i]
			}
		case strings.HasPrefix(a, "--api-key="):
			cfg.APIKey = strings.TrimPrefix(a, "--api-key=")
		case a == "--model":
			if i+1 < len(args) {
				i++
				cfg.Model = args[i]
			}
		case strings.HasPrefix(a, "--model="):
			cfg.Model = strings.TrimPrefix(a, "--model=")
		case a == "--addr":
			if i+1 < len(args) {
				i++
				cfg.Addr = args[i]
			}
		case strings.HasPrefix(a, "--addr="):
			cfg.Addr = strings.TrimPrefix(a, "--addr=")
		}
	}

	return cfg
}

// printStartupInfo 打印启动信息
func printStartupInfo(app *container.App) {
	cfg := app.Config

	if cfg.Mock {
		_, _ = fmt.Fprintln(os.Stderr, "🤖 LLM: Mock (用于演示，无需外部服务)")
	} else if oai, ok := app.LLMClient.(*llm.OpenAICompatClient); ok {
		_, _ = fmt.Fprintf(os.Stderr, "🤖 LLM: %s @ %s\n", oai.Model, oai.BaseURL)
	}

	if cfg.SearchMode {
		_, _ = fmt.Fprintln(os.Stderr, "🌐 联网搜索已启用")
	}
	if cfg.FeishuMode {
		_, _ = fmt.Fprintln(os.Stderr, "✅ 飞书文档工具已启用")
	}
	if app.MemStore != nil {
		_, _ = fmt.Fprintf(os.Stderr, "🧠 记忆功能已启用 (%d 条)\n", app.MemStore.Count())
	}
	if cfg.Budget > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "📊 Token 预算: %d\n", cfg.Budget)
	} else {
		_, _ = fmt.Fprintln(os.Stderr, "📊 Token 用量追踪已启用 (无预算限制)")
	}
	if cfg.VerifyMode {
		_, _ = fmt.Fprintln(os.Stderr, "🔍 Verification Agent 已启用")
	}
	if app.RAGEngine != nil {
		stats := app.RAGEngine.Stats(context.Background())
		_, _ = fmt.Fprintf(os.Stderr, "📚 RAG 知识库已启用 (%d 文档, %d 片段)\n", stats.DocumentCount, stats.ChunkCount)
	}
	if app.Orchestrator != nil {
		_, _ = fmt.Fprintf(os.Stderr, "🤝 Multi-Agent 模式已启用\n")
	}
	if cfg.StreamMode {
		_, _ = fmt.Fprintln(os.Stderr, "🌊 Streaming V2 已启用 (工具状态实时显示)")
	}
}
