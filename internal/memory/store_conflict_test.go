package memory

import (
	"os"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir, err := os.MkdirTemp("", "mem_conflict_test_*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStore_Add_SameTopicOverride(t *testing.T) {
	s := newTestStore(t)

	r1 := s.Add("主题偏好", "用户喜欢深色主题", nil)
	if r1.Conflict != nil {
		t.Fatal("first add should have no conflict")
	}

	r2 := s.Add("主题偏好", "用户喜欢浅色主题", nil)
	if r2.Conflict == nil {
		t.Fatal("second add with same topic should detect conflict")
	}
	if r2.Conflict.Type != "explicit_override" {
		t.Errorf("Type = %q, want explicit_override", r2.Conflict.Type)
	}
	if r2.Conflict.OldContent != "用户喜欢深色主题" {
		t.Errorf("OldContent = %q", r2.Conflict.OldContent)
	}
	// Entry should be updated in-place (same ID)
	if r2.Entry.ID != r1.Entry.ID {
		t.Errorf("same-topic update should reuse ID: got %s, want %s", r2.Entry.ID, r1.Entry.ID)
	}
	if r2.Entry.Version != 2 {
		t.Errorf("Version = %d, want 2", r2.Entry.Version)
	}
	// Count should still be 1
	if s.Count() != 1 {
		t.Errorf("Count = %d, want 1", s.Count())
	}
}

func TestStore_Add_ExplicitNegation(t *testing.T) {
	s := newTestStore(t)

	s.Add("饮食", "用户喜欢吃烤肉", nil)
	r2 := s.Add("饮食变更", "用户不再喜欢吃烤肉了", nil)

	if r2.Conflict == nil {
		t.Fatal("explicit negation should detect conflict")
	}
	if r2.Conflict.Type != "explicit_override" {
		t.Errorf("Type = %q, want explicit_override", r2.Conflict.Type)
	}
	if !r2.Conflict.AutoResolved {
		t.Error("explicit negation should be auto-resolved")
	}
	// Old entry should be superseded
	entries := s.List(0)
	// After supersede, only the new entry should be active
	activeCount := 0
	for _, e := range entries {
		if e.IsActive() {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Errorf("active count = %d, want 1", activeCount)
	}
}

func TestStore_Add_NoConflict(t *testing.T) {
	s := newTestStore(t)

	s.Add("编程语言", "用户喜欢 Go", nil)
	r2 := s.Add("邮箱", "用户的邮箱是 test@example.com", nil)

	// Completely different topic and content — should have no conflict
	if r2.Conflict != nil && r2.Conflict.Type != "" {
		t.Errorf("unrelated entries should not conflict, got type=%q", r2.Conflict.Type)
	}
}

func TestStore_Search_ExcludesSuperseded(t *testing.T) {
	s := newTestStore(t)

	s.Add("主题", "用户喜欢深色主题", []string{"主题", "深色"})
	s.Add("主题", "用户喜欢浅色主题", []string{"主题", "浅色"})

	results := s.Search("主题", 10)
	if len(results) != 1 {
		t.Errorf("Search should return 1 active entry, got %d", len(results))
	}
	if results[0].Content != "用户喜欢浅色主题" {
		t.Errorf("Content = %q, want latest", results[0].Content)
	}
}

func TestStore_Count_ExcludesSuperseded(t *testing.T) {
	s := newTestStore(t)

	s.Add("A", "内容A", nil)
	s.Add("B", "内容B", nil)
	if s.Count() != 2 {
		t.Errorf("Count = %d, want 2", s.Count())
	}

	// Update topic A → supersedes in-place
	s.Add("A", "内容A更新", nil)
	if s.Count() != 2 {
		t.Errorf("Count after update = %d, want 2", s.Count())
	}
}

func TestStore_DecayConfidence(t *testing.T) {
	s := newTestStore(t)
	r := s.Add("test", "some content", nil)
	if r.Entry.Confidence != 1.0 {
		t.Errorf("initial confidence = %f, want 1.0", r.Entry.Confidence)
	}

	// DecayConfidence should not crash and should be idempotent for fresh entries
	s.DecayConfidence()
	entries := s.List(0)
	if len(entries) == 0 {
		t.Fatal("no entries after decay")
	}
	// Fresh entry should still have very high confidence
	if entries[0].Confidence < 0.9 {
		t.Errorf("fresh entry confidence = %f, should be > 0.9", entries[0].Confidence)
	}
}
