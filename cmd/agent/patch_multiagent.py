import re

with open("/Users/bytedance/go/src/agent/cmd/agent/main.go", "r") as f:
    content = f.read()

# 1. Add multiagent import
old_imports = '''"github.com/SOULOFCINDERS/agent/internal/ctxwindow"
\t"github.com/SOULOFCINDERS/agent/internal/web"'''
new_imports = '''"github.com/SOULOFCINDERS/agent/internal/ctxwindow"
\t"github.com/SOULOFCINDERS/agent/internal/multiagent"
\t"github.com/SOULOFCINDERS/agent/internal/web"'''
content = content.replace(old_imports, new_imports)

# 2. Add --multi-agent to usage text
old_usage = '''  --budget N        set token budget limit (0 = unlimited, default: 0)`'''
new_usage = '''  --budget N        set token budget limit (0 = unlimited, default: 0)
  --multi-agent     enable multi-agent mode (orchestrator + sub-agents)
  --ctx-window N    override context window size (tokens)`'''
content = content.replace(old_usage, new_usage)

# 3. Add multiAgentMode var to chatCmd
old_chat_vars = '''		streamMode bool
		searchMode bool
		budget     int64
		ctxWindow  int'''
new_chat_vars = '''		streamMode     bool
		searchMode     bool
		multiAgentMode bool
		budget         int64
		ctxWindow      int'''
content = content.replace(old_chat_vars, new_chat_vars, 1)

# 4. Add --multi-agent flag parsing in chatCmd
old_chat_stream = '''		case a == "--stream":
			streamMode = true'''
new_chat_stream = '''		case a == "--stream":
			streamMode = true
		case a == "--multi-agent":
			multiAgentMode = true'''
content = content.replace(old_chat_stream, new_chat_stream, 1)

# 5. Replace the chat loop section - inject multi-agent support after usageTracker setup
# Find the section starting with "// 交互式对话" and replace the whole chat interaction block
old_interactive = '''	// 交互式对话
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
			_, _ = fmt.Fprintf(os.Stdout, "\\n👋 再见! 本次会话 %s\\n", usageTracker.Summary())
			break
		}
		if input == "clear" || input == "reset" {
			history = nil
			_, _ = fmt.Fprintln(os.Stdout, "🔄 对话已重置")
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)

		if streamMode {
			_, _ = fmt.Fprint(os.Stdout, "\\nAgent: ")
			reply, newHistory, err := loopAgent.ChatStream(ctx, input, history, func(delta string) {
				_, _ = fmt.Fprint(os.Stdout, delta)
			})
			cancel()
			if err != nil {
				if strings.Contains(err.Error(), "token budget exceeded") {
					_, _ = fmt.Fprintf(os.Stderr, "\\n⚠️  Token 预算已耗尽 (%s)\\n\\n", usageTracker.Summary())
				} else {
					_, _ = fmt.Fprintf(os.Stderr, "\\n❌ 错误: %s\\n\\n", err)
				}
				continue
			}
			history = newHistory
			_ = reply
			_, _ = fmt.Fprintf(os.Stdout, "\\n  [%s]\\n\\n", usageTracker.Summary())
		} else {
			reply, newHistory, err := loopAgent.Chat(ctx, input, history)
			cancel()
			if err != nil {
				if strings.Contains(err.Error(), "token budget exceeded") {
					_, _ = fmt.Fprintf(os.Stderr, "\\n⚠️  Token 预算已耗尽 (%s)\\n\\n", usageTracker.Summary())
				} else {
					_, _ = fmt.Fprintf(os.Stderr, "\\n❌ 错误: %s\\n\\n", err)
				}
				continue
			}
			history = newHistory
			_, _ = fmt.Fprintf(os.Stdout, "\\nAgent: %s\\n  [%s]\\n\\n", reply, usageTracker.Summary())
		}
	}

	return 0'''

new_interactive = '''	// Multi-Agent 模式
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
			_, _ = fmt.Fprintf(os.Stderr, "❌ Multi-Agent 初始化失败: %s\\n", err)
			return 1
		}
		orchAgent := orch.GetAgent()
		orchAgent.SetUsageTracker(usageTracker)
		orchAgent.SetContextManager(loopAgent.GetContextManager())

		_, _ = fmt.Fprintf(os.Stderr, "🤝 Multi-Agent 模式已启用 (%d 个子 Agent)\\n", len(agentDefs))
		for _, d := range agentDefs {
			_, _ = fmt.Fprintf(os.Stderr, "   • %s: %s\\n", d.Name, d.Description)
		}

		return runChatLoop(orch, streamMode, usageTracker)
	}

	return runChatLoop(loopAgent, streamMode, usageTracker)'''
content = content.replace(old_interactive, new_interactive)

# 6. Add ChatAgent interface and runChatLoop function before webCmd
old_web = '''// ---------- web 命令（图形界面） ----------'''
new_web = '''// ---------- ChatAgent 接口 ----------

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
			_, _ = fmt.Fprintf(os.Stdout, "\\n👋 再见! 本次会话 %s\\n", usageTracker.Summary())
			break
		}
		if input == "clear" || input == "reset" {
			history = nil
			_, _ = fmt.Fprintln(os.Stdout, "🔄 对话已重置")
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)

		if streamMode {
			_, _ = fmt.Fprint(os.Stdout, "\\nAgent: ")
			reply, newHistory, err := ca.ChatStream(ctx, input, history, func(delta string) {
				_, _ = fmt.Fprint(os.Stdout, delta)
			})
			cancel()
			if err != nil {
				if strings.Contains(err.Error(), "token budget exceeded") {
					_, _ = fmt.Fprintf(os.Stderr, "\\n⚠️  Token 预算已耗尽 (%s)\\n\\n", usageTracker.Summary())
				} else {
					_, _ = fmt.Fprintf(os.Stderr, "\\n❌ 错误: %s\\n\\n", err)
				}
				continue
			}
			history = newHistory
			_ = reply
			_, _ = fmt.Fprintf(os.Stdout, "\\n  [%s]\\n\\n", usageTracker.Summary())
		} else {
			reply, newHistory, err := ca.Chat(ctx, input, history)
			cancel()
			if err != nil {
				if strings.Contains(err.Error(), "token budget exceeded") {
					_, _ = fmt.Fprintf(os.Stderr, "\\n⚠️  Token 预算已耗尽 (%s)\\n\\n", usageTracker.Summary())
				} else {
					_, _ = fmt.Fprintf(os.Stderr, "\\n❌ 错误: %s\\n\\n", err)
				}
				continue
			}
			history = newHistory
			_, _ = fmt.Fprintf(os.Stdout, "\\nAgent: %s\\n  [%s]\\n\\n", reply, usageTracker.Summary())
		}
	}

	return 0
}

// ---------- web 命令（图形界面） ----------'''
content = content.replace(old_web, new_web)

with open("/Users/bytedance/go/src/agent/cmd/agent/main.go", "w") as f:
    f.write(content)

print("OK: main.go patched successfully")
