package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/memory"
)

// ---------- 保存记忆工具 ----------

type SaveMemoryTool struct {
	store *memory.Store
}

func NewSaveMemoryTool(store *memory.Store) *SaveMemoryTool {
	return &SaveMemoryTool{store: store}
}

func (t *SaveMemoryTool) Name() string { return "save_memory" }

func (t *SaveMemoryTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	topic, _ := pickString(args, "topic")
	content, _ := pickString(args, "content")

	if topic == "" {
		return nil, fmt.Errorf("missing topic")
	}
	if content == "" {
		return nil, fmt.Errorf("missing content")
	}

	// 解析 keywords
	var keywords []string
	if kw, ok := args["keywords"]; ok && kw != nil {
		switch v := kw.(type) {
		case []any:
			for _, item := range v {
				keywords = append(keywords, fmt.Sprint(item))
			}
		case string:
			for _, k := range strings.Split(v, ",") {
				k = strings.TrimSpace(k)
				if k != "" {
					keywords = append(keywords, k)
				}
			}
		}
	}

	addResult := t.store.Add(topic, content, keywords)

	resp := map[string]any{
		"status": "saved",
		"id":     addResult.Entry.ID,
		"topic":  addResult.Entry.Topic,
	}

	if addResult.Conflict != nil {
		c := addResult.Conflict
		resp["conflict_type"] = string(c.Type)
		resp["conflict_id"] = c.ConflictingID
		resp["old_content"] = c.OldContent

		switch c.Type {
		case "explicit_override":
			resp["message"] = fmt.Sprintf(
				"已保存。旧记忆「%s」已被取代（%s）",
				truncStr(c.OldContent, 60), c.Resolution,
			)
		case "semantic_conflict":
			resp["message"] = fmt.Sprintf(
				"已保存。检测到与 %s「%s」语义冲突（相似度%.0f%%），%s",
				c.ConflictingID, truncStr(c.OldContent, 60),
				c.Similarity*100, c.Resolution,
			)
		case "need_confirm":
			resp["message"] = fmt.Sprintf(
				"已保存，但检测到与 %s「%s」可能矛盾（相似度%.0f%%）。建议确认是否删除旧记忆 %s。",
				c.ConflictingID, truncStr(c.OldContent, 60),
				c.Similarity*100, c.ConflictingID,
			)
		default:
			resp["message"] = fmt.Sprintf("已保存记忆 [%s]: %s", addResult.Entry.Topic, addResult.Entry.Content)
		}
	} else {
		resp["message"] = fmt.Sprintf("已保存记忆 [%s]: %s", addResult.Entry.Topic, addResult.Entry.Content)
	}

	return resp, nil
}

// ---------- 搜索记忆工具 ----------

type SearchMemoryTool struct {
	store *memory.Store
}

func NewSearchMemoryTool(store *memory.Store) *SearchMemoryTool {
	return &SearchMemoryTool{store: store}
}

func (t *SearchMemoryTool) Name() string { return "search_memory" }

func (t *SearchMemoryTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	query, _ := pickString(args, "query", "input")

	if query == "" {
		// 无查询词，返回所有记忆
		entries := t.store.List(20)
		if len(entries) == 0 {
			return "当前没有任何已保存的记忆。", nil
		}
		return formatEntries(entries), nil
	}

	limit := 10
	if v, ok := args["limit"]; ok {
		switch x := v.(type) {
		case float64:
			limit = int(x)
		case int:
			limit = x
		}
	}

	entries := t.store.Search(query, limit)
	if len(entries) == 0 {
		return fmt.Sprintf("没有找到与 \"%s\" 相关的记忆。", query), nil
	}

	return formatEntries(entries), nil
}

// ---------- 删除记忆工具 ----------

type DeleteMemoryTool struct {
	store *memory.Store
}

func NewDeleteMemoryTool(store *memory.Store) *DeleteMemoryTool {
	return &DeleteMemoryTool{store: store}
}

func (t *DeleteMemoryTool) Name() string { return "delete_memory" }

func (t *DeleteMemoryTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	id, _ := pickString(args, "id")
	if id == "" {
		return nil, fmt.Errorf("missing memory id")
	}

	if t.store.Delete(id) {
		return map[string]any{
			"status":  "deleted",
			"id":      id,
			"message": fmt.Sprintf("已删除记忆 %s", id),
		}, nil
	}

	return nil, fmt.Errorf("memory %s not found", id)
}

// ---------- 辅助函数 ----------

func formatEntries(entries []memory.Entry) string {
	var results []map[string]any
	for _, e := range entries {
		results = append(results, map[string]any{
			"id":         e.ID,
			"topic":      e.Topic,
			"content":    e.Content,
			"keywords":   e.Keywords,
			"created_at": e.CreatedAt.Format("2006-01-02 15:04"),
			"updated_at": e.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}
	b, _ := json.MarshalIndent(results, "", "  ")
	return string(b)
}


// truncStr 截断字符串到 maxRunes 长度
func truncStr(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
