package tools

import "encoding/json"

// MemoryToolSchemas 返回记忆工具的 function calling schema
func MemoryToolSchemas() map[string]struct {
	Desc   string
	Schema json.RawMessage
} {
	return map[string]struct {
		Desc   string
		Schema json.RawMessage
	}{
		"save_memory": {
			Desc: `保存一条记忆。当用户明确要求"记住"、"帮我记一下"、"下次记得"，或者表达了个人偏好时调用。不要对普通对话内容自动保存。

冲突处理：保存时会自动检测与已有记忆的冲突。返回结果中如果包含 conflict_type 字段，表示检测到冲突：
- explicit_override：已自动处理（显式否定或同主题更新），简要告知用户
- semantic_conflict：语义冲突已自动裁决，告知用户结果
- need_confirm：无法自动裁决，必须向用户确认是否删除旧的矛盾记忆（conflict_id）`,
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"topic": {
						"type": "string",
						"description": "记忆的主题分类，如：用户偏好、项目信息、工作习惯、联系人等"
					},
					"content": {
						"type": "string",
						"description": "要记住的具体内容，使用第三人称（如：用户喜欢...）"
					},
					"keywords": {
						"type": "array",
						"items": {"type": "string"},
						"description": "用于检索的关键词列表"
					}
				},
				"required": ["topic", "content"]
			}`),
		},
		"search_memory": {
			Desc: `搜索已保存的记忆。当用户问"你还记得吗"、"之前说过什么"、引用过去的对话内容，或当前问题需要历史上下文时调用。query 为空时返回所有记忆。`,
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {
						"type": "string",
						"description": "搜索关键词，留空返回所有记忆"
					},
					"limit": {
						"type": "integer",
						"description": "最多返回条数，默认10"
					}
				}
			}`),
		},
		"delete_memory": {
			Desc: "删除一条已保存的记忆，需要提供记忆ID",
			Schema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {
						"type": "string",
						"description": "要删除的记忆ID，如 mem_1"
					}
				},
				"required": ["id"]
			}`),
		},
	}
}
