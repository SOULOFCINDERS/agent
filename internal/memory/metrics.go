package memory

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ================================================================
// Step 1: 嵌入式指标采集 (Memory Metrics Collector)
// ================================================================
//
// 功能：
//   1. 在 Search / Compress / Conflict / Compact 关键路径自动采集指标
//   2. 零额外 LLM 开销（纯本地计算）
//   3. 支持 JSON 持久化 + 实时聚合查询
//
// 使用方式：
//   metrics := NewMemoryMetrics(dataDir)
//   store.SetMetrics(metrics)       // 注入到 Store
//   compressor.SetMetrics(metrics)  // 注入到 Compressor
//   // ... 正常使用，指标自动采集
//   report := metrics.Report()      // 随时查看报告

// ================================================================
// 指标记录数据结构
// ================================================================

// MetricType 指标类型
type MetricType string

const (
	MetricSearch   MetricType = "search"
	MetricCompress MetricType = "compress"
	MetricConflict MetricType = "conflict"
	MetricCompact  MetricType = "compact"
)

// MetricRecord 单条指标记录
type MetricRecord struct {
	Timestamp time.Time  `json:"timestamp"`
	Type      MetricType `json:"type"`

	// 检索指标
	SearchQuery     string  `json:"search_query,omitempty"`
	ResultCount     int     `json:"result_count,omitempty"`
	TopScore        float64 `json:"top_score,omitempty"`
	SearchLatencyMs int64   `json:"search_latency_ms,omitempty"`

	// 压缩指标
	OriginalTokens   int     `json:"original_tokens,omitempty"`
	CompressedTokens int     `json:"compressed_tokens,omitempty"`
	CompressionRatio float64 `json:"compression_ratio,omitempty"`
	IsIncremental    bool    `json:"is_incremental,omitempty"`
	OriginalMsgCount int     `json:"original_msg_count,omitempty"`
	CompressedMsgCount int   `json:"compressed_msg_count,omitempty"`

	// 冲突指标
	ConflictType     string  `json:"conflict_type,omitempty"`
	ConflictResolved bool    `json:"conflict_resolved,omitempty"`
	Similarity       float64 `json:"similarity,omitempty"`

	// 合并指标
	ClustersFound  int `json:"clusters_found,omitempty"`
	EntriesMerged  int `json:"entries_merged,omitempty"`
	EntriesCreated int `json:"entries_created,omitempty"`
}

// ================================================================
// 聚合报告数据结构
// ================================================================

// MetricsSummary 聚合统计报告
type MetricsSummary struct {
	// 采集时间范围
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	TotalRecords int    `json:"total_records"`

	// 检索统计
	Search SearchMetrics `json:"search"`

	// 压缩统计
	Compress CompressMetrics `json:"compress"`

	// 冲突统计
	Conflict ConflictMetrics `json:"conflict"`

	// 合并统计
	Compact CompactMetrics `json:"compact"`
}

// SearchMetrics 检索指标聚合
type SearchMetrics struct {
	TotalSearches      int     `json:"total_searches"`
	AvgResultCount     float64 `json:"avg_result_count"`
	ZeroResultRate     float64 `json:"zero_result_rate"`     // 无结果比率
	AvgLatencyMs       float64 `json:"avg_latency_ms"`
	P50LatencyMs       int64   `json:"p50_latency_ms"`
	P99LatencyMs       int64   `json:"p99_latency_ms"`
	AvgTopScore        float64 `json:"avg_top_score"`
}

// CompressMetrics 压缩指标聚合
type CompressMetrics struct {
	TotalCompressions   int     `json:"total_compressions"`
	IncrementalRate     float64 `json:"incremental_rate"`      // 增量压缩占比
	AvgCompressionRatio float64 `json:"avg_compression_ratio"` // 平均压缩率
	TotalTokensSaved    int     `json:"total_tokens_saved"`    // 累计节省 token 数
	AvgTokensSaved      float64 `json:"avg_tokens_saved"`      // 平均每次节省
}

// ConflictMetrics 冲突指标聚合
type ConflictMetrics struct {
	TotalConflicts     int     `json:"total_conflicts"`
	ExplicitCount      int     `json:"explicit_count"`       // 显式覆盖
	SemanticCount      int     `json:"semantic_count"`       // 语义冲突
	NeedConfirmCount   int     `json:"need_confirm_count"`   // 需确认
	AutoResolvedRate   float64 `json:"auto_resolved_rate"`   // 自动解决率
	AvgSimilarity      float64 `json:"avg_similarity"`       // 平均冲突相似度
}

// CompactMetrics 合并指标聚合
type CompactMetrics struct {
	TotalCompactions   int     `json:"total_compactions"`
	TotalClusters      int     `json:"total_clusters"`
	TotalMerged        int     `json:"total_merged"`
	TotalCreated       int     `json:"total_created"`
	AvgClusterSize     float64 `json:"avg_cluster_size"`
}

// ================================================================
// MemoryMetrics 核心结构
// ================================================================

// MemoryMetrics 记忆系统指标采集器
type MemoryMetrics struct {
	mu       sync.Mutex
	records  []MetricRecord
	dataDir  string
	filePath string
}

// NewMemoryMetrics 创建指标采集器
// dataDir 为空时不持久化
func NewMemoryMetrics(dataDir string) *MemoryMetrics {
	m := &MemoryMetrics{
		dataDir: dataDir,
	}
	if dataDir != "" {
		m.filePath = filepath.Join(dataDir, "memory_metrics.json")
		m.load()
	}
	return m
}

// ================================================================
// 采集方法（供各模块调用）
// ================================================================

// TrackSearch 记录一次检索
func (m *MemoryMetrics) TrackSearch(query string, resultCount int, topScore float64, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.records = append(m.records, MetricRecord{
		Timestamp:       time.Now(),
		Type:            MetricSearch,
		SearchQuery:     query,
		ResultCount:     resultCount,
		TopScore:        topScore,
		SearchLatencyMs: latency.Milliseconds(),
	})
	m.autoSave()
}

// TrackCompression 记录一次压缩
func (m *MemoryMetrics) TrackCompression(originalTokens, compressedTokens, originalMsgCount, compressedMsgCount int, incremental bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ratio := 0.0
	if originalTokens > 0 {
		ratio = float64(compressedTokens) / float64(originalTokens)
	}

	m.records = append(m.records, MetricRecord{
		Timestamp:          time.Now(),
		Type:               MetricCompress,
		OriginalTokens:     originalTokens,
		CompressedTokens:   compressedTokens,
		CompressionRatio:   ratio,
		IsIncremental:      incremental,
		OriginalMsgCount:   originalMsgCount,
		CompressedMsgCount: compressedMsgCount,
	})
	m.autoSave()
}

// TrackConflict 记录一次冲突检测
func (m *MemoryMetrics) TrackConflict(conflictType string, autoResolved bool, similarity float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.records = append(m.records, MetricRecord{
		Timestamp:        time.Now(),
		Type:             MetricConflict,
		ConflictType:     conflictType,
		ConflictResolved: autoResolved,
		Similarity:       similarity,
	})
	m.autoSave()
}

// TrackCompaction 记录一次记忆合并
func (m *MemoryMetrics) TrackCompaction(clustersFound, entriesMerged, entriesCreated int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.records = append(m.records, MetricRecord{
		Timestamp:      time.Now(),
		Type:           MetricCompact,
		ClustersFound:  clustersFound,
		EntriesMerged:  entriesMerged,
		EntriesCreated: entriesCreated,
	})
	m.autoSave()
}

// ================================================================
// 聚合报告
// ================================================================

// Report 生成聚合统计报告
func (m *MemoryMetrics) Report() MetricsSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	summary := MetricsSummary{
		TotalRecords: len(m.records),
	}

	if len(m.records) == 0 {
		return summary
	}

	summary.StartTime = m.records[0].Timestamp
	summary.EndTime = m.records[len(m.records)-1].Timestamp

	// 分类统计
	var searchLatencies []int64
	var searchResultCounts []int
	var searchTopScores []float64
	var zeroResultCount int

	var compressionRatios []float64
	var tokensSaved []int
	var incrementalCount int

	var conflictSimilarities []float64
	var autoResolvedCount int

	var clusterSizes []float64

	for _, r := range m.records {
		switch r.Type {
		case MetricSearch:
			summary.Search.TotalSearches++
			searchLatencies = append(searchLatencies, r.SearchLatencyMs)
			searchResultCounts = append(searchResultCounts, r.ResultCount)
			searchTopScores = append(searchTopScores, r.TopScore)
			if r.ResultCount == 0 {
				zeroResultCount++
			}

		case MetricCompress:
			summary.Compress.TotalCompressions++
			compressionRatios = append(compressionRatios, r.CompressionRatio)
			saved := r.OriginalTokens - r.CompressedTokens
			tokensSaved = append(tokensSaved, saved)
			summary.Compress.TotalTokensSaved += saved
			if r.IsIncremental {
				incrementalCount++
			}

		case MetricConflict:
			summary.Conflict.TotalConflicts++
			switch r.ConflictType {
			case "explicit_override":
				summary.Conflict.ExplicitCount++
			case "semantic_conflict":
				summary.Conflict.SemanticCount++
			case "need_confirm":
				summary.Conflict.NeedConfirmCount++
			}
			if r.ConflictResolved {
				autoResolvedCount++
			}
			if r.Similarity > 0 {
				conflictSimilarities = append(conflictSimilarities, r.Similarity)
			}

		case MetricCompact:
			summary.Compact.TotalCompactions++
			summary.Compact.TotalClusters += r.ClustersFound
			summary.Compact.TotalMerged += r.EntriesMerged
			summary.Compact.TotalCreated += r.EntriesCreated
			if r.ClustersFound > 0 && r.EntriesMerged > 0 {
				clusterSizes = append(clusterSizes, float64(r.EntriesMerged)/float64(r.ClustersFound))
			}
		}
	}

	// 检索聚合
	if summary.Search.TotalSearches > 0 {
		summary.Search.AvgResultCount = avgIntSlice(searchResultCounts)
		summary.Search.ZeroResultRate = float64(zeroResultCount) / float64(summary.Search.TotalSearches)
		summary.Search.AvgLatencyMs = avgInt64Slice(searchLatencies)
		summary.Search.P50LatencyMs = percentileInt64(searchLatencies, 50)
		summary.Search.P99LatencyMs = percentileInt64(searchLatencies, 99)
		summary.Search.AvgTopScore = avgFloat64Slice(searchTopScores)
	}

	// 压缩聚合
	if summary.Compress.TotalCompressions > 0 {
		summary.Compress.AvgCompressionRatio = avgFloat64Slice(compressionRatios)
		summary.Compress.IncrementalRate = float64(incrementalCount) / float64(summary.Compress.TotalCompressions)
		summary.Compress.AvgTokensSaved = avgIntSlice(tokensSaved)
	}

	// 冲突聚合
	if summary.Conflict.TotalConflicts > 0 {
		summary.Conflict.AutoResolvedRate = float64(autoResolvedCount) / float64(summary.Conflict.TotalConflicts)
		if len(conflictSimilarities) > 0 {
			summary.Conflict.AvgSimilarity = avgFloat64Slice(conflictSimilarities)
		}
	}

	// 合并聚合
	if len(clusterSizes) > 0 {
		summary.Compact.AvgClusterSize = avgFloat64Slice(clusterSizes)
	}

	return summary
}

// ReportString 生成可读的文本报告
func (m *MemoryMetrics) ReportString() string {
	r := m.Report()
	var b strings.Builder

	b.WriteString("=== Memory System Metrics Report ===\n")
	b.WriteString(fmt.Sprintf("Time Range: %s ~ %s\n", r.StartTime.Format("2006-01-02 15:04"), r.EndTime.Format("2006-01-02 15:04")))
	b.WriteString(fmt.Sprintf("Total Records: %d\n\n", r.TotalRecords))

	// 检索
	b.WriteString("--- Search ---\n")
	b.WriteString(fmt.Sprintf("  Total Searches:    %d\n", r.Search.TotalSearches))
	b.WriteString(fmt.Sprintf("  Avg Result Count:  %.1f\n", r.Search.AvgResultCount))
	b.WriteString(fmt.Sprintf("  Zero Result Rate:  %.1f%%\n", r.Search.ZeroResultRate*100))
	b.WriteString(fmt.Sprintf("  Avg Latency:       %.1fms\n", r.Search.AvgLatencyMs))
	b.WriteString(fmt.Sprintf("  P50 Latency:       %dms\n", r.Search.P50LatencyMs))
	b.WriteString(fmt.Sprintf("  P99 Latency:       %dms\n", r.Search.P99LatencyMs))
	b.WriteString(fmt.Sprintf("  Avg Top Score:     %.2f\n\n", r.Search.AvgTopScore))

	// 压缩
	b.WriteString("--- Compression ---\n")
	b.WriteString(fmt.Sprintf("  Total Compressions:    %d\n", r.Compress.TotalCompressions))
	b.WriteString(fmt.Sprintf("  Incremental Rate:      %.1f%%\n", r.Compress.IncrementalRate*100))
	b.WriteString(fmt.Sprintf("  Avg Compression Ratio: %.2f\n", r.Compress.AvgCompressionRatio))
	b.WriteString(fmt.Sprintf("  Total Tokens Saved:    %d\n", r.Compress.TotalTokensSaved))
	b.WriteString(fmt.Sprintf("  Avg Tokens Saved:      %.0f\n\n", r.Compress.AvgTokensSaved))

	// 冲突
	b.WriteString("--- Conflict ---\n")
	b.WriteString(fmt.Sprintf("  Total Conflicts:    %d\n", r.Conflict.TotalConflicts))
	b.WriteString(fmt.Sprintf("  Explicit:           %d\n", r.Conflict.ExplicitCount))
	b.WriteString(fmt.Sprintf("  Semantic:           %d\n", r.Conflict.SemanticCount))
	b.WriteString(fmt.Sprintf("  Need Confirm:       %d\n", r.Conflict.NeedConfirmCount))
	b.WriteString(fmt.Sprintf("  Auto Resolved Rate: %.1f%%\n", r.Conflict.AutoResolvedRate*100))
	b.WriteString(fmt.Sprintf("  Avg Similarity:     %.3f\n\n", r.Conflict.AvgSimilarity))

	// 合并
	b.WriteString("--- Compaction ---\n")
	b.WriteString(fmt.Sprintf("  Total Compactions:  %d\n", r.Compact.TotalCompactions))
	b.WriteString(fmt.Sprintf("  Total Clusters:     %d\n", r.Compact.TotalClusters))
	b.WriteString(fmt.Sprintf("  Total Merged:       %d\n", r.Compact.TotalMerged))
	b.WriteString(fmt.Sprintf("  Total Created:      %d\n", r.Compact.TotalCreated))
	b.WriteString(fmt.Sprintf("  Avg Cluster Size:   %.1f\n", r.Compact.AvgClusterSize))

	return b.String()
}

// RecordCount 返回当前记录总数
func (m *MemoryMetrics) RecordCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

// Records 返回所有记录的副本
func (m *MemoryMetrics) Records() []MetricRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]MetricRecord, len(m.records))
	copy(cp, m.records)
	return cp
}

// Reset 清空所有指标记录
func (m *MemoryMetrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = nil
	m.autoSave()
}

// ================================================================
// 持久化
// ================================================================

func (m *MemoryMetrics) load() {
	if m.filePath == "" {
		return
	}
	data, err := os.ReadFile(m.filePath)
	if err != nil || len(data) == 0 {
		return
	}
	var records []MetricRecord
	if err := json.Unmarshal(data, &records); err == nil {
		m.records = records
	}
}

// autoSave 每 50 条记录自动保存一次（减少 I/O）
func (m *MemoryMetrics) autoSave() {
	if m.filePath == "" {
		return
	}
	if len(m.records)%50 == 0 || len(m.records) <= 1 {
		m.saveUnsafe()
	}
}

// Save 强制保存（对外暴露）
func (m *MemoryMetrics) Save() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveUnsafe()
}

func (m *MemoryMetrics) saveUnsafe() {
	if m.filePath == "" {
		return
	}
	data, err := json.MarshalIndent(m.records, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.filePath, data, 0644)
}

// ================================================================
// 统计工具函数
// ================================================================

func avgIntSlice(vals []int) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0
	for _, v := range vals {
		sum += v
	}
	return float64(sum) / float64(len(vals))
}

func avgInt64Slice(vals []int64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum int64
	for _, v := range vals {
		sum += v
	}
	return float64(sum) / float64(len(vals))
}

func avgFloat64Slice(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func percentileInt64(vals []int64, pct int) int64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]int64, len(vals))
	copy(sorted, vals)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(math.Ceil(float64(pct)/100.0*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
