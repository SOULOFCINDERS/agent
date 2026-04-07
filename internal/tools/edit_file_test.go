package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFile_SearchReplace(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.go"), []byte("package main\n\nfunc hello() {\n\treturn\n}\n"), 0644)

	tool := NewEditFileTool(root)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":     "test.go",
		"old_text": "func hello()",
		"new_text": "func greeting()",
	})
	if err != nil {
		t.Fatal(err)
	}

	res := result.(string)
	if !strings.Contains(res, "已编辑文件") {
		t.Errorf("expected edited, got: %s", res)
	}

	data, _ := os.ReadFile(filepath.Join(root, "test.go"))
	if !strings.Contains(string(data), "func greeting()") {
		t.Errorf("replacement not found: %s", string(data))
	}
	if strings.Contains(string(data), "func hello()") {
		t.Errorf("old text still present: %s", string(data))
	}
}

func TestEditFile_SearchReplace_NotFound(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("hello world"), 0644)

	tool := NewEditFileTool(root)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":     "test.txt",
		"old_text": "nonexistent text",
		"new_text": "replacement",
	})
	if err == nil {
		t.Fatal("expected error when old_text not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %s", err)
	}
}

func TestEditFile_InsertAtLine(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("line1\nline2\nline3"), 0644)

	tool := NewEditFileTool(root)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":   "test.txt",
		"line":   2,
		"insert": "inserted",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "test.txt"))
	lines := strings.Split(string(data), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(lines), lines)
	}
	if lines[1] != "inserted" {
		t.Errorf("line 2 should be 'inserted', got: %q", lines[1])
	}
}

func TestEditFile_DeleteLines(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("a\nb\nc\nd\ne"), 0644)

	tool := NewEditFileTool(root)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":   "test.txt",
		"line":   2,
		"delete": 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "test.txt"))
	lines := strings.Split(string(data), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines after deleting 2, got %d: %v", len(lines), lines)
	}
	if lines[0] != "a" || lines[1] != "d" || lines[2] != "e" {
		t.Errorf("unexpected content after delete: %v", lines)
	}
}

func TestEditFile_ReplaceLine(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("alpha\nbeta\ngamma"), 0644)

	tool := NewEditFileTool(root)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":     "test.txt",
		"line":     2,
		"new_text": "BETA",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "test.txt"))
	if !strings.Contains(string(data), "BETA") {
		t.Errorf("line replacement not found: %s", string(data))
	}
}

func TestEditFile_Append(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("line1\nline2"), 0644)

	tool := NewEditFileTool(root)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":   "test.txt",
		"append": "line3\nline4",
	})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "test.txt"))
	if !strings.Contains(string(data), "line3") {
		t.Errorf("appended content not found: %s", string(data))
	}
}

func TestEditFile_NoOperation(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test.txt"), []byte("content"), 0644)

	tool := NewEditFileTool(root)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "test.txt",
	})
	if err == nil {
		t.Fatal("expected error when no operation specified")
	}
}

func TestEditFile_PathSafety(t *testing.T) {
	root := t.TempDir()
	tool := NewEditFileTool(root)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":     ".bashrc",
		"old_text": "a",
		"new_text": "b",
	})
	if err == nil {
		t.Fatal("expected error for dangerous file")
	}
}
