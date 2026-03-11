package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type GrepRepoTool struct {
	root string
}

func NewGrepRepoTool(root string) *GrepRepoTool {
	return &GrepRepoTool{root: root}
}

func (t *GrepRepoTool) Name() string { return "grep_repo" }

func (t *GrepRepoTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	pat, _ := pickString(args, "pattern")
	if pat == "" {
		in, _ := pickString(args, "input", "text")
		pat = guessPatternFromFreeform(in)
	}
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return nil, fmt.Errorf("missing pattern")
	}

	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, err
	}

	path, _ := pickString(args, "path")
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}

	maxMatches := 20
	if v, ok := args["max_matches"]; ok && v != nil {
		switch x := v.(type) {
		case int:
			maxMatches = x
		case int64:
			maxMatches = int(x)
		case float64:
			maxMatches = int(x)
		case string:
			if n, err := strconv.Atoi(x); err == nil {
				maxMatches = n
			}
		}
	}
	if maxMatches <= 0 {
		maxMatches = 20
	}
	if maxMatches > 200 {
		maxMatches = 200
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

	var matches []string
	walkErr := filepath.WalkDir(realPath, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".trae" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= maxMatches {
			return io.EOF
		}
		if !d.Type().IsRegular() {
			return nil
		}

		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if fi.Size() > 512*1024 {
			return nil
		}

		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()

		rel, _ := filepath.Rel(absRoot, p)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 512*1024)
		ln := 0
		for sc.Scan() {
			ln++
			line := sc.Text()
			if re.FindStringIndex(line) != nil {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), ln, line))
				if len(matches) >= maxMatches {
					return io.EOF
				}
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != io.EOF {
		return nil, walkErr
	}

	if len(matches) == 0 {
		return "no matches", nil
	}
	return strings.Join(matches, "\n"), nil
}

var greedyQuotedRe = regexp.MustCompile(`["“](.+?)["”]`)

func guessPatternFromFreeform(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return ""
	}
	if m := greedyQuotedRe.FindStringSubmatch(in); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	fields := strings.Fields(in)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) == 1 {
		return fields[0]
	}
	last := fields[len(fields)-1]
	if strings.ContainsAny(last, ".*+?[]()|^$\\") {
		return last
	}
	return fields[0]
}
