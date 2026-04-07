// Package rag provides chromem-go backed vector store implementation.
//
// ChromemStore wraps the chromem-go embedded vector database, implementing
// the domain VectorStore interface. It provides:
//   - Automatic persistence (gob-encoded, optional gzip compression)
//   - Built-in embedding via OpenAI-compatible APIs (DashScope/Qwen, Ollama, etc.)
//   - Thread-safe concurrent document add/query
//   - Zero external service dependency (embedded in-process)
package rag

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	chromem "github.com/philippgille/chromem-go"

	drag "github.com/SOULOFCINDERS/agent/internal/domain/rag"
)

const (
	defaultCollectionName = "rag_documents"
)

// ChromemStore 基于 chromem-go 的向量存储
// 实现 domain/rag.VectorStore 接口
type ChromemStore struct {
	mu         sync.RWMutex
	db         *chromem.DB
	collection *chromem.Collection
	documents  map[string]Document // 文档元数据（chromem 不直接存这些）
	dbPath     string              // 持久化路径
}

// ChromemConfig chromem-go 存储配置
type ChromemConfig struct {
	DataDir       string              // 持久化目录
	EmbeddingFunc chromem.EmbeddingFunc // 嵌入函数（nil 则使用 OpenAI 默认）
	Compress      bool                // 是否 gzip 压缩持久化数据
}

// NewChromemStore 创建基于 chromem-go 的向量存储
func NewChromemStore(cfg ChromemConfig) (*ChromemStore, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("DataDir is required")
	}

	db, err := chromem.NewPersistentDB(cfg.DataDir, cfg.Compress)
	if err != nil {
		return nil, fmt.Errorf("create chromem db: %w", err)
	}

	embFunc := cfg.EmbeddingFunc
	if embFunc == nil {
		embFunc = chromem.NewEmbeddingFuncDefault()
	}

	collection, err := db.GetOrCreateCollection(defaultCollectionName, nil, embFunc)
	if err != nil {
		return nil, fmt.Errorf("get/create collection: %w", err)
	}

	store := &ChromemStore{
		db:         db,
		collection: collection,
		documents:  make(map[string]Document),
		dbPath:     cfg.DataDir,
	}

	// 从 collection 现有文档重建 documents 元数据
	store.rebuildDocMetadata()

	return store, nil
}

// AddDocument 添加文档及其 chunks 到索引
func (s *ChromemStore) AddDocument(ctx context.Context, doc Document, chunks []drag.Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 先删除旧版本（如果存在）
	if _, exists := s.documents[doc.ID]; exists {
		s.deleteDocLocked(ctx, doc.ID)
	}

	// 构建 chromem Documents
	chromemDocs := make([]chromem.Document, 0, len(chunks))
	for _, chunk := range chunks {
		chromemDocs = append(chromemDocs, chromem.Document{
			ID:      chunk.ID,
			Content: chunk.Content,
			Metadata: map[string]string{
				"doc_id":  chunk.DocID,
				"source":  chunk.Source,
				"index":   fmt.Sprintf("%d", chunk.Index),
			},
		})
	}

	// 并发添加文档（chromem-go 内置支持）
	concurrency := runtime.NumCPU()
	if concurrency > len(chromemDocs) {
		concurrency = len(chromemDocs)
	}
	if concurrency < 1 {
		concurrency = 1
	}

	if err := s.collection.AddDocuments(ctx, chromemDocs, concurrency); err != nil {
		return fmt.Errorf("add documents to chromem: %w", err)
	}

	// 保存文档元数据
	s.documents[doc.ID] = doc

	return nil
}

// Query 根据查询文本检索最相似的 chunks
// 注意：chromem-go 的 Query 方法直接接受文本，内部自动嵌入
func (s *ChromemStore) Query(ctx context.Context, queryText string, topK int) ([]QueryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.collection.Count() == 0 {
		return nil, nil
	}
	if topK <= 0 {
		topK = 5
	}

	nResults := topK
	if nResults > s.collection.Count() {
		nResults = s.collection.Count()
	}

	results, err := s.collection.Query(ctx, queryText, nResults, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("chromem query: %w", err)
	}

	queryResults := make([]QueryResult, 0, len(results))
	for _, r := range results {
		docID := r.Metadata["doc_id"]
		docTitle := ""
		if doc, ok := s.documents[docID]; ok {
			docTitle = doc.Title
		}

		queryResults = append(queryResults, QueryResult{
			Chunk: drag.Chunk{
				ID:      r.ID,
				DocID:   docID,
				Source:  r.Metadata["source"],
				Content: r.Content,
			},
			Score:    float64(r.Similarity),
			DocTitle: docTitle,
		})
	}

	return queryResults, nil
}

// QueryByEmbedding 根据向量检索（兼容 domain 接口）
func (s *ChromemStore) QueryByEmbedding(ctx context.Context, embedding []float64, topK int) ([]QueryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.collection.Count() == 0 {
		return nil, nil
	}
	if topK <= 0 {
		topK = 5
	}

	nResults := topK
	if nResults > s.collection.Count() {
		nResults = s.collection.Count()
	}

	// 转换 float64 → float32
	emb32 := make([]float32, len(embedding))
	for i, v := range embedding {
		emb32[i] = float32(v)
	}

	results, err := s.collection.QueryEmbedding(ctx, emb32, nResults, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("chromem query embedding: %w", err)
	}

	queryResults := make([]QueryResult, 0, len(results))
	for _, r := range results {
		docID := r.Metadata["doc_id"]
		docTitle := ""
		if doc, ok := s.documents[docID]; ok {
			docTitle = doc.Title
		}

		queryResults = append(queryResults, QueryResult{
			Chunk: drag.Chunk{
				ID:      r.ID,
				DocID:   docID,
				Source:  r.Metadata["source"],
				Content: r.Content,
			},
			Score:    float64(r.Similarity),
			DocTitle: docTitle,
		})
	}

	return queryResults, nil
}

// DeleteDocument 删除指定文档的所有数据
func (s *ChromemStore) DeleteDocument(ctx context.Context, docID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteDocLocked(ctx, docID)
	return nil
}

func (s *ChromemStore) deleteDocLocked(ctx context.Context, docID string) {
	// 删除 chromem 中该文档的所有 chunks
	_ = s.collection.Delete(ctx, map[string]string{"doc_id": docID}, nil)
	delete(s.documents, docID)
}

// ListDocuments 列出所有已索引文档
func (s *ChromemStore) ListDocuments(_ context.Context) ([]Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	docs := make([]Document, 0, len(s.documents))
	for _, doc := range s.documents {
		docs = append(docs, doc)
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].IndexedAt.After(docs[j].IndexedAt)
	})
	return docs, nil
}

// Stats 返回索引统计
func (s *ChromemStore) Stats(_ context.Context) IndexStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	totalChars := 0
	for _, doc := range s.documents {
		totalChars += doc.TotalChars
	}
	return IndexStats{
		DocumentCount: len(s.documents),
		ChunkCount:    s.collection.Count(),
		TotalChars:    totalChars,
	}
}

// Save 持久化（chromem-go PersistentDB 已自动持久化，此为空操作）
func (s *ChromemStore) Save() error {
	return nil // chromem-go PersistentDB 在 Add 时已自动持久化
}

// Load 加载（chromem-go PersistentDB 在 NewPersistentDB 时已自动加载）
func (s *ChromemStore) Load() error {
	return nil
}

// rebuildDocMetadata 从已持久化的 collection 重建文档元数据
func (s *ChromemStore) rebuildDocMetadata() {
	// chromem-go 不提供遍历所有文档的公开 API，
	// 所以文档元数据在重启后需要通过其他方式恢复
	// 这里作为基本实现，重启后 documents map 为空
	// 实际使用中可以通过 Query("", maxInt) 或外部元数据文件来恢复
	// TODO: 添加独立的元数据持久化
	_ = time.Now() // placeholder
}
