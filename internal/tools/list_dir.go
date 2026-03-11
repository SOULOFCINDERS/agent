package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ListDirTool struct {
	root string
}

func NewListDirTool(root string) *ListDirTool {
	return &ListDirTool{root: root}
}

func (t *ListDirTool) Name() string { return "list_dir" }

func (t *ListDirTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	_ = ctx

	p, _ := pickString(args, "path", "input", "text")
	p = strings.TrimSpace(p)
	if p == "" {
		p = "."
	}

	rootReal, err := filepath.EvalSymlinks(t.root)
	if err != nil {
		return nil, err
	}
	absRoot, err := filepath.Abs(rootReal)
	if err != nil {
		return nil, err
	}

	absPath := p
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

	entries, err := os.ReadDir(realPath)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > 200 {
		names = names[:200]
	}
	return strings.Join(names, "\n"), nil
}
