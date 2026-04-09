package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/agent"
	"github.com/SOULOFCINDERS/agent/internal/executor"
	"github.com/SOULOFCINDERS/agent/internal/planner"
	"github.com/SOULOFCINDERS/agent/internal/tools"
)

// runCmd 实现 run 子命令（保留原有功能，不经过 Container）
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
