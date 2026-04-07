// Package rag defines the domain model for Retrieval-Augmented Generation.
//
// RAG 允许 Agent 对本地文档建立索引，在对话时自动检索相关片段，
// 注入到上下文中以提升回答质量。
//
// 核心概念：
//   - Document: 一个被索引的文件/URL 来源
//   - Chunk: 文档被切分后的文本片段
//   - Embedding: Chunk 的向量表示
//   - VectorStore: 存储和检索向量的接口
package rag

import (
	"context"
	"time"
)

// ---- 值对象 ----

// Document 一个被索引的文档来源
type Document struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`    // 文件路径或 URL
	Title    string    `json:"title"`     // 文档标题
	IndexedAt time.Time `json:"indexed_at"`
	ChunkCount int     `json:"chunk_count"`
	TotalChars int     `json:"total_chars"`
}

// Chunk 文档被切分后的片段
type Chunk struct {
	ID         string    `json:"id"`
	DocID      string    `json:"doc_id"`       // 所属文档 ID
	Source     string    `json:"source"`        // 来源路径
	Content    string    `json:"content"`       // 文本内容
	Index      int       `json:"index"`         // 在文档中的序号
	StartChar  int       `json:"start_char"`    // 起始字符位置
	Embedding  []float64 `json:"embedding,omitempty"` // 向量表示
}

// QueryResult 检索结果
type QueryResult struct {
	Chunk      Chunk   `json:"chunk"`
	Score      float64 `json:"score"`       // 相似度得分 (0-1)
	DocTitle   string  `json:"doc_title"`
}

// IndexStats 索引统计
type IndexStats struct {
	DocumentCount int `json:"document_count"`
	ChunkCount    int `json:"chunk_count"`
	TotalChars    int `json:"total_chars"`
}

// ---- 接口 ----

// Embedder 文本向量化接口
// 实现可以是 LLM Embedding API、本地模型、或基于 TF-IDF 的轻量方案
type Embedder interface {
	// Embed 将文本转换为向量
	Embed(ctx context.Context, text string) ([]float64, error)

	// EmbedBatch 批量向量化
	EmbedBatch(ctx context.Context, texts []string) ([][]float64, error)

	// Dimension 返回向量维度
	Dimension() int
}

// Chunker 文档切分器接口
type Chunker interface {
	// Split 将文本切分为若干 Chunk
	// chunkSize: 每个 chunk 的目标 token 数
	// overlap: 相邻 chunk 之间重叠的 token 数
	Split(text string, chunkSize, overlap int) []string
}

// VectorStore 向量存储与检索接口
type VectorStore interface {
	// AddDocument 添加文档及其 chunks 到索引
	AddDocument(ctx context.Context, doc Document, chunks []Chunk) error

	// Query 根据向量检索最相似的 chunks
	Query(ctx context.Context, embedding []float64, topK int) ([]QueryResult, error)

	// DeleteDocument 按文档 ID 删除索引
	DeleteDocument(ctx context.Context, docID string) error

	// ListDocuments 列出所有已索引文档
	ListDocuments(ctx context.Context) ([]Document, error)

	// Stats 返回索引统计
	Stats(ctx context.Context) IndexStats

	// Save 持久化到磁盘
	Save() error

	// Load 从磁盘加载
	Load() error
}
