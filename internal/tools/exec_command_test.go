package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecCommand_BasicExecution(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world\nline2\nline3\n"), 0644)

	tool := NewExecCommandTool(dir)

	tests := []struct {
		name    string
		command string
		wantOK  bool
		contain string
	}{
		{"echo", "echo hello", true, "hello"},
		{"date", "date", true, ""},
		{"pwd", "pwd", true, dir},
		{"ls", "ls", true, "hello.txt"},
		{"cat file", "cat hello.txt", true, "hello world"},
		{"wc -l", "wc -l hello.txt", true, "3"},
		{"head -1", "head -1 hello.txt", true, "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), map[string]any{
				"command": tt.command,
			})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			m := result.(map[string]any)
			status := m["status"].(string)
			if tt.wantOK && status != "success" {
				t.Errorf("status = %s, want success. result: %v", status, m)
			}
			if tt.contain != "" {
				stdout := m["stdout"].(string)
				if !strings.Contains(stdout, tt.contain) {
					t.Errorf("stdout = %q, want to contain %q", stdout, tt.contain)
				}
			}
		})
	}
}

func TestExecCommand_WhitelistReject(t *testing.T) {
	tool := NewExecCommandTool(t.TempDir())
	rejected := []string{
		"rm -rf .",
		"wget http://example.com",
		"curl http://example.com",
		"python3 -c print",
		"chmod 777 file",
		"sudo ls",
		"bash -c ls",
		"sh -c ls",
		"mv a b",
		"cp a b",
	}
	for _, cmd := range rejected {
		t.Run(cmd, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), map[string]any{"command": cmd})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			m := result.(map[string]any)
			if m["status"] != "rejected" {
				t.Errorf("command %q should be rejected, got status=%s", cmd, m["status"])
			}
		})
	}
}

func TestExecCommand_DangerousPatterns(t *testing.T) {
	tool := NewExecCommandTool(t.TempDir())
	dangerous := []struct {
		name string
		cmd  string
	}{
		{"path traversal", "cat ../../passwd"},
		{"command injection", "echo $(whoami)"},
		{"pipe", "ls . | grep foo"},
		{"chain and", "echo a && echo b"},
		{"chain semi", "echo a ; echo b"},
		{"redirect", "echo hello > file.txt"},
	}
	for _, tt := range dangerous {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tool.Execute(context.Background(), map[string]any{"command": tt.cmd})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			m := result.(map[string]any)
			if m["status"] != "rejected" {
				t.Errorf("dangerous command %q should be rejected, got status=%s", tt.cmd, m["status"])
			}
		})
	}
}

func TestExecCommand_TimeoutKill(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wait.pipe"), []byte(""), 0644)
	tool := NewExecCommandTool(dir)
	start := time.Now()
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "tail -f wait.pipe",
		"timeout": 1.0,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	t.Logf("status=%s, elapsed=%v", m["status"], elapsed)
	if elapsed > 5*time.Second {
		t.Errorf("timeout should have killed after ~1s, but elapsed=%v", elapsed)
	}
}

func TestExecCommand_OutputTruncation(t *testing.T) {
	dir := t.TempDir()
	bigContent := strings.Repeat("abcdefghij\n", 2000)
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(bigContent), 0644)
	tool := NewExecCommandTool(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"command": "cat big.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["status"] != "success" {
		t.Errorf("status = %s, want success", m["status"])
	}
	if m["truncated"] != true {
		t.Error("expected truncated=true for large output")
	}
	stdout := m["stdout"].(string)
	if len(stdout) > maxOutputLen+100 {
		t.Errorf("stdout len = %d, expected <= %d", len(stdout), maxOutputLen+100)
	}
}

func TestExecCommand_EmptyCommand(t *testing.T) {
	tool := NewExecCommandTool(t.TempDir())
	_, err := tool.Execute(context.Background(), map[string]any{"command": ""})
	if err == nil {
		t.Error("expected error for empty command")
	}
	_, err = tool.Execute(context.Background(), map[string]any{"command": "   "})
	if err == nil {
		t.Error("expected error for whitespace-only command")
	}
	_, err = tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing command param")
	}
}

func TestExecCommand_NonZeroExit(t *testing.T) {
	tool := NewExecCommandTool(t.TempDir())
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "ls nonexistent_file_xyz_12345",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["status"] != "error" {
		t.Errorf("status = %s, want error", m["status"])
	}
}

func TestExecCommand_TimeoutParamClamping(t *testing.T) {
	tool := NewExecCommandTool(t.TempDir())
	result, _ := tool.Execute(context.Background(), map[string]any{"command": "echo ok", "timeout": -5.0})
	if result.(map[string]any)["status"] != "success" {
		t.Error("negative timeout should default to 10s")
	}
	result2, _ := tool.Execute(context.Background(), map[string]any{"command": "echo ok", "timeout": 999.0})
	if result2.(map[string]any)["status"] != "success" {
		t.Error("large timeout should be capped to 30s")
	}
}

func TestExecCommand_AbsPathBypass(t *testing.T) {
	tool := NewExecCommandTool(t.TempDir())
	absRm := filepath.Join(string(filepath.Separator), "usr", "bin", "rm")
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": absRm + " test.txt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["status"] != "rejected" {
		t.Errorf("absolute path rm should be rejected, got status=%s", m["status"])
	}
}
