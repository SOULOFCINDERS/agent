// Package persist 提供基于 JSON 文件的会话持久化存储。
// 每个会话对应一个 JSON 文件: ~/.agent/sessions/<session_id>.json
package persist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	conv "github.com/SOULOFCINDERS/agent/internal/domain/conversation"
)

// SessionRecord 单个会话的持久化记录
type SessionRecord struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	History   []conv.Message `json:"history"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// SessionSummary 会话摘要（列表用，不含完整历史）
type SessionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	MsgCount  int    `json:"msg_count"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// Store JSON 文件持久化存储
type Store struct {
	mu      sync.RWMutex
	dir     string
	cache   map[string]*SessionRecord // 内存缓存
	loaded  bool
}

// NewStore 创建存储实例，dir 为存储目录（如 ~/.agent/sessions）
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}
	s := &Store{
		dir:   dir,
		cache: make(map[string]*SessionRecord),
	}
	if err := s.loadAll(); err != nil {
		return nil, fmt.Errorf("load sessions: %w", err)
	}
	return s, nil
}

// DefaultDir 返回默认存储目录 ~/.agent/sessions
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".agent", "sessions")
}

// ---------- 读操作 ----------

// GetHistory 获取会话历史（返回副本）
func (s *Store) GetHistory(id string) []conv.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec := s.cache[id]
	if rec == nil || len(rec.History) == 0 {
		return nil
	}
	cp := make([]conv.Message, len(rec.History))
	copy(cp, rec.History)
	return cp
}

// List 列出所有会话摘要，按更新时间倒序
func (s *Store) List() []SessionSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []SessionSummary
	for _, rec := range s.cache {
		if len(rec.History) == 0 {
			continue
		}
		userCount := 0
		for _, m := range rec.History {
			if m.Role == "user" {
				userCount++
			}
		}
		title := rec.Title
		if title == "" {
			title = "新对话"
		}
		items = append(items, SessionSummary{
			ID:        rec.ID,
			Title:     title,
			MsgCount:  userCount,
			CreatedAt: rec.CreatedAt.Unix(),
			UpdatedAt: rec.UpdatedAt.Unix(),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	return items
}

// Count 返回有效会话数量
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, rec := range s.cache {
		if len(rec.History) > 0 {
			n++
		}
	}
	return n
}

// ---------- 写操作 ----------

// SaveHistory 保存会话历史（自动生成标题、更新时间戳、写盘）
func (s *Store) SaveHistory(id string, history []conv.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec := s.cache[id]
	if rec == nil {
		rec = &SessionRecord{
			ID:        id,
			CreatedAt: time.Now(),
		}
		s.cache[id] = rec
	}

	rec.History = history
	rec.UpdatedAt = time.Now()

	// 自动标题：取第一条用户消息前 30 字
	if rec.Title == "" {
		for _, msg := range history {
			if msg.Role == "user" && msg.Content != "" {
				runes := []rune(msg.Content)
				if len(runes) > 30 {
					rec.Title = string(runes[:30]) + "..."
				} else {
					rec.Title = string(runes)
				}
				break
			}
		}
	}

	return s.writeToDisk(rec)
}

// Rename 重命名会话
func (s *Store) Rename(id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.cache[id]
	if rec == nil {
		return nil
	}
	rec.Title = title
	return s.writeToDisk(rec)
}

// ClearHistory 清空会话历史（保留记录）
func (s *Store) ClearHistory(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.cache[id]
	if rec == nil {
		return nil
	}
	rec.History = nil
	rec.UpdatedAt = time.Now()
	return s.writeToDisk(rec)
}

// Delete 永久删除会话（文件 + 缓存）
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cache, id)
	path := s.filePath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ---------- 内部方法 ----------

func (s *Store) filePath(id string) string {
	// 清理 ID 中的路径分隔符防止目录遍历
	safe := strings.ReplaceAll(id, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	safe = strings.ReplaceAll(safe, "..", "_")
	return filepath.Join(s.dir, safe+".json")
}

func (s *Store) writeToDisk(rec *SessionRecord) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session %s: %w", rec.ID, err)
	}
	path := s.filePath(rec.ID)
	// 原子写：先写临时文件再 rename
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write session %s: %w", rec.ID, err)
	}
	return os.Rename(tmp, path)
}

func (s *Store) loadAll() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue // 跳过读不了的文件
		}
		var rec SessionRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue // 跳过格式错误的文件
		}
		if rec.ID == "" {
			continue
		}
		s.cache[rec.ID] = &rec
	}
	s.loaded = true
	return nil
}
