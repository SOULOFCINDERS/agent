package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryMetrics_TrackSearch(t *testing.T) {
	m := NewMemoryMetrics("")

	m.TrackSearch("golang", 5, 3.5, 15*time.Millisecond)
	m.TrackSearch("python", 0, 0, 8*time.Millisecond)
	m.TrackSearch("rust", 3, 2.1, 22*time.Millisecond)

	if got := m.RecordCount(); got != 3 {
		t.Fatalf("RecordCount = %d, want 3", got)
	}

	r := m.Report()
	if r.Search.TotalSearches != 3 {
		t.Errorf("TotalSearches = %d, want 3", r.Search.TotalSearches)
	}
	// 1 out of 3 searches had zero results
	if r.Search.ZeroResultRate < 0.3 || r.Search.ZeroResultRate > 0.34 {
		t.Errorf("ZeroResultRate = %.2f, want ~0.33", r.Search.ZeroResultRate)
	}
	// Avg result count: (5+0+3)/3 = 2.67
	if r.Search.AvgResultCount < 2.5 || r.Search.AvgResultCount > 2.8 {
		t.Errorf("AvgResultCount = %.2f, want ~2.67", r.Search.AvgResultCount)
	}
}

func TestMemoryMetrics_TrackCompression(t *testing.T) {
	m := NewMemoryMetrics("")

	m.TrackCompression(5000, 1500, 20, 5, false)
	m.TrackCompression(3000, 800, 15, 4, true)

	r := m.Report()
	if r.Compress.TotalCompressions != 2 {
		t.Errorf("TotalCompressions = %d, want 2", r.Compress.TotalCompressions)
	}
	if r.Compress.IncrementalRate != 0.5 {
		t.Errorf("IncrementalRate = %.2f, want 0.5", r.Compress.IncrementalRate)
	}
	// Total saved: (5000-1500) + (3000-800) = 3500 + 2200 = 5700
	if r.Compress.TotalTokensSaved != 5700 {
		t.Errorf("TotalTokensSaved = %d, want 5700", r.Compress.TotalTokensSaved)
	}
}

func TestMemoryMetrics_TrackConflict(t *testing.T) {
	m := NewMemoryMetrics("")

	m.TrackConflict("explicit_override", true, 0)
	m.TrackConflict("semantic_conflict", true, 0.75)
	m.TrackConflict("need_confirm", false, 0.55)

	r := m.Report()
	if r.Conflict.TotalConflicts != 3 {
		t.Errorf("TotalConflicts = %d, want 3", r.Conflict.TotalConflicts)
	}
	if r.Conflict.ExplicitCount != 1 {
		t.Errorf("ExplicitCount = %d, want 1", r.Conflict.ExplicitCount)
	}
	if r.Conflict.SemanticCount != 1 {
		t.Errorf("SemanticCount = %d, want 1", r.Conflict.SemanticCount)
	}
	if r.Conflict.NeedConfirmCount != 1 {
		t.Errorf("NeedConfirmCount = %d, want 1", r.Conflict.NeedConfirmCount)
	}
	// 2 out of 3 auto-resolved
	if r.Conflict.AutoResolvedRate < 0.66 || r.Conflict.AutoResolvedRate > 0.67 {
		t.Errorf("AutoResolvedRate = %.2f, want ~0.67", r.Conflict.AutoResolvedRate)
	}
}

func TestMemoryMetrics_TrackCompaction(t *testing.T) {
	m := NewMemoryMetrics("")

	m.TrackCompaction(3, 8, 3)
	m.TrackCompaction(2, 5, 2)

	r := m.Report()
	if r.Compact.TotalCompactions != 2 {
		t.Errorf("TotalCompactions = %d, want 2", r.Compact.TotalCompactions)
	}
	if r.Compact.TotalClusters != 5 {
		t.Errorf("TotalClusters = %d, want 5", r.Compact.TotalClusters)
	}
	if r.Compact.TotalMerged != 13 {
		t.Errorf("TotalMerged = %d, want 13", r.Compact.TotalMerged)
	}
	if r.Compact.TotalCreated != 5 {
		t.Errorf("TotalCreated = %d, want 5", r.Compact.TotalCreated)
	}
}

func TestMemoryMetrics_Persistence(t *testing.T) {
	dir := t.TempDir()
	m := NewMemoryMetrics(dir)

	m.TrackSearch("test", 3, 2.0, 10*time.Millisecond)
	m.Save() // Force save

	// Verify file exists
	fp := filepath.Join(dir, "memory_metrics.json")
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		t.Fatal("metrics file not created")
	}

	// Load into new instance
	m2 := NewMemoryMetrics(dir)
	if got := m2.RecordCount(); got != 1 {
		t.Errorf("Loaded RecordCount = %d, want 1", got)
	}

	r := m2.Report()
	if r.Search.TotalSearches != 1 {
		t.Errorf("Loaded TotalSearches = %d, want 1", r.Search.TotalSearches)
	}
}

func TestMemoryMetrics_ReportString(t *testing.T) {
	m := NewMemoryMetrics("")

	m.TrackSearch("test", 3, 2.0, 10*time.Millisecond)
	m.TrackCompression(5000, 1500, 20, 5, false)
	m.TrackConflict("explicit_override", true, 0)
	m.TrackCompaction(2, 4, 2)

	s := m.ReportString()
	if s == "" {
		t.Fatal("ReportString returned empty")
	}
	// Should contain section headers
	for _, section := range []string{"Search", "Compression", "Conflict", "Compaction"} {
		if !contains(s, section) {
			t.Errorf("Report missing section: %s", section)
		}
	}
}

func TestMemoryMetrics_Reset(t *testing.T) {
	m := NewMemoryMetrics("")

	m.TrackSearch("test", 3, 2.0, 10*time.Millisecond)
	m.TrackCompression(5000, 1500, 20, 5, false)

	if got := m.RecordCount(); got != 2 {
		t.Fatalf("RecordCount = %d, want 2", got)
	}

	m.Reset()
	if got := m.RecordCount(); got != 0 {
		t.Errorf("After reset RecordCount = %d, want 0", got)
	}

	r := m.Report()
	if r.TotalRecords != 0 {
		t.Errorf("After reset TotalRecords = %d, want 0", r.TotalRecords)
	}
}

func TestMemoryMetrics_EmptyReport(t *testing.T) {
	m := NewMemoryMetrics("")

	r := m.Report()
	if r.TotalRecords != 0 {
		t.Errorf("Empty TotalRecords = %d, want 0", r.TotalRecords)
	}
	if r.Search.TotalSearches != 0 {
		t.Errorf("Empty TotalSearches = %d, want 0", r.Search.TotalSearches)
	}
}

func TestMemoryMetrics_PercentileCalculation(t *testing.T) {
	m := NewMemoryMetrics("")

	// Add searches with known latencies
	for _, latMs := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 100} {
		m.TrackSearch("q", 1, 1.0, time.Duration(latMs)*time.Millisecond)
	}

	r := m.Report()
	// P50 should be around 5ms
	if r.Search.P50LatencyMs < 4 || r.Search.P50LatencyMs > 6 {
		t.Errorf("P50 = %d, want ~5", r.Search.P50LatencyMs)
	}
	// P99 should be 100ms
	if r.Search.P99LatencyMs != 100 {
		t.Errorf("P99 = %d, want 100", r.Search.P99LatencyMs)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
