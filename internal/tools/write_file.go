package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// WriteFileTool 写入文件内容（全量覆盖或创建新文件）
type WriteFileTool struct {
	root string
}

func NewWriteFileTool(root string) *WriteFileTool {
	return &WriteFileTool{root: root}
}

func (t *WriteFileTool) Name() string { return "write_file" }

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	_ = ctx

	path, _ := pickString(args, "path", "file")
	content, _ := pickString(args, "content", "text", "data")
	createDirs := true // 默认自动创建父目录
	if v, ok := args["create_dirs"]; ok {
		if b, ok := v.(bool); ok {
			createDirs = b
		}
	}

	if path == "" {
		return nil, fmt.Errorf("missing required parameter: path")
	}
	if content == "" {
		// 允许写空文件（显式 content=""），但不允许没传 content
		if _, hasContent := args["content"]; !hasContent {
			if _, hasText := args["text"]; !hasText {
				return nil, fmt.Errorf("missing required parameter: content")
			}
		}
	}

	// 路径安全检查
	absPath, err := t.resolveSafePath(path)
	if err != nil {
		return nil, err
	}

	// 自动创建父目录
	dir := filepath.Dir(absPath)
	if createDirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", dir, err)
		}
	} else {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return nil, fmt.Errorf("directory does not exist: %s (set create_dirs=true to auto-create)", dir)
		}
	}

	// 检查文件是否已存在（用于返回信息）
	isNew := true
	var oldSize int64
	if info, err := os.Stat(absPath); err == nil {
		isNew = false
		oldSize = info.Size()
	}

	// 写入文件
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	// 构建结果信息
	newSize := int64(len(content))
	charCount := utf8.RuneCountInString(content)
	lineCount := strings.Count(content, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		lineCount++ // 最后一行没有换行也算一行
	}

	relPath, _ := filepath.Rel(t.root, absPath)
	if relPath == "" {
		relPath = absPath
	}

	if isNew {
		return fmt.Sprintf("✅ 已创建文件: %s\n  大小: %d 字节 (%d 字符, %d 行)",
			relPath, newSize, charCount, lineCount), nil
	}
	return fmt.Sprintf("✅ 已覆写文件: %s\n  原大小: %d 字节 → 新大小: %d 字节 (%d 字符, %d 行)",
		relPath, oldSize, newSize, charCount, lineCount), nil
}

// resolveSafePath 解析路径并检查安全性
func (t *WriteFileTool) resolveSafePath(path string) (string, error) {
	// 禁止绝对路径中的危险路径
	if strings.Contains(path, "..") {
		return "", fmt.Errorf("path traversal not allowed: %s", path)
	}

	rootReal, err := filepath.EvalSymlinks(t.root)
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(rootReal)
	if err != nil {
		return "", err
	}

	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(absRoot, absPath)
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return "", err
	}

	// 确保在 root 内
	ok, err := isWithinRoot(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("path is outside of root directory: %s", path)
	}

	// 禁止写入危险路径
	base := filepath.Base(absPath)
	dangerousFiles := map[string]bool{
		".bashrc": true, ".bash_profile": true, ".zshrc": true,
		".profile": true, ".ssh": true, ".gitconfig": true,
	}
	if dangerousFiles[base] {
		return "", fmt.Errorf("writing to %s is not allowed for safety", base)
	}

	return absPath, nil
}
