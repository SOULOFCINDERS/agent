// Package rag provides infrastructure implementation for Retrieval-Augmented Generation.
//
// 核心流程:
//   Index: 文件 → Chunker.Split() → Store.AddDocument() (chromem 自动嵌入)
//   Query: 用户问题 → Store.Query() → top-K chunks → 注入 prompt
//
// 支持两种后端:
//   - chromem-go: 生产推荐，内置多种 Embedding 提供方，自动持久化
//   - 自研 MemoryVectorStore: 零依赖 fallback，TF-IDF 嵌入
package rag

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	drag "github.com/SOULOFCINDERS/agent/internal/domain/rag"
)

// Engine RAG 引擎，协调 chunker、store（和可选的 embedder）
type Engine struct {
	chunker  drag.Chunker
	embedder drag.Embedder // 仅 legacy 模式使用

	// chromem-go 后端
	chromemStore *ChromemStore

	// 自研后端（legacy fallback）
	legacyStore *MemoryVectorStore

	// 配置
	chunkSize  int
	overlap    int
	useChromem bool
}

// EngineConfig RAG 引擎配置
type EngineConfig struct {
	DataDir   string // 索引持久化目录
	ChunkSize int    // chunk 目标大小（字符数，默认 500）
	Overlap   int    // chunk 重叠大小（字符数，默认 50）

	// Chromem-go 模式配置（优先使用）
	UseChromem    bool           // 是否使用 chromem-go 后端
	ChromemConfig *ChromemConfig // chromem-go 配置（nil 则自动构建）

	// Legacy 模式配置（UseChromem=false 时使用）
	Embedder drag.Embedder // 嵌入器（nil 则使用 TF-IDF）
}

// NewEngine 创建 RAG 引擎
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("DataDir is required")
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 500
	}
	if cfg.Overlap <= 0 {
		cfg.Overlap = 50
	}

	engine := &Engine{
		chunker:    NewTextChunker(),
		chunkSize:  cfg.ChunkSize,
		overlap:    cfg.Overlap,
		useChromem: cfg.UseChromem,
	}

	if cfg.UseChromem {
		// chromem-go 后端
		chromemCfg := cfg.ChromemConfig
		if chromemCfg == nil {
			chromemCfg = &ChromemConfig{
				DataDir:  cfg.DataDir,
				Compress: true,
			}
		}
		if chromemCfg.DataDir == "" {
			chromemCfg.DataDir = cfg.DataDir
		}

		store, err := NewChromemStore(*chromemCfg)
		if err != nil {
			return nil, fmt.Errorf("create chromem store: %w", err)
		}
		engine.chromemStore = store
	} else {
		// Legacy 自研后端
		store, err := NewMemoryVectorStore(cfg.DataDir)
		if err != nil {
			return nil, fmt.Errorf("create vector store: %w", err)
		}
		engine.legacyStore = store

		embedder := cfg.Embedder
		if embedder == nil {
			embedder = NewTFIDFEmbedder(512)
		}
		engine.embedder = embedder
	}

	return engine, nil
}

// IndexFile 索引一个本地文件
func (e *Engine) IndexFile(ctx context.Context, filePath string) (*Document, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	content := string(data)

	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("file is empty: %s", absPath)
	}

	title := filepath.Base(absPath)
	docID := makeDocID(absPath)

	return e.indexContent(ctx, docID, absPath, title, content)
}

// IndexText 索引一段文本（指定来源和标题）
func (e *Engine) IndexText(ctx context.Context, source, title, content string) (*Document, error) {
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("content is empty")
	}

	docID := makeDocID(source)
	return e.indexContent(ctx, docID, source, title, content)
}

func (e *Engine) indexContent(ctx context.Context, docID, source, title, content string) (*Document, error) {
	// 1. 切分
	chunkTexts := e.chunker.Split(content, e.chunkSize, e.overlap)
	if len(chunkTexts) == 0 {
		return nil, fmt.Errorf("no chunks generated from content")
	}

	// 2. 构建 Document 元数据
	doc := Document{
		ID:         docID,
		Source:     source,
		Title:      title,
		IndexedAt:  time.Now(),
		ChunkCount: len(chunkTexts),
		TotalChars: utf8.RuneCountInString(content),
	}

	// 3. 构建 Chunk 对象
	chunks := make([]Chunk, len(chunkTexts))
	charPos := 0
	for i, text := range chunkTexts {
		chunks[i] = Chunk{
			ID:        fmt.Sprintf("%s_chunk_%d", docID, i),
			DocID:     docID,
			Source:    source,
			Content:   text,
			Index:     i,
			StartChar: charPos,
		}
		charPos += utf8.RuneCountInString(text)
	}

	if e.useChromem {
		// chromem-go 模式：embedding 由 chromem 内部处理
		if err := e.chromemStore.AddDocument(ctx, doc, chunks); err != nil {
			return nil, fmt.Errorf("store document (chromem): %w", err)
		}
	} else {
		// Legacy 模式：手动嵌入
		embeddings, err := e.embedder.EmbedBatch(ctx, chunkTexts)
		if err != nil {
			return nil, fmt.Errorf("embed chunks: %w", err)
		}
		for i := range chunks {
			chunks[i].Embedding = embeddings[i]
		}
		if err := e.legacyStore.AddDocument(ctx, doc, chunks); err != nil {
			return nil, fmt.Errorf("store document: %w", err)
		}
		// 如果是 TF-IDF embedder，更新 IDF
		if tfidf, ok := e.embedder.(*TFIDFEmbedder); ok {
			var allTexts []string
			for _, c := range e.legacyStore.chunks {
				allTexts = append(allTexts, c.Content)
			}
			tfidf.UpdateIDF(allTexts)
		}
	}

	return &doc, nil
}

// Query 检索与查询最相关的文档片段
func (e *Engine) Query(ctx context.Context, query string, topK int) ([]QueryResult, error) {
	if topK <= 0 {
		topK = 5
	}

	if e.useChromem {
		// chromem-go 模式：直接传文本，chromem 内部嵌入+查询
		return e.chromemStore.Query(ctx, query, topK)
	}

	// Legacy 模式：手动嵌入后查询
	queryVec, err := e.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	results, err := e.legacyStore.Query(ctx, queryVec, topK)
	if err != nil {
		return nil, fmt.Errorf("query store: %w", err)
	}

	return results, nil
}

// FormatResults 将检索结果格式化为可注入 prompt 的文本
func FormatResults(results []QueryResult) string {
	if len(results) == 0 {
		return "未找到相关文档片段。"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 个相关片段：\n\n", len(results)))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("--- [片段 %d] 来源: %s (相似度: %.2f) ---\n",
			i+1, r.Chunk.Source, r.Score))
		sb.WriteString(r.Chunk.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// DeleteDocument 删除文档索引
func (e *Engine) DeleteDocument(ctx context.Context, docID string) error {
	if e.useChromem {
		return e.chromemStore.DeleteDocument(ctx, docID)
	}
	return e.legacyStore.DeleteDocument(ctx, docID)
}

// ListDocuments 列出已索引文档
func (e *Engine) ListDocuments(ctx context.Context) ([]Document, error) {
	if e.useChromem {
		return e.chromemStore.ListDocuments(ctx)
	}
	return e.legacyStore.ListDocuments(ctx)
}

// Stats 返回索引统计
func (e *Engine) Stats(ctx context.Context) IndexStats {
	if e.useChromem {
		return e.chromemStore.Stats(ctx)
	}
	return e.legacyStore.Stats(ctx)
}

// Backend 返回当前使用的后端名称
func (e *Engine) Backend() string {
	if e.useChromem {
		return "chromem-go"
	}
	return "memory (legacy)"
}

// makeDocID 根据来源生成稳定的文档 ID
func makeDocID(source string) string {
	h := sha256.Sum256([]byte(source))
	return fmt.Sprintf("doc_%x", h[:8])
}
