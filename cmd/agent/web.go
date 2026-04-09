package main

import (
	"fmt"
	"io"
	"os"

	"github.com/SOULOFCINDERS/agent/internal/container"
)

// webCmd 实现 web 子命令（通过 Container 装配）
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
