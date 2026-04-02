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
	"unicode/utf8"
)

// Entry 是一条记忆
type Entry struct {
	ID        string    `json:"id"`
	Topic     string    `json:"topic"`               // 主题/标签
	Content   string    `json:"content"`              // 记忆内容
	Keywords  []string  `json:"keywords,omitempty"`   // 用于检索的关键词
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	AccessCnt int       `json:"access_cnt"`           // 访问次数（用于排序）
}

// Store 是记忆的持久化存储
type Store struct {
	mu       sync.RWMutex
	entries  []Entry
	filePath string
	nextID   int
}

// NewStore 创建/加载一个记忆存储
// dataDir 为存储目录，会在其中创建 memory.json
func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create memory dir: %w", err)
	}

	fp := filepath.Join(dataDir, "memory.json")
	s := &Store{
		filePath: fp,
		nextID:   1,
	}

	// 尝试加载已有数据
	data, err := os.ReadFile(fp)
	if err == nil && len(data) > 0 {
		var entries []Entry
		if err := json.Unmarshal(data, &entries); err == nil {
			s.entries = entries
			for _, e := range entries {
				// 解析 ID 数字部分，确保 nextID 不冲突
				var n int
				if _, err := fmt.Sscanf(e.ID, "mem_%d", &n); err == nil && n >= s.nextID {
					s.nextID = n + 1
				}
			}
		}
	}

	return s, nil
}

// Add 添加一条记忆
func (s *Store) Add(topic, content string, keywords []string) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// 检查是否有相同 topic 的记忆，如果有则更新
	for i, e := range s.entries {
		if strings.EqualFold(e.Topic, topic) {
			s.entries[i].Content = content
			s.entries[i].UpdatedAt = now
			if len(keywords) > 0 {
				s.entries[i].Keywords = keywords
			}
			s.save()
			return s.entries[i]
		}
	}

	entry := Entry{
		ID:        fmt.Sprintf("mem_%d", s.nextID),
		Topic:     topic,
		Content:   content,
		Keywords:  keywords,
		CreatedAt: now,
		UpdatedAt: now,
		AccessCnt: 0,
	}
	s.nextID++
	s.entries = append(s.entries, entry)
	s.save()
	return entry
}

// Search 搜索记忆，返回按相关度排序的结果
func (s *Store) Search(query string, limit int) []Entry {
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

	// 更新访问计数
	out := make([]Entry, len(results))
	for i, r := range results {
		out[i] = r.entry
		s.touchEntry(r.entry.ID)
	}
	return out
}

// List 返回所有记忆，按更新时间倒序
func (s *Store) List(limit int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.entries) {
		limit = len(s.entries)
	}

	// 按更新时间倒序
	sorted := make([]Entry, len(s.entries))
	copy(sorted, s.entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UpdatedAt.After(sorted[j].UpdatedAt)
	})

	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted
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

// Count 返回记忆总数
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Summary 生成记忆摘要，用于注入 system prompt
func (s *Store) Summary(maxEntries int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.entries) == 0 {
		return ""
	}

	if maxEntries <= 0 {
		maxEntries = 20
	}

	// 按 访问次数 + 最近更新 综合排序
	sorted := make([]Entry, len(s.entries))
	copy(sorted, s.entries)
	now := time.Now()
	sort.Slice(sorted, func(i, j int) bool {
		// 最近更新的 + 访问多的排前面
		si := float64(sorted[i].AccessCnt) + 10.0/math.Max(1, now.Sub(sorted[i].UpdatedAt).Hours()+1)
		sj := float64(sorted[j].AccessCnt) + 10.0/math.Max(1, now.Sub(sorted[j].UpdatedAt).Hours()+1)
		return si > sj
	})

	if len(sorted) > maxEntries {
		sorted = sorted[:maxEntries]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("你有 %d 条已保存的记忆：\n", len(s.entries)))
	for _, e := range sorted {
		b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", e.Topic, e.ID, e.Content))
	}
	return b.String()
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
	// 注意：调用者应已持有至少 RLock，这里需要升级为写锁
	// 简化处理：异步更新
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
	// 按空格和标点分词
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r >= 0x4e00)
	}) {
		if len(w) > 0 {
			words = append(words, w)
		}
	}
	// 对中文，按单字拆分（简单 unigram/bigram）
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

	// 完整查询匹配
	if strings.Contains(allText, rawQuery) {
		score += 5.0
	}

	// 逐词匹配
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

// RelevantSummary 根据当前查询返回相关记忆的摘要
// 与 Summary 不同，它基于 query 相关性检索，只注入真正相关的记忆
// 这样可以大幅减少 token 消耗
func (s *Store) RelevantSummary(query string, maxEntries int) string {
	if maxEntries <= 0 {
		maxEntries = 5
	}

	// 使用已有的 Search 方法进行相关性检索
	results := s.Search(query, maxEntries)
	if len(results) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("与当前对话相关的记忆（共 %d/%d 条）：\n", len(results), s.Count()))
	for _, e := range results {
		b.WriteString(fmt.Sprintf("- [%s] %s\n", e.Topic, e.Content))
	}
	return b.String()
}
