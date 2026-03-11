package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type ReadFileTool struct {
	root string
}

func NewReadFileTool(root string) *ReadFileTool {
	return &ReadFileTool{root: root}
}

func (t *ReadFileTool) Name() string { return "read_file" }

var (
	readFirstNRe = regexp.MustCompile(`(?i)\bfirst\s+(\d+)\s+lines?\b`)
	pathTokenRe  = regexp.MustCompile(`(?i)\b([./]?[a-z0-9_\-./]+\.[a-z0-9]{1,8})\b`)
)

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	_ = ctx

	path, _ := pickString(args, "path")
	linesAny, hasLines := args["lines"]
	summarize := pickBool(args, "summarize")

	if path == "" {
		in, _ := pickString(args, "input", "text")
		in = strings.TrimSpace(in)
		if in != "" {
			if m := pathTokenRe.FindStringSubmatch(in); len(m) == 2 {
				path = m[1]
			}
			if !hasLines {
				if m := readFirstNRe.FindStringSubmatch(in); len(m) == 2 {
					if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
						linesAny = n
						hasLines = true
					}
				}
			}
			if !summarize {
				lo := strings.ToLower(in)
				summarize = strings.Contains(in, "总结") || strings.Contains(lo, "summarize") || strings.Contains(lo, "summary")
			}
		}
	}

	if path == "" {
		return nil, fmt.Errorf("missing path")
	}

	lines := 0
	if hasLines {
		switch v := linesAny.(type) {
		case int:
			lines = v
		case int64:
			lines = int(v)
		case float64:
			lines = int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				lines = n
			}
		default:
		}
	}
	if lines <= 0 {
		lines = 20
	}

	rootReal, err := filepath.EvalSymlinks(t.root)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(rootReal)
	if err != nil {
		return nil, err
	}

	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(absRoot, absPath)
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return nil, err
	}

	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return nil, err
	}

	ok, err := isWithinRoot(absRoot, realPath)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("path is outside of root")
	}

	f, err := os.Open(realPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	const maxBytes = 256 * 1024
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, maxBytes)

	var outLines []string
	readBytes := 0
	for sc.Scan() {
		line := sc.Text()
		readBytes += len(line) + 1
		outLines = append(outLines, line)
		if len(outLines) >= lines || readBytes >= maxBytes {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	content := strings.Join(outLines, "\n")
	if !summarize {
		return content, nil
	}

	summary := buildSummary(outLines, readBytes)
	if content == "" {
		return summary, nil
	}
	return summary + "\n\n" + content, nil
}

func pickBool(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		xx := strings.ToLower(strings.TrimSpace(x))
		return xx == "1" || xx == "true" || xx == "yes" || xx == "y"
	default:
		return false
	}
}

func isWithinRoot(root string, path string) (bool, error) {
	rr, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	pp, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(rr, pp)
	if err != nil {
		return false, err
	}
	if rel == "." {
		return true, nil
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false, nil
	}
	return true, nil
}

func buildSummary(lines []string, bytes int) string {
	var first string
	var headings []string
	for _, l := range lines {
		trim := strings.TrimSpace(l)
		if first == "" && trim != "" {
			first = trim
		}
		if strings.HasPrefix(trim, "#") {
			headings = append(headings, trim)
			if len(headings) >= 5 {
				break
			}
		}
	}
	if len(first) > 120 {
		first = first[:120]
	}
	var b strings.Builder
	b.WriteString("Summary:\n")
	b.WriteString("- lines: ")
	b.WriteString(strconv.Itoa(len(lines)))
	b.WriteString("\n- bytes: ")
	b.WriteString(strconv.Itoa(bytes))
	if first != "" {
		b.WriteString("\n- first: ")
		b.WriteString(first)
	}
	if len(headings) > 0 {
		b.WriteString("\n- headings: ")
		b.WriteString(strings.Join(headings, " | "))
	}
	return b.String()
}
