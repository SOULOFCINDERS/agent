package agent

type StepKind string

const (
	StepToolCall StepKind = "tool_call"
	StepFinal    StepKind = "final"
)

type Step struct {
	Kind StepKind       `json:"kind"`
	Tool string         `json:"tool,omitempty"`
	Args map[string]any `json:"args,omitempty"`
	Text string         `json:"text,omitempty"`
}

type Plan struct {
	Steps []Step `json:"steps"`
}
