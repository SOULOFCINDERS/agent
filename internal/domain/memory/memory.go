// Package memory defines the domain model for conversation memory entries.
package memory

import "time"

// ---- 值对象 ----

// Entry 一条记忆
type Entry struct {
	ID           string    `json:"id"`
	Topic        string    `json:"topic"`
	Content      string    `json:"content"`
	Keywords     []string  `json:"keywords,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	AccessCnt    int       `json:"access_cnt"`
	Version      int       `json:"version"`                       // 版本号，覆盖更新时递增
	SupersededBy string    `json:"superseded_by,omitempty"`       // 被哪条记忆取代（软删除）
	Confidence   float64   `json:"confidence,omitempty"`          // 置信度 0~1
	Embedding    []float64 `json:"embedding,omitempty"`           // TF-IDF 向量缓存
}

// IsActive 记忆是否有效（未被取代）
func (e Entry) IsActive() bool {
	return e.SupersededBy == ""
}

// ---- 冲突检测结果 ----

// ConflictType 记忆冲突类型
type ConflictType string

const (
	ConflictNone        ConflictType = ""
	ConflictExplicit    ConflictType = "explicit_override" // P0: 显式否定覆盖
	ConflictSemantic    ConflictType = "semantic_conflict" // P1: 语义矛盾（自动裁决）
	ConflictNeedConfirm ConflictType = "need_confirm"      // P3: 需要用户确认
)

// ConflictResult 冲突检测结果
type ConflictResult struct {
	Type          ConflictType `json:"type"`
	ConflictingID string       `json:"conflicting_id,omitempty"` // 冲突的旧记忆 ID
	OldContent    string       `json:"old_content,omitempty"`
	NewContent    string       `json:"new_content,omitempty"`
	Similarity    float64      `json:"similarity,omitempty"` // 语义相似度
	AutoResolved  bool         `json:"auto_resolved"`        // 是否已自动处理
	Resolution    string       `json:"resolution,omitempty"` // 处理说明
}

// ---- 接口 ----

// AddResult Add 操作的返回值
type AddResult struct {
	Entry    Entry
	Conflict *ConflictResult // 非 nil 表示检测到冲突
}

// Store 记忆存储接口（Domain 层定义）
// 具体实现（JSON 文件、数据库等）在 infrastructure 层
type Store interface {
	Add(topic, content string, keywords []string) AddResult
	Search(query string, limit int) []Entry
	List(limit int) []Entry
	Delete(id string) bool
	Count() int
	Summary(maxEntries int) string
	RelevantSummary(query string, maxEntries int) string
}
