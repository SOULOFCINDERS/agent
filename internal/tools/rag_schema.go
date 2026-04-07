package tools

import "encoding/json"

// RAGToolSchemas 返回 RAG 工具的 schema 定义
func RAGToolSchemas() map[string]struct {
	Desc   string
	Schema json.RawMessage
} {
	return map[string]struct {
		Desc   string
		Schema json.RawMessage
	}{
		"rag_index": {
			Desc: "将文件或文本内容建立 RAG 向量索引。支持两种方式：1) 传入 file 参数索引本地文件；2) 传入 content + title 参数索引文本内容。索引后可通过 rag_query 检索相关片段。",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"file": {
						"type": "string",
						"description": "要索引的本地文件路径（相对于工作目录或绝对路径）"
					},
					"content": {
						"type": "string",
						"description": "要索引的文本内容（与 file 二选一）"
					},
					"title": {
						"type": "string",
						"description": "文档标题（索引文本时使用）"
					},
					"source": {
						"type": "string",
						"description": "文档来源标识（可选，用于追踪来源）"
					}
				}
			}`),
		},
		"rag_query": {
			Desc: "从 RAG 索引中检索与查询最相关的文档片段。返回 top-K 个最相似的文本片段及其来源信息。当用户询问已索引文档的内容时使用此工具。",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {
						"type": "string",
						"description": "检索查询（自然语言问题或关键词）"
					},
					"top_k": {
						"type": "integer",
						"description": "返回最相关的片段数量（默认 5，最大 20）"
					}
				},
				"required": ["query"]
			}`),
		},
		"rag_list": {
			Desc: "列出所有已建立 RAG 索引的文档，包括文档标题、来源、片段数、索引时间等信息。",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
		},
		"rag_delete": {
			Desc: "从 RAG 索引中删除指定文档。需要提供文档 ID（可通过 rag_list 查看）。",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"doc_id": {
						"type": "string",
						"description": "要删除的文档 ID（通过 rag_list 获取）"
					}
				},
				"required": ["doc_id"]
			}`),
		},
	}
}
