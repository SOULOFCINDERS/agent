package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sessionPkg "github.com/SOULOFCINDERS/agent/internal/session"
)

// sessionsCmd 实现 sessions 子命令
func sessionsCmd(args []string) int {
	root := "."
	sessionDir := ""
	deleteID := ""

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--root":
			if i+1 < len(args) {
				i++
				root = args[i]
			}
		case strings.HasPrefix(args[i], "--root="):
			root = strings.TrimPrefix(args[i], "--root=")
		case args[i] == "--session-dir":
			if i+1 < len(args) {
				i++
				sessionDir = args[i]
			}
		case strings.HasPrefix(args[i], "--session-dir="):
			sessionDir = strings.TrimPrefix(args[i], "--session-dir=")
		case args[i] == "--delete":
			if i+1 < len(args) {
				i++
				deleteID = args[i]
			}
		case strings.HasPrefix(args[i], "--delete="):
			deleteID = strings.TrimPrefix(args[i], "--delete=")
		}
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "resolve root: %v\n", err)
		return 1
	}
	if sessionDir == "" {
		sessionDir = filepath.Join(absRoot, ".agent-sessions")
	}

	store, err := sessionPkg.NewJSONStore(sessionDir)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "session store: %v\n", err)
		return 1
	}
	mgr := sessionPkg.NewManager(store)
	ctx := context.Background()

	// Delete mode
	if deleteID != "" {
		if err := mgr.Delete(ctx, deleteID); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "❌ 删除失败: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(os.Stdout, "🗑️  已删除会话: %s\n", deleteID)
		return 0
	}

	// List mode
	summaries, err := mgr.List(ctx, 20)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "list sessions: %v\n", err)
		return 1
	}

	if len(summaries) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "📭 暂无保存的会话")
		return 0
	}

	_, _ = fmt.Fprintf(os.Stdout, "\n📋 保存的会话 (%d 个):\n\n", len(summaries))
	_, _ = fmt.Fprintf(os.Stdout, "%-24s  %-6s  %-20s  %s\n", "ID", "轮次", "更新时间", "标题")
	_, _ = fmt.Fprintln(os.Stdout, strings.Repeat("─", 80))
	for _, s := range summaries {
		_, _ = fmt.Fprintf(os.Stdout, "%-24s  %-6d  %-20s  %s\n",
			s.ID, s.TurnCount,
			s.UpdatedAt.Format("2006-01-02 15:04:05"),
			s.Title)
	}
	_, _ = fmt.Fprintf(os.Stdout, "\n💡 恢复会话: agent chat --resume <ID>\n")
	_, _ = fmt.Fprintf(os.Stdout, "🗑️  删除会话: agent sessions --delete <ID>\n\n")

	return 0
}
