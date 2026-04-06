// Package cli 是 Presenter 层的 CLI 实现。
// 当前阶段定义 CLI 入口的配置和组装逻辑。
// 后续将从 cmd/agent/main.go 中提取交互式 REPL 逻辑到此包。
package cli

import (
	"io"

	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
	"github.com/SOULOFCINDERS/agent/internal/memory"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// Config CLI 运行配置
type Config struct {
	Client       conv.Client
	Registry     *tools.Registry
	MemStore     *memory.Store
	Compressor   *memory.Compressor
	SystemPrompt string
	TraceWriter  io.Writer
	Streaming    bool
}
