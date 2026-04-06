// Package memory defines the domain model for conversation memory entries.
package memory

import "time"

// ---- 值对象 ----

// Entry 一条记忆
type Entry struct {
	ID        string    `json:"id"`
	Topic     string    `json:"topic"`
	Content   string    `json:"content"`
	Keywords  []string  `json:"keywords,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	AccessCnt int       `json:"access_cnt"`
}

// ---- 接口 ----

// Store 记忆存储接口（Domain 层定义）
// 具体实现（JSON 文件、数据库等）在 infrastructure 层
type Store interface {
	Add(topic, content string, keywords []string) Entry
	Search(query string, limit int) []Entry
	List(limit int) []Entry
	Delete(id string) bool
	Count() int
	Summary(maxEntries int) string
	RelevantSummary(query string, maxEntries int) string
}
