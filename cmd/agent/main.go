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
	"github.com/SOULOFCINDERS/agent/internal/executor"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/planner"
	"github.com/SOULOFCINDERS/agent/internal/memory"
	"github.com/SOULOFCINDERS/agent/internal/tools"
	"github.com/SOULOFCINDERS/agent/internal/ctxwindow"
	"github.com/SOULOFCINDERS/agent/internal/multiagent"
	"github.com/SOULOFCINDERS/agent/internal/web"
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

// ---------- run 命令（保留原有功能） ----------

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
		if trace {
			_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		} else {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
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

// ---------- chat 命令（LLM 驱动的对话循环） ----------

func chatCmd(args []string) int {
	var (
		trace      bool
		root       = "."
		baseURL    string
		apiKey     string
		model      string
		feishuMode bool
		mockMode   bool
		memMode    bool
		memDir     string
		streamMode     bool
		searchMode     bool
		multiAgentMode bool
		budget         int64
		ctxWindow      int
	)

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--trace":
			trace = true
		case a == "--mock":
			mockMode = true
		case a == "--stream":
			streamMode = true
		case a == "--multi-agent":
			multiAgentMode = true
		case a == "--search":
			searchMode = true
		case a == "--budget":
			if i+1 < len(args) {
				i++
				if v, err := strconv.ParseInt(args[i], 10, 64); err == nil {
					budget = v
				}
			}
		case strings.HasPrefix(a, "--budget="):
			if v, err := strconv.ParseInt(strings.TrimPrefix(a, "--budget="), 10, 64); err == nil {
				budget = v
			}
		case a == "--ctx-window":
			if i+1 < len(args) {
				i++
				if v, err := strconv.Atoi(args[i]); err == nil {
					ctxWindow = v
				}
			}
		case a == "--memory":
			memMode = true
		case a == "--mem-dir":
			if i+1 < len(args) {
				i++
				memDir = args[i]
			}
		case strings.HasPrefix(a, "--mem-dir="):
			memDir = strings.TrimPrefix(a, "--mem-dir=")
		case a == "--feishu":
			feishuMode = true
		case a == "--root":
			if i+1 < len(args) {
				i++
				root = args[i]
			}
		case strings.HasPrefix(a, "--root="):
			root = strings.TrimPrefix(a, "--root=")
		case a == "--base-url":
			if i+1 < len(args) {
				i++
				baseURL = args[i]
			}
		case strings.HasPrefix(a, "--base-url="):
			baseURL = strings.TrimPrefix(a, "--base-url=")
		case a == "--api-key":
			if i+1 < len(args) {
				i++
				apiKey = args[i]
			}
		case strings.HasPrefix(a, "--api-key="):
			apiKey = strings.TrimPrefix(a, "--api-key=")
		case a == "--model":
			if i+1 < len(args) {
				i++
				model = args[i]
			}
		case strings.HasPrefix(a, "--model="):
			model = strings.TrimPrefix(a, "--model=")
		case a == "-h" || a == "--help":
			printUsage()
			return 0
		}
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// 初始化工具注册表
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())
	reg.Register(tools.NewCalcTool())
	reg.Register(tools.NewReadFileTool(absRoot))
	reg.Register(tools.NewListDirTool(absRoot))
	reg.Register(tools.NewGrepRepoTool(absRoot))
	reg.Register(tools.NewSummarizeTool())
	reg.Register(tools.NewWeatherTool())

	// 注册联网搜索工具
	if searchMode {
		reg.Register(tools.NewWebSearchTool())
		reg.Register(tools.NewWebFetchTool())
		_, _ = fmt.Fprintln(os.Stderr, "🌐 联网搜索已启用")
	}

	// 注册飞书工具
	if feishuMode {
		if os.Getenv("FEISHU_APP_ID") == "" || os.Getenv("FEISHU_APP_SECRET") == "" {
			_, _ = fmt.Fprintln(os.Stderr, "⚠️  --feishu requires env FEISHU_APP_ID and FEISHU_APP_SECRET")
			return 1
		}
		reg.Register(tools.NewFeishuReadDocTool())
		reg.Register(tools.NewFeishuCreateDocTool())
		_, _ = fmt.Fprintln(os.Stderr, "✅ 飞书文档工具已启用")
	}

	// 注册记忆工具
	var memStore *memory.Store
	if memMode {
		if memDir == "" {
			memDir = filepath.Join(absRoot, ".agent-memory")
		}
		var err error
		memStore, err = memory.NewStore(memDir)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "⚠️  记忆初始化失败: %s\n", err)
			return 1
		}
		reg.Register(tools.NewSaveMemoryTool(memStore))
		reg.Register(tools.NewSearchMemoryTool(memStore))
		reg.Register(tools.NewDeleteMemoryTool(memStore))
		_, _ = fmt.Fprintf(os.Stderr, "🧠 记忆功能已启用 (已有 %d 条记忆, 存储: %s)\n", memStore.Count(), memDir)
	}

	// 初始化 LLM 客户端
	var llmClient llm.Client
	if mockMode {
		llmClient = llm.NewMockClient()
		_, _ = fmt.Fprintln(os.Stderr, "🤖 LLM: Mock (用于演示，无需外部服务)")
	} else {
		oai := llm.NewOpenAICompatClient(baseURL, apiKey, model)
		_, _ = fmt.Fprintf(os.Stderr, "🤖 LLM: %s @ %s\n", oai.Model, oai.BaseURL)
		llmClient = oai
	}

	var traceW io.Writer = io.Discard
	if trace {
		traceW = os.Stderr
	}

	systemPrompt := `你是一个智能助手，可以使用各种工具来帮助用户完成任务。
你可以读取文件、列出目录、搜索代码、做计算、查天气。
如果启用了飞书功能，你还可以读取和创建飞书文档。

重要规则：
1. 当用户的请求需要使用工具时，请调用对应的工具
2. 如果不确定需要哪个工具，可以先问用户
3. 返回结果时请用中文，简洁明了
4. 文件路径都是相对于工作根目录的
5. 当用户说"记住"、"帮我记一下"等，使用 save_memory 保存记忆
6. 当用户引用过去的信息、问"你还记得吗"时，使用 search_memory 查找
7. 保存记忆时用第三人称描述（如"用户喜欢..."），topic 要简洁
8. 当用户询问最新信息、新闻、或你不确定的事实时，使用 web_search 搜索
9. 搜索后如需详细内容，用 web_fetch 抓取具体网页
10. 搜索结果要注明来源链接`

	// 创建历史压缩器（用于短期记忆管理）
	var compressor *memory.Compressor
	if memStore != nil {
		compressor = memory.NewCompressor(llmClient, memory.CompressorConfig{
			WindowSize:  3,  // 保留最近 3 轮完整对话
			MaxMessages: 12, // 超过 12 条消息时触发压缩
		})
	}

	loopAgent := agent.NewLoopAgent(llmClient, reg, systemPrompt, traceW, memStore, compressor)

	// 设置上下文窗口管理
	{
		var modelName string
		if oai, ok := llmClient.(*llm.OpenAICompatClient); ok {
			modelName = oai.Model
		}
		profile := ctxwindow.LookupModel(modelName)
		if ctxWindow > 0 {
			profile.MaxContextTokens = ctxWindow
		}
		ctxMgr := ctxwindow.NewManager(ctxwindow.ManagerConfig{
			Model:               profile,
			ProtectRecentRounds: 2,
			ToolResultMaxTokens: 2000,
		})
		loopAgent.SetContextManager(ctxMgr)
		_, _ = fmt.Fprintf(os.Stderr, "ð 上下文窗口: %d tokens (模型: %s, 输入预算: %d)\n",
			profile.MaxContextTokens, profile.Name, ctxMgr.Config().MaxInputTokens)
	}

	// 设置 token 用量追踪
	usageTracker := llm.NewUsageTracker(budget)
	loopAgent.SetUsageTracker(usageTracker)
	if budget > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "📊 Token 预算: %d\n", budget)
	} else {
		_, _ = fmt.Fprintln(os.Stderr, "📊 Token 用量追踪已启用 (无预算限制)")
	}

	// Multi-Agent 模式
	if multiAgentMode {
		agentDefs := []multiagent.AgentDef{
			multiagent.ResearchAgentDef(),
			multiagent.CodeAgentDef(),
			multiagent.WriterAgentDef(),
		}
		orch, err := multiagent.NewOrchestrator(multiagent.OrchestratorConfig{
			LLMClient:      llmClient,
			GlobalRegistry: reg,
			AgentDefs:      agentDefs,
			DirectTools:    []string{"calc", "weather"},
			MemStore:       memStore,
			Compressor:     compressor,
			TraceWriter:    traceW,
		})
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "❌ Multi-Agent 初始化失败: %s\n", err)
			return 1
		}
		orchAgent := orch.GetAgent()
		orchAgent.SetUsageTracker(usageTracker)
		orchAgent.SetContextManager(loopAgent.GetContextManager())

		_, _ = fmt.Fprintf(os.Stderr, "🤝 Multi-Agent 模式已启用 (%d 个子 Agent)\n", len(agentDefs))
		for _, d := range agentDefs {
			_, _ = fmt.Fprintf(os.Stderr, "   • %s: %s\n", d.Name, d.Description)
		}

		return runChatLoop(orch, streamMode, usageTracker)
	}

	return runChatLoop(loopAgent, streamMode, usageTracker)
}


// ---------- ChatAgent 接口 ----------

// ChatAgent 抽象了 LoopAgent 和 Orchestrator 的对话接口
type ChatAgent interface {
	Chat(ctx context.Context, userMessage string, history []llm.Message) (string, []llm.Message, error)
	ChatStream(ctx context.Context, userMessage string, history []llm.Message, onDelta agent.StreamWriter) (string, []llm.Message, error)
}

func runChatLoop(ca ChatAgent, streamMode bool, usageTracker *llm.UsageTracker) int {
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

// ---------- web 命令（图形界面） ----------

func webCmd(args []string) int {
	var (
		trace      bool
		root       = "."
		baseURL    string
		apiKey     string
		model      string
		feishuMode bool
		mockMode   bool
		searchMode bool
		memMode    bool
		memDir     string
		addr       = ":8080"
		budget     int64
	)

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--trace":
			trace = true
		case a == "--mock":
			mockMode = true
		case a == "--search":
			searchMode = true
		case a == "--budget":
			if i+1 < len(args) {
				i++
				if v, err := strconv.ParseInt(args[i], 10, 64); err == nil {
					budget = v
				}
			}
		case strings.HasPrefix(a, "--budget="):
			if v, err := strconv.ParseInt(strings.TrimPrefix(a, "--budget="), 10, 64); err == nil {
				budget = v
			}
		case a == "--memory":
			memMode = true
		case a == "--feishu":
			feishuMode = true
		case a == "--mem-dir":
			if i+1 < len(args) { i++; memDir = args[i] }
		case strings.HasPrefix(a, "--mem-dir="):
			memDir = strings.TrimPrefix(a, "--mem-dir=")
		case a == "--root":
			if i+1 < len(args) { i++; root = args[i] }
		case strings.HasPrefix(a, "--root="):
			root = strings.TrimPrefix(a, "--root=")
		case a == "--base-url":
			if i+1 < len(args) { i++; baseURL = args[i] }
		case strings.HasPrefix(a, "--base-url="):
			baseURL = strings.TrimPrefix(a, "--base-url=")
		case a == "--api-key":
			if i+1 < len(args) { i++; apiKey = args[i] }
		case strings.HasPrefix(a, "--api-key="):
			apiKey = strings.TrimPrefix(a, "--api-key=")
		case a == "--model":
			if i+1 < len(args) { i++; model = args[i] }
		case strings.HasPrefix(a, "--model="):
			model = strings.TrimPrefix(a, "--model=")
		case a == "--addr":
			if i+1 < len(args) { i++; addr = args[i] }
		case strings.HasPrefix(a, "--addr="):
			addr = strings.TrimPrefix(a, "--addr=")
		}
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// 初始化工具注册表
	reg := tools.NewRegistry()
	reg.Register(tools.NewEchoTool())
	reg.Register(tools.NewCalcTool())
	reg.Register(tools.NewReadFileTool(absRoot))
	reg.Register(tools.NewListDirTool(absRoot))
	reg.Register(tools.NewGrepRepoTool(absRoot))
	reg.Register(tools.NewSummarizeTool())
	reg.Register(tools.NewWeatherTool())
	reg.Register(tools.NewExecCommandTool(absRoot))

	if searchMode {
		reg.Register(tools.NewWebSearchTool())
		reg.Register(tools.NewWebFetchTool())
		_, _ = fmt.Fprintln(os.Stderr, "🌐 联网搜索已启用")
	}

	if feishuMode {
		if os.Getenv("FEISHU_APP_ID") == "" || os.Getenv("FEISHU_APP_SECRET") == "" {
			_, _ = fmt.Fprintln(os.Stderr, "⚠️  --feishu requires env FEISHU_APP_ID and FEISHU_APP_SECRET")
			return 1
		}
		reg.Register(tools.NewFeishuReadDocTool())
		reg.Register(tools.NewFeishuCreateDocTool())
		_, _ = fmt.Fprintln(os.Stderr, "✅ 飞书文档工具已启用")
	}

	var memStore *memory.Store
	if memMode {
		if memDir == "" {
			memDir = filepath.Join(absRoot, ".agent-memory")
		}
		memStore, err = memory.NewStore(memDir)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "⚠️  记忆初始化失败: %s\n", err)
			return 1
		}
		reg.Register(tools.NewSaveMemoryTool(memStore))
		reg.Register(tools.NewSearchMemoryTool(memStore))
		reg.Register(tools.NewDeleteMemoryTool(memStore))
		_, _ = fmt.Fprintf(os.Stderr, "🧠 记忆功能已启用 (%d 条)\n", memStore.Count())
	}

	var llmClient llm.Client
	if mockMode {
		llmClient = llm.NewMockClient()
		_, _ = fmt.Fprintln(os.Stderr, "🤖 LLM: Mock")
	} else {
		oai := llm.NewOpenAICompatClient(baseURL, apiKey, model)
		_, _ = fmt.Fprintf(os.Stderr, "🤖 LLM: %s @ %s\n", oai.Model, oai.BaseURL)
		llmClient = oai
	}

	var traceW io.Writer = io.Discard
	if trace {
		traceW = os.Stderr
	}

	systemPrompt := "你是一个智能助手，可以使用各种工具来帮助用户完成任务。\n" +
		"你可以读取文件、列出目录、搜索代码、做计算、查天气。\n" +
		"如果启用了飞书功能，你还可以读取和创建飞书文档。\n\n" +
		"重要规则：\n" +
		"1. 当用户的请求需要使用工具时，请调用对应的工具\n" +
		"2. 如果不确定需要哪个工具，可以先问用户\n" +
		"3. 返回结果时请用中文，简洁明了\n" +
		"4. 文件路径都是相对于工作根目录的\n" +
		"5. 当用户说\"记住\"、\"帮我记一下\"等，使用 save_memory 保存记忆\n" +
		"6. 当用户引用过去的信息时，使用 search_memory 查找\n" +
		"7. 保存记忆时用第三人称描述，topic 要简洁\n" +
		"8. 当用户询问最新信息或你不确定的事实时，使用 web_search 搜索\n" +
		"9. 搜索后如需详细内容，用 web_fetch 抓取具体网页\n" +
		"10. 搜索结果要注明来源链接"

	// Token 用量追踪
	usageTracker := llm.NewUsageTracker(budget)
	if budget > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "📊 Token 预算: %d\n", budget)
	}

	srv := web.NewServer(web.ServerConfig{
		LLMClient:    llmClient,
		Registry:     reg,
		MemStore:     memStore,
		Addr:         addr,
		TraceWriter:  traceW,
		SystemPrompt: systemPrompt,
		UsageTracker: usageTracker,
	})

	if err := srv.Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "❌ Server error: %s\n", err)
		return 1
	}
	return 0
}
