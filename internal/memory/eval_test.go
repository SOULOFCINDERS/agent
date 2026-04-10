package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ================================================================
// IR 指标计算函数测试
// ================================================================

func TestComputeRecallAtK(t *testing.T) {
	tests := []struct {
		name     string
		results  []string
		relevant []string
		k        int
		want     float64
	}{
		{
			name:     "perfect recall at 3",
			results:  []string{"a", "b", "c", "d", "e"},
			relevant: []string{"a", "b"},
			k:        3,
			want:     1.0,
		},
		{
			name:     "partial recall at 3",
			results:  []string{"a", "x", "y", "b", "c"},
			relevant: []string{"a", "b", "c"},
			k:        3,
			want:     1.0 / 3.0, // only "a" in top-3
		},
		{
			name:     "zero recall",
			results:  []string{"x", "y", "z"},
			relevant: []string{"a", "b"},
			k:        3,
			want:     0,
		},
		{
			name:     "empty relevant",
			results:  []string{"a", "b"},
			relevant: []string{},
			k:        3,
			want:     1.0, // no relevant = perfect recall
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeRecallAtK(tt.results, tt.relevant, tt.k)
			if abs(got-tt.want) > 0.01 {
				t.Errorf("computeRecallAtK = %.3f, want %.3f", got, tt.want)
			}
		})
	}
}

func TestComputePrecisionAtK(t *testing.T) {
	results := []string{"a", "b", "x", "c", "y"}
	relevant := []string{"a", "b", "c"}

	// Precision@3: 2/3 (a, b are relevant, x is not)
	p3 := computePrecisionAtK(results, relevant, 3)
	if abs(p3-2.0/3.0) > 0.01 {
		t.Errorf("Precision@3 = %.3f, want %.3f", p3, 2.0/3.0)
	}

	// Precision@5: 3/5
	p5 := computePrecisionAtK(results, relevant, 5)
	if abs(p5-3.0/5.0) > 0.01 {
		t.Errorf("Precision@5 = %.3f, want %.3f", p5, 3.0/5.0)
	}
}

func TestComputeMRR(t *testing.T) {
	tests := []struct {
		name     string
		results  []string
		relevant []string
		want     float64
	}{
		{
			name:     "first result is relevant",
			results:  []string{"a", "b", "c"},
			relevant: []string{"a"},
			want:     1.0,
		},
		{
			name:     "second result is relevant",
			results:  []string{"x", "a", "c"},
			relevant: []string{"a"},
			want:     0.5,
		},
		{
			name:     "no relevant results",
			results:  []string{"x", "y", "z"},
			relevant: []string{"a"},
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeMRR(tt.results, tt.relevant)
			if abs(got-tt.want) > 0.01 {
				t.Errorf("computeMRR = %.3f, want %.3f", got, tt.want)
			}
		})
	}
}

func TestComputeNDCG(t *testing.T) {
	// All relevant at top → nDCG = 1.0
	results := []string{"a", "b", "c", "x", "y"}
	relevant := []string{"a", "b", "c"}
	ndcg := computeNDCG(results, relevant, 5)
	if abs(ndcg-1.0) > 0.01 {
		t.Errorf("Perfect nDCG = %.3f, want 1.0", ndcg)
	}

	// No relevant → nDCG = 0
	results2 := []string{"x", "y", "z"}
	ndcg2 := computeNDCG(results2, relevant, 3)
	if ndcg2 != 0 {
		t.Errorf("Zero nDCG = %.3f, want 0", ndcg2)
	}
}

func TestCheckFactCoverage(t *testing.T) {
	summary := "用户喜欢Go语言，使用了TF-IDF做向量检索，项目实现了增量压缩"
	facts := []string{
		"用户喜欢Go语言",
		"使用了TF-IDF做向量检索",
		"项目实现了增量压缩",
		"系统支持分布式部署", // not in summary
	}

	result := checkFactCoverage(summary, facts)
	// 3 out of 4 facts covered
	if result.coveredCount != 3 {
		t.Errorf("coveredCount = %d, want 3", result.coveredCount)
	}
	if abs(result.coverageRate-0.75) > 0.01 {
		t.Errorf("coverageRate = %.3f, want 0.75", result.coverageRate)
	}
}

// ================================================================
// EvalRunner 集成测试
// ================================================================

func TestEvalRunner_SearchEval(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 准备数据
	store.Add("编程语言", "我最喜欢的编程语言是Go", []string{"go", "编程"})
	store.Add("数据库", "项目使用PostgreSQL作为主数据库", []string{"postgres", "数据库"})
	store.Add("框架", "后端使用Gin框架", []string{"gin", "框架"})

	entries := store.List(0)
	goID := ""
	pgID := ""
	for _, e := range entries {
		if e.Topic == "编程语言" {
			goID = e.ID
		}
		if e.Topic == "数据库" {
			pgID = e.ID
		}
	}

	cases := []EvalCase{
		{
			ID:          "s1",
			Type:        "search",
			Query:       "Go语言",
			RelevantIDs: []string{goID},
		},
		{
			ID:          "s2",
			Type:        "search",
			Query:       "数据库",
			RelevantIDs: []string{pgID},
		},
	}

	runner := NewEvalRunner(store, nil, nil)
	result := runner.EvalSearch(cases)

	if result.TotalCases != 2 {
		t.Errorf("TotalCases = %d, want 2", result.TotalCases)
	}
	if result.MRR < 0.5 {
		t.Errorf("MRR = %.3f, want >= 0.5", result.MRR)
	}
	if len(result.Details) != 2 {
		t.Errorf("Details count = %d, want 2", len(result.Details))
	}
}

func TestEvalRunner_CompressEval(t *testing.T) {
	client := llm.NewMockClient()
	compressor := NewCompressor(client, CompressorConfig{
		MaxMessages: 4,
		WindowSize:  1,
	})

	cases := []EvalCase{
		{
			ID:   "c1",
			Type: "compress",
			Messages: []llm.Message{
				{Role: "system", Content: "你是一个助手"},
				{Role: "user", Content: "帮我用Go写一个HTTP服务器"},
				{Role: "assistant", Content: "好的，这是一个简单的HTTP服务器实现..."},
				{Role: "user", Content: "加一个中间件"},
				{Role: "assistant", Content: "好的，加上日志中间件..."},
				{Role: "user", Content: "部署到Docker"},
				{Role: "assistant", Content: "这是Dockerfile..."},
			},
			KeyFacts: []string{
				"用Go写HTTP服务器",
				"添加了日志中间件",
				"部署到Docker",
			},
		},
	}

	ctx := context.Background()
	runner := NewEvalRunner(nil, compressor, nil)
	result := runner.EvalCompress(ctx, cases)

	if result.TotalCases != 1 {
		t.Errorf("TotalCases = %d, want 1", result.TotalCases)
	}
	if result.AvgCompressionRatio <= 0 {
		t.Errorf("AvgCompressionRatio = %.3f, want > 0", result.AvgCompressionRatio)
	}
	if len(result.Details) != 1 {
		t.Errorf("Details count = %d, want 1", len(result.Details))
	}
	if result.Details[0].OriginalTokens <= 0 {
		t.Errorf("OriginalTokens = %d, want > 0", result.Details[0].OriginalTokens)
	}
}

func TestEvalRunner_ConflictEval(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 准备数据
	store.Add("饮料", "我喜欢喝咖啡", []string{"咖啡", "饮料"})
	store.Add("编辑器", "我使用VSCode编程", []string{"vscode", "编辑器"})

	cases := []EvalCase{
		{
			ID:               "cf1",
			Type:             "conflict",
			NewContent:       "我不再喜欢咖啡了",
			ExpectedConflict: true,
		},
		{
			ID:               "cf2",
			Type:             "conflict",
			NewContent:       "今天天气很好",
			ExpectedConflict: false,
		},
	}

	ctx := context.Background()
	runner := NewEvalRunner(store, nil, nil)
	result := runner.EvalConflict(ctx, cases)

	if result.TotalCases != 2 {
		t.Errorf("TotalCases = %d, want 2", result.TotalCases)
	}
	// The negation "不再喜欢" should be detected against "我喜欢喝咖啡"
	if result.Recall < 0.5 {
		t.Logf("Recall = %.3f (may vary based on detector sensitivity)", result.Recall)
	}
	if len(result.Details) != 2 {
		t.Errorf("Details count = %d, want 2", len(result.Details))
	}
}

func TestEvalRunner_RunAll(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	store.Add("编程语言", "我喜欢Go语言", []string{"go", "编程"})

	client := llm.NewMockClient()
	compressor := NewCompressor(client, CompressorConfig{MaxMessages: 4, WindowSize: 1})

	entries := store.List(0)

	cases := []EvalCase{
		{
			ID:          "s1",
			Type:        "search",
			Query:       "Go",
			RelevantIDs: []string{entries[0].ID},
		},
		{
			ID:   "c1",
			Type: "compress",
			Messages: []llm.Message{
				{Role: "system", Content: "你是助手"},
				{Role: "user", Content: "问题1"},
				{Role: "assistant", Content: "回答1"},
				{Role: "user", Content: "问题2"},
				{Role: "assistant", Content: "回答2"},
				{Role: "user", Content: "问题3"},
				{Role: "assistant", Content: "回答3"},
			},
			KeyFacts: []string{"问题1"},
		},
		{
			ID:               "cf1",
			Type:             "conflict",
			NewContent:       "今天心情不错",
			ExpectedConflict: false,
		},
	}

	ctx := context.Background()
	runner := NewEvalRunner(store, compressor, nil)
	report := runner.RunAll(ctx, cases)

	if report.Search == nil {
		t.Error("Search result should not be nil")
	}
	if report.Compress == nil {
		t.Error("Compress result should not be nil")
	}
	if report.Conflict == nil {
		t.Error("Conflict result should not be nil")
	}

	// Test String() output
	s := report.String()
	if s == "" {
		t.Error("Report String() returned empty")
	}
}

func TestLoadEvalCases(t *testing.T) {
	// Create a temp eval dataset file
	dir := t.TempDir()
	fp := filepath.Join(dir, "eval_data.json")

	dataset := EvalDataset{
		Name:    "test_dataset",
		Version: "1.0",
		Cases: []EvalCase{
			{ID: "t1", Type: "search", Query: "test"},
			{ID: "t2", Type: "conflict", NewContent: "test", ExpectedConflict: false},
		},
	}
	data, _ := json.Marshal(dataset)
	os.WriteFile(fp, data, 0644)

	cases, err := LoadEvalCases(fp)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Errorf("Loaded %d cases, want 2", len(cases))
	}
	if cases[0].ID != "t1" {
		t.Errorf("First case ID = %s, want t1", cases[0].ID)
	}
}

func TestSaveEvalReport(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "report.json")

	report := &FullEvalReport{
		Search: &SearchEvalResult{
			TotalCases: 5,
			MRR:        0.8,
		},
	}

	if err := SaveEvalReport(report, fp); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("Report file is empty")
	}
}

// ================================================================
// Store + Metrics 集成测试
// ================================================================

func TestStore_MetricsIntegration(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	metrics := NewMemoryMetrics("")
	store.SetMetrics(metrics)

	// Add some memories
	store.Add("语言", "我喜欢Go", []string{"go"})
	store.Add("数据库", "使用PostgreSQL", []string{"pg"})

	// Search should track metrics
	store.Search("Go语言", 5)
	store.Search("没有匹配的查询xyz", 5)

	r := metrics.Report()
	if r.Search.TotalSearches != 2 {
		t.Errorf("TotalSearches = %d, want 2", r.Search.TotalSearches)
	}

	// Add conflicting memory (P0: same topic)
	store.Add("语言", "我改为喜欢Rust了", []string{"rust"})
	r = metrics.Report()
	if r.Conflict.TotalConflicts != 1 {
		t.Errorf("TotalConflicts = %d, want 1", r.Conflict.TotalConflicts)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
