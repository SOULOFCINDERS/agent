package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// TextChunker 测试
// =============================================================================

func TestTextChunker_BasicSplit(t *testing.T) {
	chunker := NewTextChunker()

	text := "第一段内容。这是第一段。\n\n第二段内容。这是第二段。\n\n第三段内容。这是第三段。"
	chunks := chunker.Split(text, 20, 0)

	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk")
	}
	for i, c := range chunks {
		t.Logf("Chunk %d: %q", i, c)
	}
}

func TestTextChunker_ShortText(t *testing.T) {
	chunker := NewTextChunker()

	text := "短文本"
	chunks := chunker.Split(text, 500, 50)

	if len(chunks) != 1 {
		t.Errorf("short text should produce 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != "短文本" {
		t.Errorf("expected %q, got %q", "短文本", chunks[0])
	}
}

func TestTextChunker_EmptyText(t *testing.T) {
	chunker := NewTextChunker()
	chunks := chunker.Split("", 500, 50)
	if chunks != nil {
		t.Errorf("empty text should return nil, got %v", chunks)
	}
}

func TestTextChunker_Overlap(t *testing.T) {
	chunker := NewTextChunker()

	// 创建一段较长文本
	text := strings.Repeat("这是一段测试文本内容。", 20)
	chunks := chunker.Split(text, 50, 10)

	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks with overlap, got %d", len(chunks))
	}
	t.Logf("Generated %d chunks with overlap", len(chunks))
}

// =============================================================================
// TFIDFEmbedder 测试
// =============================================================================

func TestTFIDFEmbedder_Basic(t *testing.T) {
	embedder := NewTFIDFEmbedder(256)

	vec, err := embedder.Embed(context.Background(), "hello world test")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 256 {
		t.Errorf("expected dim 256, got %d", len(vec))
	}

	// 向量应该被归一化
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	if norm < 0.99 || norm > 1.01 {
		t.Errorf("vector should be normalized, got norm=%f", norm)
	}
}

func TestTFIDFEmbedder_SimilarTexts(t *testing.T) {
	embedder := NewTFIDFEmbedder(512)
	ctx := context.Background()

	v1, _ := embedder.Embed(ctx, "Go programming language concurrency")
	v2, _ := embedder.Embed(ctx, "Go language concurrent programming")
	v3, _ := embedder.Embed(ctx, "Python machine learning neural network")

	sim12 := CosineSimilarity(v1, v2)
	sim13 := CosineSimilarity(v1, v3)

	// 相似的文本应该有更高的相似度
	if sim12 <= sim13 {
		t.Errorf("similar texts should have higher similarity: sim(Go,Go)=%f <= sim(Go,Python)=%f", sim12, sim13)
	}
	t.Logf("sim(Go↔Go)=%.4f, sim(Go↔Python)=%.4f", sim12, sim13)
}

func TestTFIDFEmbedder_BatchEmbed(t *testing.T) {
	embedder := NewTFIDFEmbedder(128)
	ctx := context.Background()

	texts := []string{"first text", "second text", "third text"}
	vecs, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Errorf("expected 3 vectors, got %d", len(vecs))
	}
}

// =============================================================================
// MemoryVectorStore 测试
// =============================================================================

func TestMemoryVectorStore_AddAndQuery(t *testing.T) {
	dir := t.TempDir()
	store, err := NewMemoryVectorStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	embedder := NewTFIDFEmbedder(64)

	// 准备文档
	texts := []string{
		"Go is a statically typed compiled language",
		"Python is a dynamically typed interpreted language",
	}
	vecs, _ := embedder.EmbedBatch(ctx, texts)

	chunks := []Chunk{
		{ID: "c1", DocID: "doc1", Content: texts[0], Embedding: vecs[0]},
		{ID: "c2", DocID: "doc1", Content: texts[1], Embedding: vecs[1]},
	}
	doc := Document{ID: "doc1", Title: "Languages", ChunkCount: 2}
	if err := store.AddDocument(ctx, doc, chunks); err != nil {
		t.Fatal(err)
	}

	// 查询
	queryVec, _ := embedder.Embed(ctx, "Go compiled language")
	results, err := store.Query(ctx, queryVec, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// 第一个结果应该是 Go 相关的
	if !strings.Contains(results[0].Chunk.Content, "Go") {
		t.Errorf("expected Go-related chunk first, got: %s", results[0].Chunk.Content)
	}
	t.Logf("Best match (score=%.4f): %s", results[0].Score, results[0].Chunk.Content)
}

func TestMemoryVectorStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// 创建并写入
	store1, _ := NewMemoryVectorStore(dir)
	doc := Document{ID: "doc1", Title: "Test"}
	chunks := []Chunk{{ID: "c1", DocID: "doc1", Content: "test", Embedding: []float64{1, 0}}}
	store1.AddDocument(ctx, doc, chunks)

	// 重新加载
	store2, _ := NewMemoryVectorStore(dir)
	docs, _ := store2.ListDocuments(ctx)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc after reload, got %d", len(docs))
	}
	if docs[0].Title != "Test" {
		t.Errorf("expected title 'Test', got %q", docs[0].Title)
	}
}

func TestMemoryVectorStore_Delete(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	store, _ := NewMemoryVectorStore(dir)
	doc := Document{ID: "doc1", Title: "ToDelete"}
	chunks := []Chunk{{ID: "c1", DocID: "doc1", Content: "data"}}
	store.AddDocument(ctx, doc, chunks)

	stats := store.Stats(ctx)
	if stats.DocumentCount != 1 {
		t.Fatalf("expected 1 doc, got %d", stats.DocumentCount)
	}

	store.DeleteDocument(ctx, "doc1")
	stats = store.Stats(ctx)
	if stats.DocumentCount != 0 {
		t.Errorf("expected 0 docs after delete, got %d", stats.DocumentCount)
	}
}

// =============================================================================
// Engine 端到端测试
// =============================================================================

func TestEngine_IndexFileAndQuery(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// 创建测试文件
	testFile := filepath.Join(dir, "test.txt")
	content := `Go 语言教程

Go 语言是 Google 开发的一种静态类型编程语言。
Go 支持并发编程，通过 goroutine 和 channel 实现。

Python 教程

Python 是一种动态类型的解释型语言。
Python 广泛用于数据科学和机器学习领域。`

	os.WriteFile(testFile, []byte(content), 0644)

	engine, err := NewEngine(EngineConfig{
		DataDir:   filepath.Join(dir, "rag"),
		ChunkSize: 80,
		Overlap:   10,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 索引文件
	doc, err := engine.IndexFile(ctx, testFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Indexed: %s (%d chunks, %d chars)", doc.Title, doc.ChunkCount, doc.TotalChars)

	if doc.ChunkCount == 0 {
		t.Fatal("expected at least 1 chunk")
	}

	// 查询
	results, err := engine.Query(ctx, "Go 并发 goroutine", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	t.Logf("Top result (score=%.4f): %s", results[0].Score, results[0].Chunk.Content[:min(80, len(results[0].Chunk.Content))])

	// 统计
	stats := engine.Stats(ctx)
	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 doc, got %d", stats.DocumentCount)
	}
}

func TestEngine_IndexTextAndQuery(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	engine, err := NewEngine(EngineConfig{
		DataDir: filepath.Join(dir, "rag"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 索引多个文本
	engine.IndexText(ctx, "doc:go", "Go Guide", "Go is a compiled language with garbage collection and concurrency support.")
	engine.IndexText(ctx, "doc:rust", "Rust Guide", "Rust is a systems language with memory safety without garbage collection.")
	engine.IndexText(ctx, "doc:python", "Python Guide", "Python is an interpreted language popular in data science and AI.")

	stats := engine.Stats(ctx)
	if stats.DocumentCount != 3 {
		t.Fatalf("expected 3 docs, got %d", stats.DocumentCount)
	}

	// 查询应该匹配 Go
	results, _ := engine.Query(ctx, "compiled garbage collection", 2)
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	t.Logf("Query 'compiled gc' → best: %s (score=%.4f)", results[0].DocTitle, results[0].Score)
}

func TestEngine_FormatResults(t *testing.T) {
	results := []QueryResult{
		{Chunk: Chunk{Source: "test.go", Content: "package main"}, Score: 0.95, DocTitle: "Main"},
		{Chunk: Chunk{Source: "lib.go", Content: "func helper()"}, Score: 0.80, DocTitle: "Lib"},
	}

	formatted := FormatResults(results)
	if !strings.Contains(formatted, "片段 1") {
		t.Error("expected '片段 1' in formatted output")
	}
	if !strings.Contains(formatted, "test.go") {
		t.Error("expected source in formatted output")
	}
	t.Logf("Formatted:\n%s", formatted)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
