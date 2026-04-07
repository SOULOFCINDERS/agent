package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/rag"
)

// ---- RAG Index Tool ----

// RAGIndexTool 将文件或文本建立 RAG 索引
type RAGIndexTool struct {
	engine *rag.Engine
}

func NewRAGIndexTool(engine *rag.Engine) *RAGIndexTool {
	return &RAGIndexTool{engine: engine}
}

func (t *RAGIndexTool) Name() string { return "rag_index" }

func (t *RAGIndexTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	// 支持索引文件或直接文本
	filePath := ""
	if v, ok := args["file"]; ok {
		filePath = fmt.Sprint(v)
	} else if v, ok := args["path"]; ok {
		filePath = fmt.Sprint(v)
	}

	if filePath != "" {
		doc, err := t.engine.IndexFile(ctx, filePath)
		if err != nil {
			return nil, fmt.Errorf("index file failed: %w", err)
		}
		stats := t.engine.Stats(ctx)
		return fmt.Sprintf("✅ 已索引文件: %s\n  文档ID: %s\n  切分为 %d 个片段 (%d 字符)\n  当前索引: %d 文档, %d 片段",
			doc.Source, doc.ID, doc.ChunkCount, doc.TotalChars,
			stats.DocumentCount, stats.ChunkCount), nil
	}

	// 索引文本内容
	content := ""
	if v, ok := args["content"]; ok {
		content = fmt.Sprint(v)
	} else if v, ok := args["text"]; ok {
		content = fmt.Sprint(v)
	}
	title := "untitled"
	if v, ok := args["title"]; ok {
		title = fmt.Sprint(v)
	}
	source := "user_input"
	if v, ok := args["source"]; ok {
		source = fmt.Sprint(v)
	}

	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("请提供 file（文件路径）或 content（文本内容）参数")
	}

	doc, err := t.engine.IndexText(ctx, source, title, content)
	if err != nil {
		return nil, fmt.Errorf("index text failed: %w", err)
	}
	stats := t.engine.Stats(ctx)
	return fmt.Sprintf("✅ 已索引文本: %s\n  文档ID: %s\n  切分为 %d 个片段 (%d 字符)\n  当前索引: %d 文档, %d 片段",
		doc.Title, doc.ID, doc.ChunkCount, doc.TotalChars,
		stats.DocumentCount, stats.ChunkCount), nil
}

// ---- RAG Query Tool ----

// RAGQueryTool 从 RAG 索引中检索相关内容
type RAGQueryTool struct {
	engine *rag.Engine
}

func NewRAGQueryTool(engine *rag.Engine) *RAGQueryTool {
	return &RAGQueryTool{engine: engine}
}

func (t *RAGQueryTool) Name() string { return "rag_query" }

func (t *RAGQueryTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	query := ""
	if v, ok := args["query"]; ok {
		query = fmt.Sprint(v)
	} else if v, ok := args["question"]; ok {
		query = fmt.Sprint(v)
	} else if v, ok := args["input"]; ok {
		query = fmt.Sprint(v)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("请提供 query 参数")
	}

	topK := 5
	if v, ok := args["top_k"]; ok {
		switch n := v.(type) {
		case float64:
			topK = int(n)
		case int:
			topK = n
		}
	}
	if topK <= 0 || topK > 20 {
		topK = 5
	}

	results, err := t.engine.Query(ctx, query, topK)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	return rag.FormatResults(results), nil
}

// ---- RAG List Tool ----

// RAGListTool 列出所有已索引的文档
type RAGListTool struct {
	engine *rag.Engine
}

func NewRAGListTool(engine *rag.Engine) *RAGListTool {
	return &RAGListTool{engine: engine}
}

func (t *RAGListTool) Name() string { return "rag_list" }

func (t *RAGListTool) Execute(ctx context.Context, _ map[string]any) (any, error) {
	docs, err := t.engine.ListDocuments(ctx)
	if err != nil {
		return nil, fmt.Errorf("list documents failed: %w", err)
	}

	if len(docs) == 0 {
		return "📭 RAG 索引为空，还没有索引任何文档。\n使用 rag_index 工具来索引文件或文本。", nil
	}

	stats := t.engine.Stats(ctx)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📚 RAG 索引统计: %d 文档, %d 片段, %d 字符\n\n",
		stats.DocumentCount, stats.ChunkCount, stats.TotalChars))

	for i, doc := range docs {
		sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, doc.Title))
		sb.WriteString(fmt.Sprintf("    来源: %s\n", doc.Source))
		sb.WriteString(fmt.Sprintf("    片段数: %d | 字符数: %d | 索引时间: %s\n",
			doc.ChunkCount, doc.TotalChars, doc.IndexedAt.Format("2006-01-02 15:04")))
		sb.WriteString(fmt.Sprintf("    ID: %s\n\n", doc.ID))
	}

	return sb.String(), nil
}

// ---- RAG Delete Tool ----

// RAGDeleteTool 从 RAG 索引中删除文档
type RAGDeleteTool struct {
	engine *rag.Engine
}

func NewRAGDeleteTool(engine *rag.Engine) *RAGDeleteTool {
	return &RAGDeleteTool{engine: engine}
}

func (t *RAGDeleteTool) Name() string { return "rag_delete" }

func (t *RAGDeleteTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	docID := ""
	if v, ok := args["doc_id"]; ok {
		docID = fmt.Sprint(v)
	} else if v, ok := args["id"]; ok {
		docID = fmt.Sprint(v)
	}
	docID = strings.TrimSpace(docID)
	if docID == "" {
		return nil, fmt.Errorf("请提供 doc_id 参数（可通过 rag_list 查看文档 ID）")
	}

	if err := t.engine.DeleteDocument(ctx, docID); err != nil {
		return nil, fmt.Errorf("delete failed: %w", err)
	}

	stats := t.engine.Stats(ctx)
	return fmt.Sprintf("🗑️ 已删除文档: %s\n当前索引: %d 文档, %d 片段",
		docID, stats.DocumentCount, stats.ChunkCount), nil
}

// ---- RAG Import Tool ----

// RAGImportTool 批量导入知识库目录
type RAGImportTool struct {
	engine *rag.Engine
}

func NewRAGImportTool(engine *rag.Engine) *RAGImportTool {
	return &RAGImportTool{engine: engine}
}

func (t *RAGImportTool) Name() string { return "rag_import" }

func (t *RAGImportTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	// 获取路径参数
	path := ""
	if v, ok := args["path"]; ok {
		path = fmt.Sprint(v)
	} else if v, ok := args["dir"]; ok {
		path = fmt.Sprint(v)
	} else if v, ok := args["directory"]; ok {
		path = fmt.Sprint(v)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("请提供 path 参数（目录路径或 glob 模式，如 ./docs 或 ./data/*.md）")
	}

	// 构建导入选项
	opts := rag.DefaultImportOptions()

	// recursive 参数
	if v, ok := args["recursive"]; ok {
		switch b := v.(type) {
		case bool:
			opts.Recursive = b
		case string:
			opts.Recursive = b != "false" && b != "0"
		}
	}

	// glob 参数
	if v, ok := args["glob"]; ok {
		opts.GlobPattern = fmt.Sprint(v)
	}

	// max_file_size 参数（MB）
	if v, ok := args["max_file_size"]; ok {
		switch n := v.(type) {
		case float64:
			opts.MaxFileSize = int64(n) * 1024 * 1024
		case int:
			opts.MaxFileSize = int64(n) * 1024 * 1024
		}
	}

	// extensions 参数
	if v, ok := args["extensions"]; ok {
		if extStr, ok := v.(string); ok {
			exts := make(map[string]bool)
			for _, ext := range strings.Split(extStr, ",") {
				ext = strings.TrimSpace(ext)
				if !strings.HasPrefix(ext, ".") {
					ext = "." + ext
				}
				exts[strings.ToLower(ext)] = true
			}
			opts.Extensions = exts
		}
	}

	// 判断是 glob 模式还是目录
	var result *rag.ImportResult
	var err error

	// 检查是否包含 glob 通配符
	if strings.ContainsAny(path, "*?[") {
		result, err = t.engine.IndexGlob(ctx, path, opts)
	} else {
		result, err = t.engine.IndexDirectory(ctx, path, opts)
	}
	if err != nil {
		return nil, fmt.Errorf("import failed: %w", err)
	}

	// 格式化结果
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📦 知识库导入完成\n"))
	sb.WriteString(fmt.Sprintf("  扫描: %d 个文件\n", result.Total))
	sb.WriteString(fmt.Sprintf("  ✅ 成功索引: %d\n", result.Indexed))
	if result.Skipped > 0 {
		sb.WriteString(fmt.Sprintf("  ⏭️  跳过: %d (格式不支持/已存在/过大)\n", result.Skipped))
	}
	if result.Failed > 0 {
		sb.WriteString(fmt.Sprintf("  ❌ 失败: %d\n", result.Failed))
		for _, e := range result.Errors {
			if len(e) > 200 {
				e = e[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("    - %s\n", e))
		}
	}

	if len(result.Documents) > 0 {
		sb.WriteString(fmt.Sprintf("\n已索引文档:\n"))
		for i, doc := range result.Documents {
			if i >= 20 {
				sb.WriteString(fmt.Sprintf("  ... 及其他 %d 个文档\n", len(result.Documents)-20))
				break
			}
			sb.WriteString(fmt.Sprintf("  [%d] %s (%d 片段)\n", i+1, doc.Title, doc.ChunkCount))
		}
	}

	stats := t.engine.Stats(ctx)
	sb.WriteString(fmt.Sprintf("\n📚 当前索引总计: %d 文档, %d 片段, %d 字符\n",
		stats.DocumentCount, stats.ChunkCount, stats.TotalChars))

	return sb.String(), nil
}
