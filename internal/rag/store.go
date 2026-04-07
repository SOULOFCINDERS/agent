package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	drag "github.com/SOULOFCINDERS/agent/internal/domain/rag"
)

// ---- 类型别名 ----

type Document = drag.Document
type Chunk = drag.Chunk
type QueryResult = drag.QueryResult
type IndexStats = drag.IndexStats

// MemoryVectorStore 内存向量存储 + 文件持久化
// 适用于中小规模文档集（<10000 chunks）
type MemoryVectorStore struct {
	mu        sync.RWMutex
	documents map[string]Document // docID → Document
	chunks    []Chunk             // 所有 chunks（含 embedding）
	filePath  string              // 持久化路径
}

// storeData 持久化格式
type storeData struct {
	Documents map[string]Document `json:"documents"`
	Chunks    []Chunk             `json:"chunks"`
}

// NewMemoryVectorStore 创建内存向量存储
func NewMemoryVectorStore(dataDir string) (*MemoryVectorStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create rag dir: %w", err)
	}

	store := &MemoryVectorStore{
		documents: make(map[string]Document),
		filePath:  filepath.Join(dataDir, "rag_index.json"),
	}

	// 尝试加载已有索引
	_ = store.Load()

	return store, nil
}

// AddDocument 添加文档及其 chunks 到索引
func (s *MemoryVectorStore) AddDocument(_ context.Context, doc Document, chunks []Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 如果同一文档已存在，先删除旧数据
	if _, exists := s.documents[doc.ID]; exists {
		s.deleteDocLocked(doc.ID)
	}

	s.documents[doc.ID] = doc
	s.chunks = append(s.chunks, chunks...)

	return s.saveLocked()
}

// Query 根据查询向量检索最相似的 chunks
func (s *MemoryVectorStore) Query(_ context.Context, embedding []float64, topK int) ([]QueryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.chunks) == 0 {
		return nil, nil
	}
	if topK <= 0 {
		topK = 5
	}

	type scored struct {
		idx   int
		score float64
	}

	var scores []scored
	for i, chunk := range s.chunks {
		if len(chunk.Embedding) == 0 {
			continue
		}
		sim := CosineSimilarity(embedding, chunk.Embedding)
		scores = append(scores, scored{idx: i, score: sim})
	}

	// 按相似度降序排序
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	if topK > len(scores) {
		topK = len(scores)
	}

	results := make([]QueryResult, topK)
	for i := 0; i < topK; i++ {
		chunk := s.chunks[scores[i].idx]
		docTitle := ""
		if doc, ok := s.documents[chunk.DocID]; ok {
			docTitle = doc.Title
		}
		results[i] = QueryResult{
			Chunk:    chunk,
			Score:    scores[i].score,
			DocTitle: docTitle,
		}
	}

	return results, nil
}

// DeleteDocument 删除指定文档的所有数据
func (s *MemoryVectorStore) DeleteDocument(_ context.Context, docID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteDocLocked(docID)
	return s.saveLocked()
}

func (s *MemoryVectorStore) deleteDocLocked(docID string) {
	delete(s.documents, docID)

	// 过滤掉该文档的 chunks
	filtered := make([]Chunk, 0, len(s.chunks))
	for _, c := range s.chunks {
		if c.DocID != docID {
			filtered = append(filtered, c)
		}
	}
	s.chunks = filtered
}

// ListDocuments 列出所有已索引文档
func (s *MemoryVectorStore) ListDocuments(_ context.Context) ([]Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	docs := make([]Document, 0, len(s.documents))
	for _, doc := range s.documents {
		docs = append(docs, doc)
	}
	// 按索引时间排序
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].IndexedAt.After(docs[j].IndexedAt)
	})
	return docs, nil
}

// Stats 返回索引统计
func (s *MemoryVectorStore) Stats(_ context.Context) IndexStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	totalChars := 0
	for _, c := range s.chunks {
		totalChars += len(c.Content)
	}
	return IndexStats{
		DocumentCount: len(s.documents),
		ChunkCount:    len(s.chunks),
		TotalChars:    totalChars,
	}
}

// Save 持久化到磁盘
func (s *MemoryVectorStore) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveLocked()
}

func (s *MemoryVectorStore) saveLocked() error {
	data := storeData{
		Documents: s.documents,
		Chunks:    s.chunks,
	}
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	if err := os.WriteFile(s.filePath, b, 0644); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	return nil
}

// Load 从磁盘加载索引
func (s *MemoryVectorStore) Load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在不算错误
		}
		return fmt.Errorf("read index: %w", err)
	}

	var sd storeData
	if err := json.Unmarshal(data, &sd); err != nil {
		return fmt.Errorf("unmarshal index: %w", err)
	}

	s.documents = sd.Documents
	s.chunks = sd.Chunks
	if s.documents == nil {
		s.documents = make(map[string]Document)
	}

	return nil
}
