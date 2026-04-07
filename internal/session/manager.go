package session

import (
	"context"
	"fmt"
	"time"

	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
	ds "github.com/SOULOFCINDERS/agent/internal/domain/session"
)

// Manager coordinates session lifecycle operations: create, save, resume, list.
// It bridges the domain session types with the conversation.Message type used
// by LoopAgent.
type Manager struct {
	store ds.Store
}

// NewManager creates a Manager backed by the given Store.
func NewManager(store ds.Store) *Manager {
	return &Manager{store: store}
}

// NewSession creates a fresh session with a generated ID.
func (m *Manager) NewSession() *Session {
	id := generateID()
	now := time.Now()
	return &Session{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// SaveHistory persists the current conversation history into the session.
// It auto-generates a title from the first user message if not already set.
func (m *Manager) SaveHistory(ctx context.Context, sess *Session, history []conv.Message) error {
	sess.Messages = convToSessionMessages(history)
	sess.UpdatedAt = time.Now()
	sess.Metadata.TurnCount = countTurns(history)

	if sess.Title == "" {
		sess.Title = autoTitle(history)
	}

	return m.store.Save(ctx, sess)
}

// LoadHistory restores a session and returns the conversation history.
func (m *Manager) LoadHistory(ctx context.Context, id string) (*Session, []conv.Message, error) {
	sess, err := m.store.Load(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return sess, sessionToConvMessages(sess.Messages), nil
}

// List returns recent session summaries.
func (m *Manager) List(ctx context.Context, limit int) ([]Summary, error) {
	return m.store.List(ctx, limit)
}

// Delete removes a session.
func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.store.Delete(ctx, id)
}

// ---------- Conversion helpers ----------

func convToSessionMessages(msgs []conv.Message) []ds.Message {
	out := make([]ds.Message, len(msgs))
	for i, m := range msgs {
		out[i] = ds.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
	}
	return out
}

func sessionToConvMessages(msgs []ds.Message) []conv.Message {
	out := make([]conv.Message, len(msgs))
	for i, m := range msgs {
		out[i] = conv.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
	}
	return out
}

// autoTitle generates a short title from the first user message.
func autoTitle(history []conv.Message) string {
	for _, m := range history {
		if m.Role == conv.RoleUser && m.Content != "" {
			title := m.Content
			const maxRunes = 40
			runes := []rune(title)
			if len(runes) > maxRunes {
				title = string(runes[:maxRunes]) + "..."
			}
			// Replace newlines with spaces for a clean single-line title
			for _, ch := range []string{"\n", "\r", "\t"} {
				title = replaceAll(title, ch, " ")
			}
			return title
		}
	}
	return "untitled"
}

func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// countTurns counts user/assistant exchanges.
func countTurns(history []conv.Message) int {
	count := 0
	for _, m := range history {
		if m.Role == conv.RoleUser {
			count++
		}
	}
	return count
}

// generateID creates a time-based session ID: sess_20060102_150405
func generateID() string {
	return fmt.Sprintf("sess_%s", time.Now().Format("20060102_150405"))
}

// Ensure utf8 is used (avoid unused import)
