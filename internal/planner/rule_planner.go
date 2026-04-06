// Package planner implements rule-based planning that parses user input into structured execution plans.
package planner

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/agent"
)

type RulePlanner struct {
	readFirstNRe *regexp.Regexp
}

func NewRulePlanner() *RulePlanner {
	return &RulePlanner{
		readFirstNRe: regexp.MustCompile(`(?i)\bfirst\s+(\d+)\s+lines?\b`),
	}
}

func (p *RulePlanner) Plan(ctx context.Context, input string) (agent.Plan, error) {
	_ = ctx

	in := strings.TrimSpace(input)
	if in == "" {
		return agent.Plan{}, nil
	}

	parts := splitIntoParts(in)
	var steps []agent.Step
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		step := p.planOne(part)
		if step.Kind == "" {
			continue
		}
		steps = append(steps, step)
	}
	return agent.Plan{Steps: steps}, nil
}

func (p *RulePlanner) planOne(in string) agent.Step {
	if tool, rest, ok := splitPrefix(in); ok {
		args := map[string]any{}
		if s := strings.TrimSpace(rest); s != "" {
			args["input"] = s
		}
		return agent.Step{Kind: agent.StepToolCall, Tool: tool, Args: args}
	}

	lo := strings.ToLower(in)

	if strings.Contains(in, "读取") || strings.Contains(lo, "read") {
		args := map[string]any{}
		path := guessPath(in)
		if path != "" {
			args["path"] = path
		}
		if m := p.readFirstNRe.FindStringSubmatch(in); len(m) == 2 {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				args["lines"] = n
			}
		}
		if strings.Contains(in, "总结") || strings.Contains(lo, "summarize") {
			args["summarize"] = true
		}
		if len(args) == 0 {
			args["input"] = in
		}
		return agent.Step{Kind: agent.StepToolCall, Tool: "read_file", Args: args}
	}

	if strings.Contains(in, "总结") || strings.Contains(lo, "summarize") {
		return agent.Step{Kind: agent.StepToolCall, Tool: "summarize", Args: map[string]any{}}
	}

	if strings.Contains(in, "列出") || strings.Contains(lo, "list") || strings.Contains(lo, "ls") {
		if strings.Contains(in, "目录") || strings.Contains(lo, "dir") || strings.Contains(lo, "folder") || strings.Contains(lo, "files") {
			args := map[string]any{}
			if path := guessPathOrDir(in); path != "" {
				args["path"] = path
			}
			return agent.Step{Kind: agent.StepToolCall, Tool: "list_dir", Args: args}
		}
	}

	if strings.Contains(in, "搜索") || strings.Contains(in, "查找") || strings.Contains(lo, "grep") || strings.Contains(lo, "search") || strings.Contains(lo, "find") {
		args := map[string]any{}
		if pat := guessPattern(in); pat != "" {
			args["pattern"] = pat
		} else {
			args["input"] = in
		}
		if path := guessPathOrDir(in); path != "" {
			args["path"] = path
		}
		return agent.Step{Kind: agent.StepToolCall, Tool: "grep_repo", Args: args}
	}

	if strings.Contains(in, "天气") || strings.Contains(lo, "weather") {
		args := map[string]any{}
		// simple heuristic: remove "weather" or "天气" and use rest as location
		// if "in Beijing", remove "in"
		clean := in
		clean = strings.ReplaceAll(clean, "weather", "")
		clean = strings.ReplaceAll(clean, "天气", "")
		clean = strings.ReplaceAll(clean, " in ", " ")
		clean = strings.TrimSpace(clean)
		if clean == "" {
			clean = "Beijing" // default
		}
		args["location"] = clean
		return agent.Step{Kind: agent.StepToolCall, Tool: "weather", Args: args}
	}

	if looksLikeExpr(in) {
		return agent.Step{Kind: agent.StepToolCall, Tool: "calc", Args: map[string]any{"expr": in}}
	}

	return agent.Step{Kind: agent.StepToolCall, Tool: "echo", Args: map[string]any{"text": in}}
}

func splitPrefix(in string) (tool string, rest string, ok bool) {
	l := strings.ToLower(strings.TrimSpace(in))
	for _, prefix := range []struct {
		p string
		t string
	}{
		{"calc:", "calc"},
		{"calc ", "calc"},
		{"echo:", "echo"},
		{"echo ", "echo"},
		{"read:", "read_file"},
		{"read ", "read_file"},
		{"summarize:", "summarize"},
		{"summarize ", "summarize"},
		{"summary:", "summarize"},
		{"summary ", "summarize"},
		{"ls:", "list_dir"},
		{"ls ", "list_dir"},
		{"list:", "list_dir"},
		{"list ", "list_dir"},
		{"grep:", "grep_repo"},
		{"grep ", "grep_repo"},
		{"search:", "grep_repo"},
		{"search ", "grep_repo"},
		{"weather:", "weather"},
		{"weather ", "weather"},
	} {
		if strings.HasPrefix(l, prefix.p) {
			return prefix.t, strings.TrimSpace(in[len(prefix.p):]), true
		}
	}
	return "", "", false
}

var pathTokenRe = regexp.MustCompile(`(?i)\b([./]?[a-z0-9_\-./]+\.[a-z0-9]{1,8})\b`)
var dirTokenRe = regexp.MustCompile(`(?i)\b([./]?[a-z0-9_\-./]+/)\b`)
var quotedRe = regexp.MustCompile(`["“](.+?)["”]`)
var patternHintRe = regexp.MustCompile(`(?i)\bpattern\s*[:=]\s*([^\s]+)`)

func guessPath(in string) string {
	m := pathTokenRe.FindStringSubmatch(in)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func guessPathOrDir(in string) string {
	if p := guessPath(in); p != "" {
		return p
	}
	m := dirTokenRe.FindStringSubmatch(in)
	if len(m) == 2 {
		return strings.TrimSuffix(m[1], "/")
	}
	return ""
}

func guessPattern(in string) string {
	if m := patternHintRe.FindStringSubmatch(in); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	if m := quotedRe.FindStringSubmatch(in); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func looksLikeExpr(in string) bool {
	if len(in) < 3 {
		return false
	}
	hasDigit := false
	hasOp := false
	for _, r := range in {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '+' || r == '-' || r == '*' || r == '/' || r == '(' || r == ')' || r == '.' || r == ' ':
			if r == '+' || r == '-' || r == '*' || r == '/' {
				hasOp = true
			}
		default:
			return false
		}
	}
	return hasDigit && hasOp
}

func splitIntoParts(in string) []string {
	s := in
	repl := strings.NewReplacer(
		"；", "\n",
		";", "\n",
		"\r\n", "\n",
		"\r", "\n",
		"\n", "\n",
		"然后", "\n",
		"并且", "\n",
		"同时", "\n",
		"接着", "\n",
	)
	s = repl.Replace(s)
	s = regexp.MustCompile(`(?i)\band\s+then\b`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`(?i)\bthen\b`).ReplaceAllString(s, "\n")
	var out []string
	for _, p := range strings.Split(s, "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return []string{in}
	}
	return out
}
