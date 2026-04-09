package main

import (
	"fmt"
	"os"

	"github.com/SOULOFCINDERS/agent/internal/envfile"
)

func main() {
	os.Exit(realMain())
}

func realMain() int {
	_ = envfile.Load(".env")

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
	case "sessions":
		return sessionsCmd(os.Args[2:])
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		return 2
	}
}

func printUsage() {
	_, _ = fmt.Fprintln(os.Stderr, `usage: agent <command> [args]

commands:
  run       run agent on a single task (rule-based planner)
  chat      start interactive chat with LLM-powered agent
  web       start web UI with HTTP server
  sessions  list or manage saved conversation sessions

chat options:
  --trace           print trace events to stderr
  --root DIR        root directory for file tools (default: .)
  --base-url URL    LLM API base URL (or env LLM_BASE_URL)
  --api-key KEY     LLM API key (or env LLM_API_KEY)
  --model NAME      LLM model name (or env LLM_MODEL)
  --stream          enable streaming output (token-by-token)
  --verify          enable Verification Agent (anti-hallucination)
  --search          enable web search tools (web_search + web_fetch)
  --feishu          enable Feishu doc tools (requires env FEISHU_APP_ID, FEISHU_APP_SECRET)
  --budget N        set token budget limit (0 = unlimited, default: 0)
  --multi-agent     enable multi-agent mode (orchestrator + sub-agents)
  --ctx-window N    override context window size (tokens)
  --resume ID       resume a previous conversation session
  --session-dir DIR session storage directory (default: <root>/.agent-sessions)
  --rag             enable RAG (Retrieval-Augmented Generation)
  --rag-dir DIR     RAG index storage directory (default: <root>/.agent-rag)
  --rag-load DIR    load knowledge base from directory at startup (auto-enables --rag)
  --rag-backend STR RAG backend: "chromem" or "legacy" (default: legacy)`)
}
