package memory

import (
	"context"
	"testing"

	dmem "github.com/SOULOFCINDERS/agent/internal/domain/memory"
)

func TestDetectExplicitOverride_NegationHit(t *testing.T) {
	d := NewConflictDetector()
	entries := []Entry{
		{ID: "mem_1", Content: "用户喜欢深色主题", Confidence: 0.8},
	}

	cr := d.DetectExplicitOverride("用户不再喜欢深色主题，改为浅色", entries)
	if cr == nil {
		t.Fatal("expected conflict result, got nil")
	}
	if cr.Type != dmem.ConflictExplicit {
		t.Errorf("Type = %q, want %q", cr.Type, dmem.ConflictExplicit)
	}
	if cr.ConflictingID != "mem_1" {
		t.Errorf("ConflictingID = %q, want mem_1", cr.ConflictingID)
	}
	if !cr.AutoResolved {
		t.Error("explicit override should be auto-resolved")
	}
}

func TestDetectExplicitOverride_NoNegation(t *testing.T) {
	d := NewConflictDetector()
	entries := []Entry{
		{ID: "mem_1", Content: "用户喜欢深色主题", Confidence: 0.8},
	}

	cr := d.DetectExplicitOverride("用户喜欢 Go 语言", entries)
	if cr != nil {
		t.Errorf("expected nil conflict, got %+v", cr)
	}
}

func TestDetectExplicitOverride_SkipsSuperseded(t *testing.T) {
	d := NewConflictDetector()
	entries := []Entry{
		{ID: "mem_1", Content: "用户喜欢深色主题", SupersededBy: "mem_2"},
	}

	cr := d.DetectExplicitOverride("用户不再喜欢深色主题", entries)
	if cr != nil {
		t.Error("should skip superseded entries")
	}
}

func TestDetectSemanticConflict_SimilarContent(t *testing.T) {
	d := NewConflictDetector()
	entries := []Entry{
		{ID: "mem_1", Content: "用户喜欢吃素食，不吃肉", Confidence: 0.8},
		{ID: "mem_2", Content: "用户的邮箱是 test@example.com", Confidence: 1.0},
	}

	ctx := context.Background()
	cr := d.DetectSemanticConflict(ctx, "用户喜欢吃烤肉", entries)
	if cr == nil {
		t.Fatal("expected semantic conflict, got nil")
	}
	if cr.ConflictingID != "mem_1" {
		t.Errorf("ConflictingID = %q, want mem_1", cr.ConflictingID)
	}
	if cr.Similarity <= 0 {
		t.Errorf("Similarity should be > 0, got %f", cr.Similarity)
	}
}

func TestDetectSemanticConflict_NoConflict(t *testing.T) {
	d := NewConflictDetector()
	entries := []Entry{
		{ID: "mem_1", Content: "用户喜欢深色主题", Confidence: 1.0},
	}

	ctx := context.Background()
	cr := d.DetectSemanticConflict(ctx, "用户的邮箱是 test@example.com", entries)
	if cr != nil {
		t.Errorf("expected no conflict for unrelated content, got similarity=%f", cr.Similarity)
	}
}

func TestDetectSemanticConflict_IdenticalContentNotConflict(t *testing.T) {
	d := NewConflictDetector()
	entries := []Entry{
		{ID: "mem_1", Content: "用户喜欢深色主题", Confidence: 1.0},
	}

	ctx := context.Background()
	cr := d.DetectSemanticConflict(ctx, "用户喜欢深色主题", entries)
	if cr != nil {
		t.Error("identical content should not be flagged as conflict")
	}
}

func TestCompareConfidence(t *testing.T) {
	tests := []struct {
		name        string
		newConf     float64
		oldConf     float64
		wantAuto    bool
		wantKeepNew bool
	}{
		{"new much higher", 1.0, 0.5, true, true},
		{"old much higher", 0.3, 0.8, true, false},
		{"close values", 0.7, 0.6, false, false},
		{"both high", 1.0, 0.9, false, false},
		{"zero new defaults to 1.0", 0, 0.5, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auto, keepNew := CompareConfidence(tt.newConf, tt.oldConf)
			if auto != tt.wantAuto {
				t.Errorf("autoResolve = %v, want %v", auto, tt.wantAuto)
			}
			if keepNew != tt.wantKeepNew {
				t.Errorf("keepNew = %v, want %v", keepNew, tt.wantKeepNew)
			}
		})
	}
}

func TestComputeEmbedding(t *testing.T) {
	d := NewConflictDetector()
	ctx := context.Background()
	emb := d.ComputeEmbedding(ctx, "用户喜欢深色主题")
	if emb == nil {
		t.Fatal("embedding should not be nil")
	}
	if len(emb) != 256 {
		t.Errorf("embedding dim = %d, want 256", len(emb))
	}
}
