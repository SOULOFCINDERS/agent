// Package session implements conversation persistence using JSON files.
// Each session is stored as a separate file: <dataDir>/<sessionID>.json.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	ds "github.com/SOULOFCINDERS/agent/internal/domain/session"
)

// ---------- Type aliases ----------

// Session is an alias for the domain Session type.
type Session = ds.Session

// Summary is an alias for the domain Summary type.
type Summary = ds.Summary

// ---------- JSONStore ----------

// JSONStore persists sessions as individual JSON files in a directory.
// It implements ds.Store.
type JSONStore struct {
	mu      sync.RWMutex
	dataDir string
}

// NewJSONStore creates a JSONStore, ensuring the data directory exists.
func NewJSONStore(dataDir string) (*JSONStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}
	return &JSONStore{dataDir: dataDir}, nil
}

func (s *JSONStore) Save(_ context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", sess.ID, err)
	}
	fp := s.filePath(sess.ID)
	if err := os.WriteFile(fp, data, 0644); err != nil {
		return fmt.Errorf("write session %s: %w", sess.ID, err)
	}
	return nil
}

func (s *JSONStore) Load(_ context.Context, id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.filePath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session %s not found", id)
		}
		return nil, fmt.Errorf("read session %s: %w", id, err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session %s: %w", id, err)
	}
	return &sess, nil
}

func (s *JSONStore) List(_ context.Context, limit int) ([]Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}

	var summaries []Summary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dataDir, e.Name()))
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		summaries = append(summaries, Summary{
			ID:        sess.ID,
			Title:     sess.Title,
			CreatedAt: sess.CreatedAt,
			UpdatedAt: sess.UpdatedAt,
			TurnCount: sess.Metadata.TurnCount,
		})
	}

	// Order by UpdatedAt descending
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, nil
}

func (s *JSONStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fp := s.filePath(id)
	if err := os.Remove(fp); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %s not found", id)
		}
		return fmt.Errorf("delete session %s: %w", id, err)
	}
	return nil
}

func (s *JSONStore) filePath(id string) string {
	return filepath.Join(s.dataDir, id+".json")
}

// Compile-time interface check
var _ ds.Store = (*JSONStore)(nil)
