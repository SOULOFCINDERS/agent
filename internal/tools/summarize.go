package tools

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type SummarizeTool struct{}

func NewSummarizeTool() *SummarizeTool { return &SummarizeTool{} }

func (t *SummarizeTool) Name() string { return "summarize" }

func (t *SummarizeTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	_ = ctx
	in, _ := pickString(args, "input", "text")
	in = strings.TrimSpace(in)
	if in == "" {
		return "Summary:\n- lines: 0\n- bytes: 0", nil
	}

	lines := splitLines(in, 2000)
	bytes := len(in)

	first := ""
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

	keywords := topKeywords(in, 8)

	var b strings.Builder
	b.WriteString("Summary:\n- lines: ")
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
	if len(keywords) > 0 {
		b.WriteString("\n- keywords: ")
		b.WriteString(strings.Join(keywords, ", "))
	}
	return b.String(), nil
}

func splitLines(s string, max int) []string {
	raw := strings.Split(s, "\n")
	if len(raw) > max {
		return raw[:max]
	}
	return raw
}

var wordRe = regexp.MustCompile(`[A-Za-z0-9_]{3,}`)

func topKeywords(s string, n int) []string {
	words := wordRe.FindAllString(strings.ToLower(s), -1)
	if len(words) == 0 {
		return nil
	}
	stop := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "this": {}, "that": {}, "from": {}, "into": {}, "then": {},
		"read": {}, "file": {}, "lines": {}, "line": {}, "summary": {}, "summarize": {},
		"package": {}, "import": {}, "func": {}, "return": {}, "type": {}, "var": {}, "const": {},
	}
	m := map[string]int{}
	for _, w := range words {
		if _, ok := stop[w]; ok {
			continue
		}
		m[w]++
	}
	type kv struct {
		k string
		v int
	}
	var arr []kv
	for k, v := range m {
		arr = append(arr, kv{k: k, v: v})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].v == arr[j].v {
			return arr[i].k < arr[j].k
		}
		return arr[i].v > arr[j].v
	})
	if len(arr) > n {
		arr = arr[:n]
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		out = append(out, x.k)
	}
	return out
}
