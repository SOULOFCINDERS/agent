package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_CreateNew(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteFileTool(root)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "hello.txt",
		"content": "Hello, World!\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	res := result.(string)
	if !strings.Contains(res, "已创建文件") {
		t.Errorf("expected created, got: %s", res)
	}

	data, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "Hello, World!\n" {
		t.Errorf("file content mismatch: %q", string(data))
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "existing.txt"), []byte("old content"), 0644)

	tool := NewWriteFileTool(root)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "existing.txt",
		"content": "new content",
	})
	if err != nil {
		t.Fatal(err)
	}

	res := result.(string)
	if !strings.Contains(res, "已覆写文件") {
		t.Errorf("expected overwrite, got: %s", res)
	}

	data, _ := os.ReadFile(filepath.Join(root, "existing.txt"))
	if string(data) != "new content" {
		t.Errorf("file content mismatch: %q", string(data))
	}
}

func TestWriteFile_CreateDirs(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteFileTool(root)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "a/b/c/deep.txt",
		"content": "deep file",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, "a/b/c/deep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep file" {
		t.Errorf("got: %q", string(data))
	}
}

func TestWriteFile_PathTraversal(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteFileTool(root)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "..%s..%stmp%stest" + ".txt",
		"content": "hacked",
	})
	// The ".." in path should be rejected
	// Use actual path traversal
	_, err = tool.Execute(context.Background(), map[string]any{
		"path":    strings.Join([]string{"..", "..", "tmp", "test.txt"}, string(filepath.Separator)),
		"content": "hacked",
	})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestWriteFile_DangerousFile(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteFileTool(root)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    ".bashrc",
		"content": "bad content",
	})
	if err == nil {
		t.Fatal("expected error for dangerous file")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("expected not allowed error, got: %s", err)
	}
}

func TestWriteFile_MissingParams(t *testing.T) {
	root := t.TempDir()
	tool := NewWriteFileTool(root)

	_, err := tool.Execute(context.Background(), map[string]any{
		"content": "hello",
	})
	if err == nil {
		t.Fatal("expected error for missing path")
	}

	_, err = tool.Execute(context.Background(), map[string]any{
		"path": "test.txt",
	})
	if err == nil {
		t.Fatal("expected error for missing content")
	}
}
