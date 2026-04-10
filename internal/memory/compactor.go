package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
	"github.com/SOULOFCINDERS/agent/internal/rag"
)

// ================================================================
// 长期记忆主题合并 (Memory Compactor)
// ================================================================
//
// 功能：
//   1. 按语义相似度对记忆进行聚类
//   2. 对同一簇的多条记忆用 LLM 合并为一条
//   3. 软删除原始条目，保留合并后的新条目
//
// 使用场景：
//   - 定期后台运行（如每天/每周一次）
//   - 记忆条目数超过阈值时触发
//   - 用户主动请求"整理记忆"

// ================================================================
// 数据结构
// ================================================================

// CompactResult 合并结果
type CompactResult struct {
	ClustersFound   int             `json:"clusters_found"`    // 发现的可合并簇数
	EntriesMerged   int             `json:"entries_merged"`    // 被合并的条目总数
	EntriesCreated  int             `json:"entries_created"`   // 新创建的合并条目数
	EntriesRemoved  int             `json:"entries_removed"`   // 被软删除的条目数
	MergeDetails    []MergeDetail   `json:"merge_details"`     // 每个簇的合并详情
}

// MergeDetail 单个簇的合并详情
type MergeDetail struct {
	ClusterTopic   string   `json:"cluster_topic"`    // 簇的主题
	OriginalIDs    []string `json:"original_ids"`     // 原始条目 ID
	OriginalTexts  []string `json:"original_texts"`   // 原始内容（调试用）
	MergedContent  string   `json:"merged_content"`   // 合并后内容
	MergedKeywords []string `json:"merged_keywords"`  // 合并后关键词
	NewID          string   `json:"new_id"`           // 新条目 ID
}

// CompactorConfig 合并器配置
type CompactorConfig struct {
	// SimilarityThreshold 语义相似度阈值，高于此值的记忆对归为同一簇。默认 0.65
	SimilarityThreshold float64

	// MinClusterSize 最小簇大小，只有 ≥ 此值的簇才会被合并。默认 2
	MinClusterSize int

	// MaxClusterSize 最大簇大小，超过此值时拆分。默认 10
	MaxClusterSize int

	// DryRun 只分析不实际合并。默认 false
	DryRun bool
}

// MemoryCompactor 长期记忆合并器
type MemoryCompactor struct {
	store     *Store
	llmClient llm.Client
	embedder  *rag.TFIDFEmbedder
	cfg       CompactorConfig
	metrics   *MemoryMetrics
}

// NewMemoryCompactor 创建记忆合并器
func NewMemoryCompactor(store *Store, client llm.Client, cfg CompactorConfig) *MemoryCompactor {
	if cfg.SimilarityThreshold <= 0 {
		cfg.SimilarityThreshold = 0.65
	}
	if cfg.MinClusterSize <= 0 {
		cfg.MinClusterSize = 2
	}
	if cfg.MaxClusterSize <= 0 {
		cfg.MaxClusterSize = 10
	}
	return &MemoryCompactor{
		store:     store,
		llmClient: client,
		embedder:  rag.NewTFIDFEmbedder(256),
		cfg:       cfg,
	}
}

// SetMetrics 注入指标采集器
func (mc *MemoryCompactor) SetMetrics(m *MemoryMetrics) {
	mc.metrics = m
}

// ================================================================
// 核心方法
// ================================================================

// Compact 执行记忆合并
func (mc *MemoryCompactor) Compact(ctx context.Context) (*CompactResult, error) {
	mc.store.mu.Lock()
	defer mc.store.mu.Unlock()

	result := &CompactResult{}

	// Step 1: 获取所有活跃记忆
	var active []Entry
	var activeIdx []int // 在 entries 中的索引
	for i, e := range mc.store.entries {
		if e.IsActive() {
			active = append(active, e)
			activeIdx = append(activeIdx, i)
		}
	}

	if len(active) < mc.cfg.MinClusterSize {
		return result, nil // 记忆太少，无需合并
	}

	// Step 2: 计算 embedding（复用已有的或重新计算）
	embeddings := make([][]float64, len(active))
	for i, e := range active {
		if len(e.Embedding) > 0 {
			embeddings[i] = e.Embedding
		} else {
			emb, err := mc.embedder.Embed(ctx, e.Content)
			if err != nil {
				continue
			}
			embeddings[i] = emb
		}
	}

	// Step 3: 贪心聚类
	clusters := mc.greedyCluster(active, embeddings)
	result.ClustersFound = len(clusters)

	if mc.cfg.DryRun {
		for _, cluster := range clusters {
			detail := MergeDetail{ClusterTopic: cluster[0].Topic}
			for _, e := range cluster {
				detail.OriginalIDs = append(detail.OriginalIDs, e.ID)
				detail.OriginalTexts = append(detail.OriginalTexts, e.Content)
			}
			result.MergeDetails = append(result.MergeDetails, detail)
			result.EntriesMerged += len(cluster)
		}
		return result, nil
	}

	// Step 4: 逐簇合并
	for _, cluster := range clusters {
		detail, err := mc.mergeCluster(ctx, cluster)
		if err != nil {
			continue // 单簇合并失败不阻塞其他簇
		}

		// Step 5: 软删除原始条目
		for _, e := range cluster {
			for i := range mc.store.entries {
				if mc.store.entries[i].ID == e.ID {
					mc.store.entries[i].SupersededBy = detail.NewID
					break
				}
			}
		}

		// Step 6: 写入合并后的新条目
		var emb []float64
		emb, _ = mc.embedder.Embed(ctx, detail.MergedContent)
		newEntry := Entry{
			ID:         detail.NewID,
			Topic:      detail.ClusterTopic,
			Content:    detail.MergedContent,
			Keywords:   detail.MergedKeywords,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
			Version:    1,
			Confidence: 1.0,
			Embedding:  emb,
		}
		mc.store.entries = append(mc.store.entries, newEntry)

		result.MergeDetails = append(result.MergeDetails, detail)
		result.EntriesMerged += len(cluster)
		result.EntriesCreated++
		result.EntriesRemoved += len(cluster)
	}

	// Step 7: 持久化
	mc.store.save()

	// 指标采集
	if mc.metrics != nil {
		mc.metrics.TrackCompaction(result.ClustersFound, result.EntriesMerged, result.EntriesCreated)
	}

	return result, nil
}

// ================================================================
// 聚类算法
// ================================================================

// greedyCluster 贪心聚类：每条记忆找最相似的伙伴，相似度 > 阈值则归为同簇
// 返回的每个 cluster 都 >= MinClusterSize
func (mc *MemoryCompactor) greedyCluster(entries []Entry, embeddings [][]float64) [][]Entry {
	n := len(entries)
	assigned := make([]bool, n)
	var clusters [][]Entry

	for i := 0; i < n; i++ {
		if assigned[i] || len(embeddings[i]) == 0 {
			continue
		}

		cluster := []Entry{entries[i]}
		assigned[i] = true

		for j := i + 1; j < n; j++ {
			if assigned[j] || len(embeddings[j]) == 0 {
				continue
			}
			if len(cluster) >= mc.cfg.MaxClusterSize {
				break
			}

			sim := rag.CosineSimilarity(embeddings[i], embeddings[j])
			if sim >= mc.cfg.SimilarityThreshold {
				cluster = append(cluster, entries[j])
				assigned[j] = true
			}
		}

		if len(cluster) >= mc.cfg.MinClusterSize {
			clusters = append(clusters, cluster)
		}
	}

	return clusters
}

// ================================================================
// 合并逻辑
// ================================================================

// mergeCluster 用 LLM 将一个簇的记忆合并为一条
func (mc *MemoryCompactor) mergeCluster(ctx context.Context, cluster []Entry) (MergeDetail, error) {
	detail := MergeDetail{
		ClusterTopic: cluster[0].Topic,
	}

	var entriesText strings.Builder
	allKeywords := make(map[string]bool)

	for _, e := range cluster {
		detail.OriginalIDs = append(detail.OriginalIDs, e.ID)
		detail.OriginalTexts = append(detail.OriginalTexts, e.Content)
		entriesText.WriteString(fmt.Sprintf("- [%s] (topic: %s, 更新于 %s): %s\n",
			e.ID, e.Topic, e.UpdatedAt.Format("2006-01-02"), e.Content))
		for _, kw := range e.Keywords {
			allKeywords[kw] = true
		}
	}

	prompt := []llm.Message{
		{
			Role: "system",
			Content: "你是一个记忆整理助手。以下是关于同一主题的多条记忆，请将它们合并为一条完整、准确的记忆。" +
				"\n\n要求：" +
				"\n1. 合并后保留所有重要信息，去除重复" +
				"\n2. 如果有矛盾信息，以最新（更新时间最晚）的为准" +
				"\n3. 保持简洁，一段话概括" +
				"\n4. 以JSON格式返回: {\"content\": \"合并后的内容\", \"topic\": \"主题\", \"keywords\": [\"关键词\"]}" +
				"\n\n只返回JSON，不要其他内容。",
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("需要合并的记忆：\n%s", entriesText.String()),
		},
	}

	resp, err := mc.llmClient.Chat(ctx, prompt, nil)
	if err != nil {
		return detail, fmt.Errorf("merge cluster LLM call: %w", err)
	}

	content := strings.TrimSpace(resp.Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var parsed struct {
		Content  string   `json:"content"`
		Topic    string   `json:"topic"`
		Keywords []string `json:"keywords"`
	}

	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// JSON 解析失败，使用原始回复
		parsed.Content = resp.Message.Content
		parsed.Topic = cluster[0].Topic
		// 合并所有已有关键词
		for kw := range allKeywords {
			parsed.Keywords = append(parsed.Keywords, kw)
		}
	}

	detail.MergedContent = parsed.Content
	detail.ClusterTopic = parsed.Topic
	detail.MergedKeywords = parsed.Keywords
	detail.NewID = fmt.Sprintf("mem_%d", mc.store.nextID)
	mc.store.nextID++

	return detail, nil
}
