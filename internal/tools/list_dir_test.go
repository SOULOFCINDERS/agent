package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListDirTool(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	tl := NewListDirTool(root)
	out, err := tl.Execute(context.Background(), map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	s := out.(string)
	if !strings.Contains(s, "a.txt") || !strings.Contains(s, "dir/") {
		t.Fatalf("unexpected output: %q", s)
	}
}
