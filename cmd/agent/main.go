package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/container"
	"github.com/SOULOFCINDERS/agent/internal/executor"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/planner"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	if len(os.Args) < 2 {
		printUsage()
		return 2
	}

	switch os.Args[1] {
	case "run":
		return runCmd(os.Args[2:])
	case "chat":
		return chatCmd(os.Args[2:])
	case "web":
		return webCmd(os.Args[2:])
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		return 2
	}
}

func printUsage() {
	_, _ = fmt.Fprintln(os.Stderr, `usage: agent <command> [args]

commands:
  run    run agent on a single task (rule-based planner)
  chat   start interactive chat with LLM-powered agent
  web    start web UI with HTTP server

chat options:
  --trace           print trace events to stderr
  --root DIR        root directory for file tools (default: .)
  --base-url URL    LLM API base URL (or env LLM_BASE_URL)
  --api-key KEY     LLM API key (or env LLM_API_KEY)
  --model NAME      LLM model name (or env LLM_MODEL)
  --stream          enable streaming output (token-by-token)
  --search          enable web search tools (web_search + web_fetch)
  --feishu          enable Feishu doc tools (requires env FEISHU_APP_ID, FEISHU_APP_SECRET)
  --budget N        set token budget limit (0 = unlimited, default: 0)
  --multi-agent     enable multi-agent mode (orchestrator + sub-agents)
  --ctx-window N    override context window size (tokens)`)
}

// ---------- run 命令（保留原有功能，不经过 Container） ----------

func runCmd(args []string) int {
	var (
		trace bool
		jsonO bool
		root  = "."
	)

	var taskParts []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--trace":
			trace = true
		case a == "--json":
			jsonO = true
		case a == "--root":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintln(os.Stderr, "usage: agent run [--trace] [--json] [--root DIR] <task>")
				return 2
			}
			i++
			root = args[i]
		case strings.HasPrefix(a, "--root="):
			root = strings.TrimPrefix(a, "--root=")
		case a == "-h" || a == "--help":
			_, _ = fmt.Fprintln(os.Stderr, "usage: agent run [--trace] [--json] [--root DIR] <task>")
			return 2
		default:
			taskParts = append(taskParts, a)
		}
	}

	task := strings.TrimSpace(strings.Join(taskParts, " "))
	if task == "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			return 1
		}
		task = strings.TrimSpace(string(b))
	}
	if task == "" {
		_, _ = fmt.Fprintln(os.Stderr, "empty task")
		return 2
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}

	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())
	reg.Register(tools.NewCalcTool())
	reg.Register(tools.NewReadFileTool(absRoot))
	reg.Register(tools.NewListDirTool(absRoot))
	reg.Register(tools.NewGrepRepoTool(absRoot))
	reg.Register(tools.NewSummarizeTool())
	reg.Register(tools.NewWeatherTool())

	p := planner.NewRulePlanner()
	ex := executor.NewExecutor(reg)
	a := agent.New(p, ex)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var traceW io.Writer = io.Discard
	if trace {
		traceW = os.Stderr
	}

	out, runTrace, err := a.Run(ctx, task, traceW)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}

	if jsonO {
		b, _ := json.MarshalIndent(map[string]any{
			"output": out,
			"trace":  runTrace,
		}, "", "  ")
		_, _ = fmt.Fprintln(os.Stdout, string(b))
		return 0
	}

	_, _ = fmt.Fprintln(os.Stdout, out)
	return 0
}

// ---------- chat 命令（通过 Container 装配） ----------

func chatCmd(args []string) int {
	cfg := parseCommonFlags(args)

	if cfg.FeishuMode {
		if os.Getenv("FEISHU_APP_ID") == "" || os.Getenv("FEISHU_APP_SECRET") == "" {
			_, _ = fmt.Fprintln(os.Stderr, "⚠️  --feishu requires env FEISHU_APP_ID and FEISHU_APP_SECRET")
			return 1
		}
	}

	var traceW io.Writer = io.Discard
	if cfg.Trace {
		traceW = os.Stderr
	}
	cfg.TraceWriter = traceW

	app, err := container.Build(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "❌ 初始化失败: %s\n", err)
		return 1
	}

	// 打印启动信息
	printStartupInfo(app)

	return runChatLoop(app.ChatAgent(), cfg.StreamMode, app.UsageTracker)
}

// ---------- web 命令（通过 Container 装配） ----------

func webCmd(args []string) int {
	cfg := parseCommonFlags(args)
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}

	if cfg.FeishuMode {
		if os.Getenv("FEISHU_APP_ID") == "" || os.Getenv("FEISHU_APP_SECRET") == "" {
			_, _ = fmt.Fprintln(os.Stderr, "⚠️  --feishu requires env FEISHU_APP_ID and FEISHU_APP_SECRET")
			return 1
		}
	}

	var traceW io.Writer = io.Discard
	if cfg.Trace {
		traceW = os.Stderr
	}
	cfg.TraceWriter = traceW

	app, err := container.Build(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "❌ 初始化失败: %s\n", err)
		return 1
	}

	printStartupInfo(app)

	srv := app.BuildWebServer()
	if err := srv.Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "❌ Server error: %s\n", err)
		return 1
	}
	return 0
}

// ---------- 公共辅助 ----------

// parseCommonFlags 解析 chat / web 共用的命令行参数
func parseCommonFlags(args []string) container.Config {
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
	if app.Orchestrator != nil {
		_, _ = fmt.Fprintf(os.Stderr, "🤝 Multi-Agent 模式已启用\n")
	}
}

// runChatLoop 交互式对话循环
func runChatLoop(ca container.ChatAgent, streamMode bool, usageTracker *llm.UsageTracker) int {
	_, _ = fmt.Fprintln(os.Stdout, "")
	_, _ = fmt.Fprintln(os.Stdout, "╔══════════════════════════════════════════╗")
	_, _ = fmt.Fprintln(os.Stdout, "║       🤖 Agent Chat (输入 quit 退出)      ║")
	_, _ = fmt.Fprintln(os.Stdout, "╚══════════════════════════════════════════╝")
	_, _ = fmt.Fprintln(os.Stdout, "")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var history []llm.Message

	for {
		_, _ = fmt.Fprint(os.Stdout, "你: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "quit" || input == "exit" || input == "q" {
			_, _ = fmt.Fprintf(os.Stdout, "\n👋 再见! 本次会话 %s\n", usageTracker.Summary())
			break
		}
		if input == "clear" || input == "reset" {
			history = nil
			_, _ = fmt.Fprintln(os.Stdout, "🔄 对话已重置")
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)

		if streamMode {
			_, _ = fmt.Fprint(os.Stdout, "\nAgent: ")
			reply, newHistory, err := ca.ChatStream(ctx, input, history, func(delta string) {
				_, _ = fmt.Fprint(os.Stdout, delta)
			})
			cancel()
			if err != nil {
				if strings.Contains(err.Error(), "token budget exceeded") {
					_, _ = fmt.Fprintf(os.Stderr, "\n⚠️  Token 预算已耗尽 (%s)\n\n", usageTracker.Summary())
				} else {
					_, _ = fmt.Fprintf(os.Stderr, "\n❌ 错误: %s\n\n", err)
				}
				continue
			}
			history = newHistory
			_ = reply
			_, _ = fmt.Fprintf(os.Stdout, "\n  [%s]\n\n", usageTracker.Summary())
		} else {
			reply, newHistory, err := ca.Chat(ctx, input, history)
			cancel()
			if err != nil {
				if strings.Contains(err.Error(), "token budget exceeded") {
					_, _ = fmt.Fprintf(os.Stderr, "\n⚠️  Token 预算已耗尽 (%s)\n\n", usageTracker.Summary())
				} else {
					_, _ = fmt.Fprintf(os.Stderr, "\n❌ 错误: %s\n\n", err)
				}
				continue
			}
			history = newHistory
			_, _ = fmt.Fprintf(os.Stdout, "\nAgent: %s\n  [%s]\n\n", reply, usageTracker.Summary())
		}
	}

	return 0
}
