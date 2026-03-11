package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepRepoTool(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("other\nHELLO\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tl := NewGrepRepoTool(root)
	out, err := tl.Execute(context.Background(), map[string]any{"pattern": "hello"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	s := out.(string)
	if !strings.Contains(s, "a.txt:1:hello") {
		t.Fatalf("unexpected output: %q", s)
	}
}
