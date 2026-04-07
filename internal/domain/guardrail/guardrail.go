// Package guardrail defines domain interfaces for input/output safety checks
// in the Agent Harness layer. Guardrails are executed before and after LLM calls
// to enforce content safety policies (sensitive word filtering, PII detection, etc.).
package guardrail

import "context"

// Action describes what the harness should do when a guard triggers.
type Action int

const (
	ActionPass   Action = iota // content is safe
	ActionRedact               // auto-sanitized; use RedactedContent
	ActionBlock                // unsafe; refuse with BlockReason
)

// String returns a human-readable action name.
func (a Action) String() string {
	switch a {
	case ActionPass:
		return "pass"
	case ActionRedact:
		return "redact"
	case ActionBlock:
		return "block"
	default:
		return "unknown"
	}
}

// Violation records a single safety issue found during a check.
type Violation struct {
	Rule        string // e.g., "pii_phone", "keyword_violence"
	Severity    string // "low", "medium", "high", "critical"
	Description string
	Span        [2]int // byte offsets in original text (optional)
}

// CheckResult is the outcome of a single guard evaluation.
type CheckResult struct {
	Action          Action
	Violations      []Violation
	RedactedContent string // non-empty when Action == ActionRedact
	BlockReason     string // non-empty when Action == ActionBlock
	GuardName       string
}

// Phase indicates when in the harness loop a guard should run.
type Phase int

const (
	PhaseInput      Phase = 1 << iota // check user input before LLM
	PhaseOutput                       // check LLM output before returning
	PhaseToolResult                   // check tool results before feeding back
	PhaseAll = PhaseInput | PhaseOutput | PhaseToolResult
)

// Guard is the core interface for a safety check.
// Implementations must be safe for concurrent use.
type Guard interface {
	Name() string
	Phase() Phase
	Check(ctx context.Context, text string) CheckResult
}

// Pipeline aggregates multiple guards and evaluates them in order.
type Pipeline interface {
	Add(g Guard)
	Run(ctx context.Context, phase Phase, text string) CheckResult
}
