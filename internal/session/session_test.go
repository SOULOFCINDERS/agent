package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
	ds "github.com/SOULOFCINDERS/agent/internal/domain/session"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "agent_session_test_"+time.Now().Format("150405.000"))
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestJSONStoreSaveAndLoad(t *testing.T) {
	store, err := NewJSONStore(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	sess := &Session{
		ID:        "sess_test_001",
		Title:     "Test Session",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Messages: []ds.Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
		},
		Metadata: ds.Metadata{TurnCount: 1},
	}

	if err := store.Save(ctx, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, "sess_test_001")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Title != "Test Session" {
		t.Errorf("Title = %q, want %q", loaded.Title, "Test Session")
	}
	if len(loaded.Messages) != 3 {
		t.Errorf("Messages = %d, want 3", len(loaded.Messages))
	}
	if loaded.Metadata.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", loaded.Metadata.TurnCount)
	}
}

func TestJSONStoreLoadNotFound(t *testing.T) {
	store, err := NewJSONStore(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Load(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestJSONStoreList(t *testing.T) {
	dir := tempDir(t)
	store, err := NewJSONStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Create 3 sessions with different update times
	for i, title := range []string{"First", "Second", "Third"} {
		sess := &Session{
			ID:        fmt.Sprintf("sess_%03d", i+1),
			Title:     title,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now().Add(time.Duration(i) * time.Hour),
			Metadata:  ds.Metadata{TurnCount: i + 1},
		}
		if err := store.Save(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}

	summaries, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("got %d summaries, want 3", len(summaries))
	}
	// Should be ordered by UpdatedAt desc: Third, Second, First
	if summaries[0].Title != "Third" {
		t.Errorf("first summary title = %q, want %q", summaries[0].Title, "Third")
	}
	if summaries[2].Title != "First" {
		t.Errorf("last summary title = %q, want %q", summaries[2].Title, "First")
	}

	// Test limit
	summaries2, err := store.List(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries2) != 2 {
		t.Errorf("got %d summaries with limit=2, want 2", len(summaries2))
	}
}

func TestJSONStoreDelete(t *testing.T) {
	store, err := NewJSONStore(tempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	sess := &Session{
		ID:        "sess_del",
		Title:     "To Delete",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = store.Save(ctx, sess)

	if err := store.Delete(ctx, "sess_del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = store.Load(ctx, "sess_del")
	if err == nil {
		t.Fatal("expected error after deletion")
	}
}

func TestManagerNewSessionAndSave(t *testing.T) {
	store, _ := NewJSONStore(tempDir(t))
	mgr := NewManager(store)
	ctx := context.Background()

	sess := mgr.NewSession()
	if sess.ID == "" {
		t.Fatal("session ID should not be empty")
	}

	history := []conv.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "记住我喜欢 Go 语言"},
		{Role: "assistant", Content: "好的，我记住了！"},
	}

	if err := mgr.SaveHistory(ctx, sess, history); err != nil {
		t.Fatalf("SaveHistory: %v", err)
	}

	if sess.Title == "" || sess.Title == "untitled" {
		t.Error("title should be auto-generated from first user message")
	}
	if sess.Metadata.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", sess.Metadata.TurnCount)
	}
}

func TestManagerLoadHistory(t *testing.T) {
	store, _ := NewJSONStore(tempDir(t))
	mgr := NewManager(store)
	ctx := context.Background()

	sess := mgr.NewSession()
	origHistory := []conv.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!"},
		{Role: "user", Content: "How are you?"},
		{Role: "assistant", Content: "I'm fine."},
	}
	_ = mgr.SaveHistory(ctx, sess, origHistory)

	loaded, history, err := mgr.LoadHistory(ctx, sess.ID)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if loaded.ID != sess.ID {
		t.Errorf("loaded ID = %q, want %q", loaded.ID, sess.ID)
	}
	if len(history) != 5 {
		t.Errorf("history len = %d, want 5", len(history))
	}
	if history[1].Content != "Hello" {
		t.Errorf("history[1].Content = %q, want %q", history[1].Content, "Hello")
	}
}

func TestAutoTitle(t *testing.T) {
	tests := []struct {
		name    string
		msgs    []conv.Message
		want    string
		partial bool // if true, just check prefix
	}{
		{
			name: "short message",
			msgs: []conv.Message{{Role: "user", Content: "Hello"}},
			want: "Hello",
		},
		{
			name: "long message truncated",
			msgs: []conv.Message{{Role: "user", Content: "这是一段非常长的消息，超过四十个字符的部分应该被截断并显示省略号，确保标题不会太长影响显示效果"}},
			partial: true,
			want:    "这是一段非常长的消息，超过四十个字符的部分应该被截断并显示省略号，确保标题不",
		},
		{
			name: "system only",
			msgs: []conv.Message{{Role: "system", Content: "You are helpful."}},
			want: "untitled",
		},
		{
			name:    "with newline",
			msgs:    []conv.Message{{Role: "user", Content: "Line1\nLine2"}},
			want:    "Line1 Line2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := autoTitle(tt.msgs)
			if tt.partial {
				if got[:len(tt.want)] != tt.want {
					t.Errorf("autoTitle() = %q, want prefix %q", got, tt.want)
				}
			} else {
				if got != tt.want {
					t.Errorf("autoTitle() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}
