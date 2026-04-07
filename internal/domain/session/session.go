// Package session defines domain types and interfaces for conversation persistence.
// A Session captures the full history of a multi-turn dialogue so it can be
// saved, listed, and resumed across CLI invocations.
package session

import (
	"context"
	"time"
)

// Message mirrors conversation.Message but lives in the domain/session package
// to avoid a circular dependency. In practice, callers convert between the two.
type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Session represents a persisted conversation (aggregate root).
type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`      // human-readable, auto-generated from first user message
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  []Message `json:"messages"`
	Metadata  Metadata  `json:"metadata,omitempty"`
}

// Metadata holds optional session-level metadata.
type Metadata struct {
	Model       string `json:"model,omitempty"`
	TotalTokens int    `json:"total_tokens,omitempty"`
	TurnCount   int    `json:"turn_count,omitempty"`
}

// Summary is a lightweight view used for listing sessions without full message history.
type Summary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	TurnCount int       `json:"turn_count"`
}

// Store defines the persistence interface for sessions.
// Implementations live in the infrastructure layer.
type Store interface {
	// Save persists a session (create or update).
	Save(ctx context.Context, s *Session) error

	// Load retrieves a session by ID. Returns an error if not found.
	Load(ctx context.Context, id string) (*Session, error)

	// List returns summaries of recent sessions, ordered by UpdatedAt desc.
	List(ctx context.Context, limit int) ([]Summary, error)

	// Delete removes a session by ID.
	Delete(ctx context.Context, id string) error
}
