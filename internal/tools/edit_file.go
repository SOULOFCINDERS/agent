package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// EditFileTool 精确编辑文件内容
// 支持两种模式：
//   1. 搜索替换: old_text → new_text
//   2. 行号编辑: 插入/替换/删除指定行
type EditFileTool struct {
	root string
}

func NewEditFileTool(root string) *EditFileTool {
	return &EditFileTool{root: root}
}

func (t *EditFileTool) Name() string { return "edit_file" }

func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	_ = ctx

	path, _ := pickString(args, "path", "file")
	if path == "" {
		return nil, fmt.Errorf("missing required parameter: path")
	}

	// 路径安全检查（复用 WriteFileTool 的安全逻辑）
	wt := &WriteFileTool{root: t.root}
	absPath, err := wt.resolveSafePath(path)
	if err != nil {
		return nil, err
	}

	// 读取现有文件
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	original := string(data)

	// 判断编辑模式
	oldText, hasOld := args["old_text"]
	newText, hasNew := args["new_text"]
	_, hasLine := args["line"]
	_, hasInsert := args["insert"]
	_, hasDelete := args["delete"]
	_, hasAppend := args["append"]

	var result string

	switch {
	case hasOld && hasNew:
		// 模式 1: 搜索替换
		result, err = t.searchReplace(original, fmt.Sprint(oldText), fmt.Sprint(newText))

	case hasOld && !hasNew:
		// 删除匹配的文本
		result, err = t.searchReplace(original, fmt.Sprint(oldText), "")

	case hasLine:
		// 模式 2: 行号编辑
		lineNum := toInt(args["line"])
		if hasInsert {
			result, err = t.insertAtLine(original, lineNum, fmt.Sprint(args["insert"]))
		} else if hasDelete {
			count := toInt(args["delete"])
			if count <= 0 {
				count = 1
			}
			result, err = t.deleteLines(original, lineNum, count)
		} else if hasNew {
			result, err = t.replaceLine(original, lineNum, fmt.Sprint(newText))
		} else {
			return nil, fmt.Errorf("line mode requires one of: insert, delete, or new_text")
		}

	case hasAppend:
		// 追加到文件末尾
		appendText := fmt.Sprint(args["append"])
		if !strings.HasSuffix(original, "\n") && original != "" {
			result = original + "\n" + appendText
		} else {
			result = original + appendText
		}

	default:
		return nil, fmt.Errorf("请指定编辑操作：\n" +
			"  搜索替换: old_text + new_text\n" +
			"  行号插入: line + insert\n" +
			"  行号删除: line + delete\n" +
			"  行号替换: line + new_text\n" +
			"  末尾追加: append")
	}

	if err != nil {
		return nil, err
	}

	// 写回文件
	if err := os.WriteFile(absPath, []byte(result), 0644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	relPath, _ := filepath.Rel(t.root, absPath)
	if relPath == "" {
		relPath = absPath
	}

	// 构建 diff 摘要
	oldLines := strings.Count(original, "\n")
	newLines := strings.Count(result, "\n")
	oldChars := utf8.RuneCountInString(original)
	newChars := utf8.RuneCountInString(result)
	diffLines := newLines - oldLines
	diffChars := newChars - oldChars

	diffSign := func(n int) string {
		if n > 0 {
			return fmt.Sprintf("+%d", n)
		}
		return fmt.Sprintf("%d", n)
	}

	return fmt.Sprintf("✅ 已编辑文件: %s\n  行数: %d → %d (%s)\n  字符: %d → %d (%s)",
		relPath, oldLines, newLines, diffSign(diffLines),
		oldChars, newChars, diffSign(diffChars)), nil
}

// searchReplace 搜索替换（精确匹配）
func (t *EditFileTool) searchReplace(content, oldText, newText string) (string, error) {
	if oldText == "" {
		return "", fmt.Errorf("old_text cannot be empty")
	}

	count := strings.Count(content, oldText)
	if count == 0 {
		// 尝试忽略前后空白匹配
		trimmedOld := strings.TrimSpace(oldText)
		if trimmedOld != oldText && strings.Contains(content, trimmedOld) {
			count = strings.Count(content, trimmedOld)
			oldText = trimmedOld
			newText = strings.TrimSpace(newText)
		} else {
			// 提供上下文帮助定位
			return "", fmt.Errorf("old_text not found in file. Please verify the exact text to replace.\n"+
				"Tip: make sure whitespace and line breaks match exactly.\n"+
				"File preview (first 500 chars):\n%s", truncateStr(content, 500))
		}
	}

	if count > 1 {
		// 多处匹配时只替换第一处，避免意外批量修改
		result := strings.Replace(content, oldText, newText, 1)
		return result, nil
	}

	return strings.Replace(content, oldText, newText, 1), nil
}

// insertAtLine 在指定行号前插入内容
func (t *EditFileTool) insertAtLine(content string, line int, text string) (string, error) {
	lines := strings.Split(content, "\n")

	if line < 1 {
		line = 1
	}
	if line > len(lines)+1 {
		line = len(lines) + 1
	}

	// 插入到 line 之前（line=1 表示插入到文件开头）
	insertLines := strings.Split(text, "\n")
	idx := line - 1 // 0-based

	newLines := make([]string, 0, len(lines)+len(insertLines))
	newLines = append(newLines, lines[:idx]...)
	newLines = append(newLines, insertLines...)
	newLines = append(newLines, lines[idx:]...)

	return strings.Join(newLines, "\n"), nil
}

// replaceLine 替换指定行
func (t *EditFileTool) replaceLine(content string, line int, newText string) (string, error) {
	lines := strings.Split(content, "\n")

	if line < 1 || line > len(lines) {
		return "", fmt.Errorf("line %d out of range (file has %d lines)", line, len(lines))
	}

	lines[line-1] = newText
	return strings.Join(lines, "\n"), nil
}

// deleteLines 删除从指定行开始的 count 行
func (t *EditFileTool) deleteLines(content string, startLine, count int) (string, error) {
	lines := strings.Split(content, "\n")

	if startLine < 1 || startLine > len(lines) {
		return "", fmt.Errorf("line %d out of range (file has %d lines)", startLine, len(lines))
	}

	endLine := startLine + count - 1
	if endLine > len(lines) {
		endLine = len(lines)
	}

	newLines := make([]string, 0, len(lines)-count)
	newLines = append(newLines, lines[:startLine-1]...)
	newLines = append(newLines, lines[endLine:]...)

	return strings.Join(newLines, "\n"), nil
}

// toInt 安全地将 any 转换为 int
func toInt(v any) int {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}

// truncateStr 截断字符串
func truncateStr(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "..."
}
