package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	dmem "github.com/SOULOFCINDERS/agent/internal/domain/memory"
)

// ---------- 类型别名：从 domain/memory 引入 ----------

type Entry = dmem.Entry
type AddResult = dmem.AddResult

// Store 是记忆的持久化存储（具体实现）
type Store struct {
	mu       sync.RWMutex
	entries  []Entry
	filePath string
	nextID   int
	detector *ConflictDetector
	metrics  *MemoryMetrics
}

// NewStore 创建/加载一个记忆存储
func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create memory dir: %w", err)
	}

	fp := filepath.Join(dataDir, "memory.json")
	s := &Store{
		filePath: fp,
		nextID:   1,
		detector: NewConflictDetector(),
	}

	// 尝试加载已有数据
	data, err := os.ReadFile(fp)
	if err == nil && len(data) > 0 {
		var entries []Entry
		if err := json.Unmarshal(data, &entries); err == nil {
			s.entries = entries
			for _, e := range entries {
				var n int
				if _, err := fmt.Sscanf(e.ID, "mem_%d", &n); err == nil && n >= s.nextID {
					s.nextID = n + 1
				}
			}
		}
	}

	return s, nil
}

// SetMetrics 注入指标采集器
func (s *Store) SetMetrics(m *MemoryMetrics) {
	s.metrics = m
}

// Metrics 获取指标采集器
func (s *Store) Metrics() *MemoryMetrics {
	return s.metrics
}

// Add 添加一条记忆（含 P0→P1→P2→P3 冲突检测流水线）
func (s *Store) Add(topic, content string, keywords []string) AddResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	now := time.Now()
	result := AddResult{}

	// ============================================================
	// P0: 同 Topic 覆盖（最常见路径）
	// ============================================================
	for i, e := range s.entries {
		if !e.IsActive() || !strings.EqualFold(e.Topic, topic) {
			continue
		}
		// 同 topic 旧记忆存在 → 就地更新
		result.Conflict = &dmem.ConflictResult{
			Type:          dmem.ConflictExplicit,
			ConflictingID: e.ID,
			OldContent:    e.Content,
			NewContent:    content,
			AutoResolved:  true,
			Resolution:    fmt.Sprintf("同主题更新 v%d→v%d", e.Version, e.Version+1),
		}
		s.entries[i].Content = content
		s.entries[i].UpdatedAt = now
		s.entries[i].Version++
		s.entries[i].Confidence = 1.0
		if s.detector != nil {
			s.entries[i].Embedding = s.detector.ComputeEmbedding(ctx, content)
		}
		if len(keywords) > 0 {
			s.entries[i].Keywords = keywords
		}
		s.save()
		// 指标采集: P0 同 Topic 覆盖
		if s.metrics != nil {
			s.metrics.TrackConflict(string(result.Conflict.Type), true, 0)
		}
		result.Entry = s.entries[i]
		return result
	}

	// ============================================================
	// P0: 显式否定检测（跨 topic）
	// ============================================================
	if s.detector != nil {
		if cr := s.detector.DetectExplicitOverride(content, s.entries); cr != nil {
			newID := fmt.Sprintf("mem_%d", s.nextID)
			for i := range s.entries {
				if s.entries[i].ID == cr.ConflictingID {
					s.entries[i].SupersededBy = newID
					break
				}
			}
			result.Conflict = cr
		}
	}

	// ============================================================
	// P1 + P2: 语义匹配 + 置信度裁决（仅在 P0 未命中时）
	// ============================================================
	if result.Conflict == nil && s.detector != nil {
		if cr := s.detector.DetectSemanticConflict(ctx, content, s.entries); cr != nil {
			// 找到语义冲突的旧记忆，用 P2 置信度裁决
			for _, e := range s.entries {
				if e.ID == cr.ConflictingID {
					autoResolve, keepNew := CompareConfidence(1.0, e.Confidence)
					if autoResolve && keepNew {
						cr.AutoResolved = true
						cr.Resolution = "新记忆置信度更高，已自动取代旧记忆"
						newID := fmt.Sprintf("mem_%d", s.nextID)
						for j := range s.entries {
							if s.entries[j].ID == cr.ConflictingID {
								s.entries[j].SupersededBy = newID
								break
							}
						}
					} else if autoResolve && !keepNew {
						cr.AutoResolved = true
						cr.Resolution = "旧记忆置信度更高，新记忆已保存但请注意可能存在矛盾"
					} else {
						// P3: 无法自动裁决 → 需要用户确认
						cr.Type = dmem.ConflictNeedConfirm
						cr.AutoResolved = false
						cr.Resolution = "置信度相近，建议用户确认"
					}
					result.Conflict = cr
					break
				}
			}
		}
	}

	// ============================================================
	// 写入新记忆
	// ============================================================
	var emb []float64
	if s.detector != nil {
		emb = s.detector.ComputeEmbedding(ctx, content)
	}
	entry := Entry{
		ID:         fmt.Sprintf("mem_%d", s.nextID),
		Topic:      topic,
		Content:    content,
		Keywords:   keywords,
		CreatedAt:  now,
		UpdatedAt:  now,
		Version:    1,
		Confidence: 1.0,
		Embedding:  emb,
	}
	s.nextID++
	s.entries = append(s.entries, entry)
	s.save()
	result.Entry = entry
	return result
}

// Search 搜索记忆，返回按相关度排序的结果（仅返回 active 记忆）
func (s *Store) Search(query string, limit int) []Entry {
	searchStart := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 10
	}

	query = strings.ToLower(query)
	queryWords := tokenize(query)

	type scored struct {
		entry Entry
		score float64
	}

	var results []scored
	for _, e := range s.entries {
		if !e.IsActive() {
			continue
		}
		score := relevanceScore(e, queryWords, query)
		if score > 0 {
			results = append(results, scored{entry: e, score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if len(results) > limit {
		results = results[:limit]
	}

	out := make([]Entry, len(results))
	for i, r := range results {
		out[i] = r.entry
		s.touchEntry(r.entry.ID)
	}
	// 指标采集
	if s.metrics != nil {
		topScore := 0.0
		if len(results) > 0 {
			topScore = results[0].score
		}
		s.metrics.TrackSearch(query, len(out), topScore, time.Since(searchStart))
	}

	return out
}

// List 返回所有活跃记忆，按更新时间倒序
func (s *Store) List(limit int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var active []Entry
	for _, e := range s.entries {
		if e.IsActive() {
			active = append(active, e)
		}
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].UpdatedAt.After(active[j].UpdatedAt)
	})

	if limit > 0 && len(active) > limit {
		active = active[:limit]
	}
	return active
}

// Delete 删除一条记忆
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, e := range s.entries {
		if e.ID == id {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			s.save()
			return true
		}
	}
	return false
}

// Count 返回活跃记忆总数
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, e := range s.entries {
		if e.IsActive() {
			count++
		}
	}
	return count
}

// Summary 生成记忆摘要，用于注入 system prompt
func (s *Store) Summary(maxEntries int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var active []Entry
	for _, e := range s.entries {
		if e.IsActive() {
			active = append(active, e)
		}
	}
	if len(active) == 0 {
		return ""
	}

	if maxEntries <= 0 {
		maxEntries = 20
	}

	now := time.Now()
	sort.Slice(active, func(i, j int) bool {
		si := float64(active[i].AccessCnt) + 10.0/math.Max(1, now.Sub(active[i].UpdatedAt).Hours()+1)
		sj := float64(active[j].AccessCnt) + 10.0/math.Max(1, now.Sub(active[j].UpdatedAt).Hours()+1)
		return si > sj
	})

	if len(active) > maxEntries {
		active = active[:maxEntries]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("你有 %d 条已保存的记忆：\n", len(active)))
	for _, e := range active {
		b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", e.Topic, e.ID, e.Content))
	}
	return b.String()
}

// RelevantSummary 根据当前查询返回相关记忆的摘要（仅 active，含时效标注）
func (s *Store) RelevantSummary(query string, maxEntries int) string {
	if maxEntries <= 0 {
		maxEntries = 5
	}

	results := s.Search(query, maxEntries)
	if len(results) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("与当前对话相关的记忆（共 %d/%d 条）：\n", len(results), s.Count()))
	b.WriteString("提醒：用户当前的明确指令优先于以下记忆。\n")
	for _, e := range results {
		age := time.Since(e.UpdatedAt)
		freshness := "🟢"
		if age > 90*24*time.Hour {
			freshness = "🔴"
		} else if age > 30*24*time.Hour {
			freshness = "🟡"
		}
		b.WriteString(fmt.Sprintf("- %s [%s] %s", freshness, e.Topic, e.Content))
		if e.Version > 1 {
			b.WriteString(fmt.Sprintf(" (v%d, 更新于%s)", e.Version, e.UpdatedAt.Format("2006-01-02")))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// DecayConfidence 对所有记忆执行时间衰减（建议在会话开始时调用一次）
// 半衰期 90 天，访问频率加成
func (s *Store) DecayConfidence() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	changed := false
	for i, e := range s.entries {
		if !e.IsActive() {
			continue
		}
		daysSinceUpdate := now.Sub(e.UpdatedAt).Hours() / 24.0
		// 半衰期 90 天
		newConf := math.Pow(0.5, daysSinceUpdate/90.0)
		// 访问频率加成（上限 0.3）
		accessBonus := math.Min(0.3, float64(e.AccessCnt)*0.02)
		newConf = math.Min(1.0, newConf+accessBonus)

		if math.Abs(newConf-s.entries[i].Confidence) > 0.01 {
			s.entries[i].Confidence = newConf
			changed = true
		}
	}
	if changed {
		s.save()
	}
}

// --- 内部方法 ---

func (s *Store) save() {
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.filePath, data, 0644)
}

func (s *Store) touchEntry(id string) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, e := range s.entries {
			if e.ID == id {
				s.entries[i].AccessCnt++
				s.save()
				return
			}
		}
	}()
}

// --- 文本相关度计算 ---

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var words []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r >= 0x4e00)
	}) {
		if len(w) > 0 {
			words = append(words, w)
		}
	}
	var cjk []string
	for _, w := range words {
		runes := []rune(w)
		if len(runes) > 1 && isCJK(runes[0]) {
			for i := 0; i < len(runes); i++ {
				cjk = append(cjk, string(runes[i]))
				if i+1 < len(runes) {
					cjk = append(cjk, string(runes[i:i+2]))
				}
			}
		}
	}
	return append(words, cjk...)
}

func isCJK(r rune) bool {
	return r >= 0x4e00 && r <= 0x9fff
}

func relevanceScore(e Entry, queryWords []string, rawQuery string) float64 {
	score := 0.0
	contentLower := strings.ToLower(e.Content)
	topicLower := strings.ToLower(e.Topic)
	allText := topicLower + " " + contentLower + " " + strings.ToLower(strings.Join(e.Keywords, " "))

	if strings.Contains(allText, rawQuery) {
		score += 5.0
	}

	for _, w := range queryWords {
		if utf8.RuneCountInString(w) < 1 {
			continue
		}
		if strings.Contains(topicLower, w) {
			score += 3.0
		}
		if strings.Contains(contentLower, w) {
			score += 1.0
		}
		for _, kw := range e.Keywords {
			if strings.EqualFold(kw, w) {
				score += 2.0
			}
		}
	}

	return score
}
