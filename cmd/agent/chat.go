package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/container"
	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/rag"
	sessionPkg "github.com/SOULOFCINDERS/agent/internal/session"
)

// chatCmd 实现 chat 子命令（通过 Container 装配）
func chatCmd(args []string) int {
	// Extract --resume flag before common parsing
	var resumeID string
	for i := 0; i < len(args); i++ {
		if args[i] == "--resume" && i+1 < len(args) {
			resumeID = args[i+1]
			// Remove from args so parseCommonFlags ignores it
			args = append(args[:i], args[i+2:]...)
			break
		}
		if strings.HasPrefix(args[i], "--resume=") {
			resumeID = strings.TrimPrefix(args[i], "--resume=")
			args = append(args[:i], args[i+1:]...)
			break
		}
	}

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

	// 启动时加载知识库
	if ragLoadDir != "" && app.RAGEngine != nil {
		_, _ = fmt.Fprintf(os.Stderr, "📦 正在加载知识库: %s\n", ragLoadDir)
		opts := rag.DefaultImportOptions()
		result, loadErr := app.RAGEngine.IndexDirectory(context.Background(), ragLoadDir, opts)
		if loadErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "⚠️  知识库加载错误: %s\n", loadErr)
		} else {
			_, _ = fmt.Fprintf(os.Stderr, "✅ 知识库加载完成: %d 文件索引, %d 跳过, %d 失败\n",
				result.Indexed, result.Skipped, result.Failed)
		}
	}

	// 打印启动信息
	printStartupInfo(app)

	// 会话管理
	var sess *sessionPkg.Session
	var history []llm.Message
	if resumeID != "" && app.SessionManager != nil {
		var err error
		sess, history, err = app.SessionManager.LoadHistory(context.Background(), resumeID)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "⚠️  无法恢复会话 %s: %v\n", resumeID, err)
		} else {
			_, _ = fmt.Fprintf(os.Stderr, "📂 已恢复会话: %s (%s, %d 轮对话)\n", sess.ID, sess.Title, sess.Metadata.TurnCount)
		}
	}
	if sess == nil && app.SessionManager != nil {
		sess = app.SessionManager.NewSession()
	}

	return runChatLoopWithSession(app.ChatAgent(), app.LoopAgent, cfg.StreamMode, app.UsageTracker, app.SessionManager, sess, history)
}

// runChatLoopWithSession 带会话持久化的对话循环
func runChatLoopWithSession(ca container.ChatAgent, loopAgent *agent.LoopAgent, streamMode bool, usageTracker *llm.UsageTracker, sessMgr *sessionPkg.Manager, sess *sessionPkg.Session, history []llm.Message) int {
	_, _ = fmt.Fprintln(os.Stdout, "")
	_, _ = fmt.Fprintln(os.Stdout, "╔══════════════════════════════════════════════╗")
	_, _ = fmt.Fprintln(os.Stdout, "║  🤖 Agent Chat (quit 退出 | compact 压缩上下文) ║")
	_, _ = fmt.Fprintln(os.Stdout, "╚══════════════════════════════════════════════╝")
	if sess != nil {
		_, _ = fmt.Fprintf(os.Stdout, "  📝 会话: %s\n", sess.ID)
	}
	_, _ = fmt.Fprintln(os.Stdout, "")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	saveSession := func() {
		if sessMgr != nil && sess != nil {
			ctx := context.Background()
			if err := sessMgr.SaveHistory(ctx, sess, history); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "⚠️  会话保存失败: %v\n", err)
			}
		}
	}

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
			saveSession()
			if sess != nil {
				_, _ = fmt.Fprintf(os.Stdout, "\n💾 会话已保存: %s\n", sess.ID)
			}
			_, _ = fmt.Fprintf(os.Stdout, "👋 再见! 本次会话 %s\n", usageTracker.Summary())
			break
		}
		if input == "clear" || input == "reset" {
			history = nil
			if sess != nil {
				sess = sessMgr.NewSession()
			}
			_, _ = fmt.Fprintln(os.Stdout, "🔄 对话已重置")
			continue
		}
		if input == "compact" {
			if len(history) == 0 {
				_, _ = fmt.Fprintln(os.Stdout, "ℹ️  没有可压缩的对话历史")
				continue
			}
			compacted, result, err := loopAgent.CompactHistory(context.Background(), history)
			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "❌ 压缩失败: %s\n", err)
				continue
			}
			history = compacted
			_, _ = fmt.Fprintf(os.Stdout, "📦 上下文已压缩: %d→%d 条消息, %d→%d tokens, 策略: %s\n",
				result.OriginalCount, result.FinalCount, result.TokensBefore, result.TokensAfter, result.Strategy)
			if result.SummaryInserted {
				_, _ = fmt.Fprintln(os.Stdout, "   ✅ 已生成对话摘要替换旧消息")
			}
			saveSession()
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)

		if streamMode {
			_, _ = fmt.Fprint(os.Stdout, "\nAgent: ")
			reply, newHistory, err := ca.ChatStreamV2(ctx, input, history, func(event agent.StreamEvent) {
				switch event.Type {
				case agent.EventDelta:
					_, _ = fmt.Fprint(os.Stdout, event.Content)
				case agent.EventToolStart:
					_, _ = fmt.Fprintf(os.Stderr, "\n  🔧 %s(%s)\n", event.ToolName, truncateArgs(event.ToolArgs, 60))
				case agent.EventToolEnd:
					if event.ToolError != "" {
						_, _ = fmt.Fprintf(os.Stderr, "  ❌ %s (%dms)\n", event.ToolError, event.Duration)
					} else {
						_, _ = fmt.Fprintf(os.Stderr, "  ✅ done (%dms)\n", event.Duration)
					}
				case agent.EventIteration:
					if event.Iteration > 1 {
						_, _ = fmt.Fprintf(os.Stderr, "  ⟳ 轮次 %d/%d\n", event.Iteration, event.MaxIter)
					}
				case agent.EventStatus:
					_, _ = fmt.Fprintf(os.Stderr, "  ⏳ %s\n", event.Status)
				}
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

		// Auto-save after each turn
		saveSession()
	}

	return 0
}

// truncateArgs 截断工具参数用于 CLI 显示
func truncateArgs(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
