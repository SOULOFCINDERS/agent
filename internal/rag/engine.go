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

// ---- 知识库批量导入 ----

// ImportResult 批量导入的结果
type ImportResult struct {
	Total     int      // 扫描到的文件总数
	Indexed   int      // 成功索引的文件数
	Skipped   int      // 跳过的文件数（格式不支持/已存在等）
	Failed    int      // 失败的文件数
	Errors    []string // 错误详情
	Documents []Document // 成功索引的文档列表
}

// SupportedExtensions 默认支持索引的文件扩展名
var SupportedExtensions = map[string]bool{
	".txt":  true,
	".md":   true,
	".go":   true,
	".py":   true,
	".js":   true,
	".ts":   true,
	".java": true,
	".c":    true,
	".cpp":  true,
	".h":    true,
	".rs":   true,
	".rb":   true,
	".php":  true,
	".sh":   true,
	".bash": true,
	".zsh":  true,
	".yaml": true,
	".yml":  true,
	".json": true,
	".toml": true,
	".xml":  true,
	".html": true,
	".css":  true,
	".sql":  true,
	".r":    true,
	".lua":  true,
	".swift":true,
	".kt":   true,
	".scala":true,
	".csv":  true,
	".tsv":  true,
	".log":  true,
	".conf": true,
	".cfg":  true,
	".ini":  true,
	".env":  true,
	".dockerfile": true,
	".makefile":    true,
	".proto":       true,
	".graphql":     true,
	".tex":  true,
	".rst":  true,
	".org":  true,
	".adoc": true,
}

// ImportOptions 批量导入配置
type ImportOptions struct {
	Extensions   map[string]bool // 允许的扩展名（nil=使用默认）
	MaxFileSize  int64           // 单文件最大字节数（0=默认 1MB）
	Recursive    bool            // 是否递归子目录
	GlobPattern  string          // glob 匹配模式（空=全部）
	SkipExisting bool            // 跳过已索引的文件
}

// DefaultImportOptions 返回默认导入配置
func DefaultImportOptions() ImportOptions {
	return ImportOptions{
		Extensions:   nil, // 使用 SupportedExtensions
		MaxFileSize:  1 * 1024 * 1024, // 1MB
		Recursive:    true,
		SkipExisting: true,
	}
}

// IndexDirectory 递归扫描目录，批量索引所有支持的文件
func (e *Engine) IndexDirectory(ctx context.Context, dirPath string, opts ImportOptions) (*ImportResult, error) {
	absDir, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("resolve dir: %w", err)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return nil, fmt.Errorf("stat dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", absDir)
	}

	if opts.MaxFileSize <= 0 {
		opts.MaxFileSize = 1 * 1024 * 1024
	}
	exts := opts.Extensions
	if exts == nil {
		exts = SupportedExtensions
	}

	// 获取已索引文档列表（用于去重）
	existingDocs := make(map[string]bool)
	if opts.SkipExisting {
		if docs, err := e.ListDocuments(ctx); err == nil {
			for _, doc := range docs {
				existingDocs[doc.Source] = true
			}
		}
	}

	result := &ImportResult{}

	// 遍历目录
	walkFn := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的路径
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// 跳过隐藏目录
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__" {
				return filepath.SkipDir
			}
			if !opts.Recursive && path != absDir {
				return filepath.SkipDir
			}
			return nil
		}

		result.Total++

		// 检查扩展名
		ext := strings.ToLower(filepath.Ext(path))
		// 特殊文件名检查
		baseName := strings.ToLower(filepath.Base(path))
		if !exts[ext] {
			// 检查无扩展名的特殊文件
			if baseName != "makefile" && baseName != "dockerfile" && baseName != "readme" {
				result.Skipped++
				return nil
			}
		}

		// Glob 模式匹配
		if opts.GlobPattern != "" {
			matched, _ := filepath.Match(opts.GlobPattern, filepath.Base(path))
			if !matched {
				result.Skipped++
				return nil
			}
		}

		// 检查文件大小
		finfo, err := d.Info()
		if err != nil {
			result.Skipped++
			return nil
		}
		if finfo.Size() > opts.MaxFileSize {
			result.Skipped++
			return nil
		}
		if finfo.Size() == 0 {
			result.Skipped++
			return nil
		}

		// 检查是否已索引
		if opts.SkipExisting && existingDocs[path] {
			result.Skipped++
			return nil
		}

		// 索引文件
		doc, err := e.IndexFile(ctx, path)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", path, err))
			return nil
		}

		result.Indexed++
		result.Documents = append(result.Documents, *doc)
		return nil
	}

	if err := filepath.WalkDir(absDir, walkFn); err != nil {
		return result, fmt.Errorf("walk dir: %w", err)
	}

	return result, nil
}

// IndexGlob 按 glob 模式匹配并索引文件
func (e *Engine) IndexGlob(ctx context.Context, pattern string, opts ImportOptions) (*ImportResult, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid glob pattern: %w", err)
	}

	result := &ImportResult{Total: len(matches)}

	// 获取已索引文档列表（用于去重）
	existingDocs := make(map[string]bool)
	if opts.SkipExisting {
		if docs, err := e.ListDocuments(ctx); err == nil {
			for _, doc := range docs {
				existingDocs[doc.Source] = true
			}
		}
	}

	for _, path := range matches {
		if ctx.Err() != nil {
			break
		}

		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			result.Skipped++
			continue
		}
		if info.Size() > opts.MaxFileSize || info.Size() == 0 {
			result.Skipped++
			continue
		}

		absPath, _ := filepath.Abs(path)
		if opts.SkipExisting && existingDocs[absPath] {
			result.Skipped++
			continue
		}

		doc, err := e.IndexFile(ctx, path)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", path, err))
			continue
		}

		result.Indexed++
		result.Documents = append(result.Documents, *doc)
	}

	return result, nil
}
