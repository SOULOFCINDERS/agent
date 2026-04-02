package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var allowedCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true,
	"wc": true, "find": true, "grep": true, "date": true,
	"echo": true, "pwd": true, "sort": true, "uniq": true,
	"cut": true, "tr": true, "file": true, "du": true,
	"df": true, "env": true, "which": true, "uname": true,
}

var dangerousPatterns = []string{
	"../", "/etc/", "/proc/", "/sys/", "/dev/",
	"$(", "`",
	"&&", "||", ";", "|",
	">", ">>", "<",
}

const (
	defaultExecTimeout = 10 * time.Second
	maxExecTimeout     = 30 * time.Second
	maxOutputLen       = 10000
)

// ExecCommandTool 安全命令执行工具
type ExecCommandTool struct {
	rootDir string
}

func NewExecCommandTool(rootDir string) *ExecCommandTool {
	return &ExecCommandTool{rootDir: rootDir}
}

func (t *ExecCommandTool) Name() string { return "exec_command" }

func (t *ExecCommandTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	command, _ := args["command"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("parameter 'command' is required and cannot be empty")
	}

	timeoutSec := 10.0
	if v, ok := args["timeout"].(float64); ok {
		timeoutSec = v
	}
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	if timeoutSec > 30 {
		timeoutSec = 30
	}
	timeout := time.Duration(timeoutSec * float64(time.Second))

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("parameter 'command' is empty after parsing")
	}

	cmdName := filepath.Base(parts[0])
	if !allowedCommands[cmdName] {
		return map[string]any{
			"status": "rejected",
			"error":  fmt.Sprintf("command '%s' is not in the allowed whitelist. Allowed: %s", cmdName, allowedListStr()),
		}, nil
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(command, pattern) {
			return map[string]any{
				"status": "rejected",
				"error":  fmt.Sprintf("command contains dangerous pattern '%s'", pattern),
			}, nil
		}
	}

	absRoot, err := filepath.Abs(t.rootDir)
	if err != nil {
		return nil, fmt.Errorf("invalid root directory: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, parts[0], parts[1:]...)
	cmd.Dir = absRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(startTime)

	result := map[string]any{
		"command":    command,
		"elapsed_ms": elapsed.Milliseconds(),
	}

	if execCtx.Err() == context.DeadlineExceeded {
		result["status"] = "timeout"
		result["error"] = fmt.Sprintf("command timed out after %.0f seconds", timeoutSec)
		return result, nil
	}

	outStr := stdout.String()
	errStr := stderr.String()

	truncated := false
	combined := outStr + errStr
	if len(combined) > maxOutputLen {
		truncated = true
		if len(outStr) > maxOutputLen {
			outStr = outStr[:maxOutputLen] + "\n... [output truncated]"
			errStr = ""
		} else {
			remaining := maxOutputLen - len(outStr)
			if len(errStr) > remaining {
				errStr = errStr[:remaining] + "\n... [stderr truncated]"
			}
		}
	}

	result["stdout"] = outStr
	result["truncated"] = truncated
	if errStr != "" {
		result["stderr"] = errStr
	}

	if runErr != nil {
		result["status"] = "error"
		result["exit_error"] = runErr.Error()
	} else {
		result["status"] = "success"
	}

	return result, nil
}

func allowedListStr() string {
	cmds := make([]string, 0, len(allowedCommands))
	for k := range allowedCommands {
		cmds = append(cmds, k)
	}
	return strings.Join(cmds, ", ")
}
