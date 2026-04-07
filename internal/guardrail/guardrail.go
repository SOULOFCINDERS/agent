// Package guardrail implements safety guardrails for the Agent Harness layer.
// It provides concrete Guard implementations (keyword filter, PII detector,
// injection detector) and a Pipeline that chains them together.
package guardrail

import (
	"context"
	"regexp"
	"strings"
	"sync"

	gd "github.com/SOULOFCINDERS/agent/internal/domain/guardrail"
)

// ---- GuardPipeline ----

// GuardPipeline chains multiple guards in registration order.
type GuardPipeline struct {
	mu     sync.RWMutex
	guards []gd.Guard
}

// NewPipeline creates an empty GuardPipeline.
func NewPipeline() *GuardPipeline { return &GuardPipeline{} }

// Add registers a guard.
func (p *GuardPipeline) Add(g gd.Guard) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.guards = append(p.guards, g)
}

// Run executes all guards matching the given phase.
// Short-circuits on ActionBlock; merges ActionRedact results.
func (p *GuardPipeline) Run(ctx context.Context, phase gd.Phase, text string) gd.CheckResult {
	p.mu.RLock()
	guards := make([]gd.Guard, len(p.guards))
	copy(guards, p.guards)
	p.mu.RUnlock()

	merged := gd.CheckResult{Action: gd.ActionPass}
	cur := text

	for _, g := range guards {
		if g.Phase()&phase == 0 {
			continue
		}
		r := g.Check(ctx, cur)
		merged.Violations = append(merged.Violations, r.Violations...)

		switch r.Action {
		case gd.ActionBlock:
			merged.Action = gd.ActionBlock
			merged.BlockReason = r.BlockReason
			merged.GuardName = r.GuardName
			return merged
		case gd.ActionRedact:
			if merged.Action < gd.ActionRedact {
				merged.Action = gd.ActionRedact
			}
			merged.RedactedContent = r.RedactedContent
			merged.GuardName = r.GuardName
			cur = r.RedactedContent
		}
	}
	return merged
}

// ---- KeywordGuard ----

// KeywordRule defines a keyword matching rule.
type KeywordRule struct {
	Keyword  string
	Category string
	Severity string
	Block    bool
}

// KeywordGuard detects sensitive keywords in text.
type KeywordGuard struct {
	name  string
	phase gd.Phase
	rules []KeywordRule
	mask  string
}

// KeywordGuardOption configures a KeywordGuard.
type KeywordGuardOption func(*KeywordGuard)

// WithKeywordMask sets the replacement string.
func WithKeywordMask(mask string) KeywordGuardOption {
	return func(g *KeywordGuard) { g.mask = mask }
}

// WithKeywordPhase sets active phases.
func WithKeywordPhase(phase gd.Phase) KeywordGuardOption {
	return func(g *KeywordGuard) { g.phase = phase }
}

// NewKeywordGuard creates a keyword filter guard.
func NewKeywordGuard(rules []KeywordRule, opts ...KeywordGuardOption) *KeywordGuard {
	g := &KeywordGuard{name: "keyword_filter", phase: gd.PhaseAll, rules: rules, mask: "***"}
	for _, o := range opts {
		o(g)
	}
	return g
}

func (g *KeywordGuard) Name() string    { return g.name }
func (g *KeywordGuard) Phase() gd.Phase { return g.phase }

func (g *KeywordGuard) Check(_ context.Context, text string) gd.CheckResult {
	result := gd.CheckResult{Action: gd.ActionPass, GuardName: g.name}
	lower := strings.ToLower(text)
	redacted := text
	needsRedact := false

	for _, rule := range g.rules {
		kw := strings.ToLower(rule.Keyword)
		if !strings.Contains(lower, kw) {
			continue
		}
		result.Violations = append(result.Violations, gd.Violation{
			Rule: "keyword_" + rule.Category, Severity: rule.Severity,
			Description: "detected sensitive keyword: " + rule.Keyword,
		})
		if rule.Block {
			result.Action = gd.ActionBlock
			result.BlockReason = "\u5185\u5bb9\u5305\u542b\u8fdd\u89c4\u5173\u952e\u8bcd\uff0c\u8bf7\u6c42\u5df2\u88ab\u62d2\u7edd"
			return result
		}
		re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(rule.Keyword))
		redacted = re.ReplaceAllString(redacted, g.mask)
		needsRedact = true
	}
	if needsRedact {
		result.Action = gd.ActionRedact
		result.RedactedContent = redacted
	}
	return result
}

// ---- PIIGuard ----

type piiPattern struct {
	name     string
	regex    *regexp.Regexp
	severity string
	mask     string
}

// PIIGuard detects and redacts Personally Identifiable Information.
type PIIGuard struct {
	name     string
	phase    gd.Phase
	patterns []piiPattern
}

// PIIGuardOption configures a PIIGuard.
type PIIGuardOption func(*PIIGuard)

// WithPIIPhase sets active phases.
func WithPIIPhase(phase gd.Phase) PIIGuardOption {
	return func(g *PIIGuard) { g.phase = phase }
}

// NewPIIGuard creates a PII detector with built-in patterns.
func NewPIIGuard(opts ...PIIGuardOption) *PIIGuard {
	g := &PIIGuard{
		name:  "pii_detector",
		phase: gd.PhaseAll,
		patterns: []piiPattern{
			{name: "phone_cn", regex: regexp.MustCompile(`1[3-9]\d{9}`), severity: "high", mask: "[PHONE]"},
			{name: "id_card_cn", regex: regexp.MustCompile(`[1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx]`), severity: "critical", mask: "[ID_CARD]"},
			{name: "email", regex: regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`), severity: "high", mask: "[EMAIL]"},
			{name: "bank_card", regex: regexp.MustCompile(`(?:6[2]\d{2}|4\d{3}|5[1-5]\d{2})\s?\d{4}\s?\d{4}\s?\d{4}(?:\s?\d{1,3})?`), severity: "critical", mask: "[BANK_CARD]"},
			{name: "ip_address", regex: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), severity: "medium", mask: "[IP]"},
		},
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

func (g *PIIGuard) Name() string    { return g.name }
func (g *PIIGuard) Phase() gd.Phase { return g.phase }

func (g *PIIGuard) Check(_ context.Context, text string) gd.CheckResult {
	result := gd.CheckResult{Action: gd.ActionPass, GuardName: g.name}
	redacted := text
	needsRedact := false

	for _, p := range g.patterns {
		matches := p.regex.FindAllStringIndex(text, -1)
		if len(matches) == 0 {
			continue
		}
		for _, loc := range matches {
			result.Violations = append(result.Violations, gd.Violation{
				Rule: "pii_" + p.name, Severity: p.severity,
				Description: "detected PII: " + p.name, Span: [2]int{loc[0], loc[1]},
			})
		}
		redacted = p.regex.ReplaceAllString(redacted, p.mask)
		needsRedact = true
	}
	if needsRedact {
		result.Action = gd.ActionRedact
		result.RedactedContent = redacted
	}
	return result
}

// ---- PromptInjectionGuard ----

// PromptInjectionGuard detects common prompt injection patterns.
type PromptInjectionGuard struct {
	name     string
	phase    gd.Phase
	patterns []*regexp.Regexp
}

// NewPromptInjectionGuard creates a prompt injection detector.
func NewPromptInjectionGuard() *PromptInjectionGuard {
	return &PromptInjectionGuard{
		name:  "prompt_injection",
		phase: gd.PhaseInput | gd.PhaseToolResult,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)ignore\s+(all\s+)?previous\s+instructions`),
			regexp.MustCompile(`(?i)ignore\s+(all\s+)?above\s+instructions`),
			regexp.MustCompile(`(?i)disregard\s+(all\s+)?previous`),
			regexp.MustCompile(`(?i)forget\s+(all\s+)?previous`),
			regexp.MustCompile(`(?i)you\s+are\s+now\s+(?:a|an|the)\s+\w+`),
			regexp.MustCompile(`(?i)new\s+instructions?\s*:`),
			regexp.MustCompile(`(?i)system\s*:\s*you\s+are`),
			regexp.MustCompile(`(?i)\bDAN\b.*\bjailbreak\b`),
			regexp.MustCompile(`(?i)act\s+as\s+(?:if\s+)?you\s+(?:have\s+)?no\s+(?:rules|restrictions|limitations)`),
		},
	}
}

func (g *PromptInjectionGuard) Name() string    { return g.name }
func (g *PromptInjectionGuard) Phase() gd.Phase { return g.phase }

func (g *PromptInjectionGuard) Check(_ context.Context, text string) gd.CheckResult {
	result := gd.CheckResult{Action: gd.ActionPass, GuardName: g.name}
	for _, p := range g.patterns {
		loc := p.FindStringIndex(text)
		if loc == nil {
			continue
		}
		result.Violations = append(result.Violations, gd.Violation{
			Rule: "prompt_injection", Severity: "critical",
			Description: "detected prompt injection pattern", Span: [2]int{loc[0], loc[1]},
		})
		result.Action = gd.ActionBlock
		result.BlockReason = "\u68c0\u6d4b\u5230 Prompt \u6ce8\u5165\u653b\u51fb\uff0c\u8bf7\u6c42\u5df2\u88ab\u62d2\u7edd"
		return result
	}
	return result
}

// ---- LengthGuard ----

// LengthGuard blocks inputs exceeding a character limit.
type LengthGuard struct {
	name     string
	phase    gd.Phase
	maxChars int
}

// NewLengthGuard creates a guard that blocks text exceeding maxChars.
func NewLengthGuard(maxChars int) *LengthGuard {
	return &LengthGuard{name: "length_limit", phase: gd.PhaseInput, maxChars: maxChars}
}

func (g *LengthGuard) Name() string    { return g.name }
func (g *LengthGuard) Phase() gd.Phase { return g.phase }

func (g *LengthGuard) Check(_ context.Context, text string) gd.CheckResult {
	result := gd.CheckResult{Action: gd.ActionPass, GuardName: g.name}
	if len([]rune(text)) > g.maxChars {
		result.Action = gd.ActionBlock
		result.BlockReason = "\u8f93\u5165\u8fc7\u957f\uff0c\u8bf7\u7f29\u77ed\u540e\u91cd\u8bd5"
		result.Violations = append(result.Violations, gd.Violation{
			Rule: "length_exceeded", Severity: "medium", Description: "input exceeds max length",
		})
	}
	return result
}

// ---- DefaultPipeline ----

// DefaultPipeline creates a pipeline with sensible defaults.
func DefaultPipeline(keywords []KeywordRule, maxInputChars int) *GuardPipeline {
	p := NewPipeline()
	p.Add(NewPromptInjectionGuard())
	p.Add(NewPIIGuard())
	if len(keywords) > 0 {
		p.Add(NewKeywordGuard(keywords))
	}
	if maxInputChars > 0 {
		p.Add(NewLengthGuard(maxInputChars))
	}
	return p
}
