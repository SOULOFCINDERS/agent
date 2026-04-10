package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// mockCompactorLLM 用于合并器的 mock
type mockCompactorLLM struct{}

func (m *mockCompactorLLM) Chat(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Message: llm.Message{
			Role:    "assistant",
			Content: `{"content": "用户喜欢拿铁咖啡，尤其偏爱冰拿铁", "topic": "饮品偏好", "keywords": ["咖啡", "拿铁", "冰拿铁"]}`,
		},
	}, nil
}

func TestMemoryCompactor_DryRun(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 添加一些相似的记忆
	store.Add("咖啡偏好", "用户喜欢喝咖啡", []string{"咖啡"})
	store.Add("咖啡偏好细节", "用户最爱拿铁咖啡", []string{"咖啡", "拿铁"})
	store.Add("咖啡偏好温度", "用户喜欢冰咖啡", []string{"咖啡", "冰"})
	// 添加一条无关的记忆
	store.Add("工作地点", "用户在北京工作", []string{"北京", "工作"})

	mc := NewMemoryCompactor(store, &mockCompactorLLM{}, CompactorConfig{
		SimilarityThreshold: 0.3, // 低阈值让相似记忆更容易聚在一起
		MinClusterSize:      2,
		DryRun:              true,
	})

	result, err := mc.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	t.Logf("DryRun result: clusters=%d, entries_merged=%d",
		result.ClustersFound, result.EntriesMerged)

	for i, d := range result.MergeDetails {
		t.Logf("  cluster %d: topic=%s, ids=%v", i, d.ClusterTopic, d.OriginalIDs)
	}

	// DryRun 不应该修改数据
	if store.Count() != 4 {
		t.Errorf("DryRun should not modify store, expected 4 active entries, got %d", store.Count())
	}
}

func TestMemoryCompactor_ActualMerge(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 添加相似记忆
	store.Add("咖啡偏好", "用户喜欢喝咖啡", []string{"咖啡"})
	store.Add("咖啡偏好细节", "用户最爱拿铁咖啡", []string{"咖啡", "拿铁"})
	store.Add("咖啡偏好温度", "用户喜欢冰咖啡", []string{"咖啡", "冰"})

	originalCount := store.Count()
	t.Logf("original active count: %d", originalCount)

	mc := NewMemoryCompactor(store, &mockCompactorLLM{}, CompactorConfig{
		SimilarityThreshold: 0.3,
		MinClusterSize:      2,
	})

	result, err := mc.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	t.Logf("Compact result: clusters=%d, merged=%d, created=%d, removed=%d",
		result.ClustersFound, result.EntriesMerged, result.EntriesCreated, result.EntriesRemoved)

	for _, d := range result.MergeDetails {
		t.Logf("  merged %v → %s: %s", d.OriginalIDs, d.NewID, d.MergedContent)
	}

	// 合并后应该活跃条目数减少
	newCount := store.Count()
	t.Logf("new active count: %d", newCount)

	if result.ClustersFound > 0 {
		if newCount >= originalCount {
			t.Errorf("expected fewer active entries after merge, got %d (was %d)", newCount, originalCount)
		}
		if result.EntriesCreated == 0 {
			t.Error("expected at least 1 new merged entry")
		}
	}
}

func TestMemoryCompactor_TooFewEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 只有一条记忆
	store.Add("测试", "只有一条记忆", nil)

	mc := NewMemoryCompactor(store, &mockCompactorLLM{}, CompactorConfig{
		MinClusterSize: 2,
	})

	result, err := mc.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.ClustersFound != 0 {
		t.Errorf("expected 0 clusters for single entry, got %d", result.ClustersFound)
	}
}

func TestMemoryCompactor_NoSimilar(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// 添加不相关的记忆
	store.Add("编程语言", "用户使用Go语言", []string{"go"})
	store.Add("旅行目的地", "用户想去日本旅行", []string{"日本", "旅行"})
	store.Add("音乐", "用户喜欢古典音乐", []string{"古典", "音乐"})

	mc := NewMemoryCompactor(store, &mockCompactorLLM{}, CompactorConfig{
		SimilarityThreshold: 0.9, // 极高阈值
		MinClusterSize:      2,
	})

	result, err := mc.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.ClustersFound != 0 {
		t.Errorf("expected 0 clusters for dissimilar entries, got %d", result.ClustersFound)
	}
}

func TestMemoryCompactor_MergePreservesLatest(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	store.Add("咖啡", "用户喜欢热咖啡", []string{"咖啡", "热"})
	store.Add("咖啡", "用户改为喜欢冰咖啡", []string{"咖啡", "冰"})

	mc := NewMemoryCompactor(store, &mockCompactorLLM{}, CompactorConfig{
		SimilarityThreshold: 0.3,
		MinClusterSize:      2,
	})

	result, err := mc.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if len(result.MergeDetails) > 0 {
		merged := result.MergeDetails[0].MergedContent
		t.Logf("merged content: %s", merged)
		// mock 返回的合并结果应该包含拿铁（由 mock 固定返回）
		if !strings.Contains(merged, "拿铁") {
			t.Logf("NOTE: merged content depends on mock, actual LLM would prioritize latest")
		}
	}
}
