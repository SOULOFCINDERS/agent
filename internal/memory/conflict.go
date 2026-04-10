package memory

import (
	"context"
	"strings"

	dmem "github.com/SOULOFCINDERS/agent/internal/domain/memory"
	"github.com/SOULOFCINDERS/agent/internal/rag"
)

// ConflictDetector 记忆冲突检测器
// 利用 TF-IDF embedding + 否定模式匹配，实现 P0~P2 的冲突检测流水线
type ConflictDetector struct {
	embedder *rag.TFIDFEmbedder
}

// NewConflictDetector 创建冲突检测器
func NewConflictDetector() *ConflictDetector {
	return &ConflictDetector{
		embedder: rag.NewTFIDFEmbedder(256),
	}
}

// ================================================================
// P0: 显式覆盖检测
// ================================================================

// negationPattern 否定模式
type negationPattern struct {
	trigger string   // 新内容中的否定表达
	targets []string // 被否定的旧内容关键词（空=仅按 topic 匹配）
}

var negationPatterns = []negationPattern{
	{"不再喜欢", []string{"喜欢"}},
	{"不喜欢", []string{"喜欢"}},
	{"不再使用", []string{"使用", "用"}},
	{"不用了", []string{"使用", "用"}},
	{"放弃了", []string{"使用", "选择"}},
	{"戒了", []string{"喜欢", "吃", "喝"}},
	{"换成", nil},
	{"改为", nil},
	{"改成", nil},
	{"改用", nil},
	{"转向", nil},
	{"no longer", []string{"like", "prefer", "use"}},
	{"switched to", nil},
	{"changed to", nil},
	{"stopped using", []string{"use", "using"}},
	{"don't like", []string{"like", "prefer"}},
}

// DetectExplicitOverride P0: 检测新内容是否显式否定某条旧记忆
func (d *ConflictDetector) DetectExplicitOverride(newContent string, entries []Entry) *dmem.ConflictResult {
	lower := strings.ToLower(newContent)

	for _, p := range negationPatterns {
		if !strings.Contains(lower, p.trigger) {
			continue
		}

		// 无 target 关键词 → 通用"换成/改为"，匹配交给调用方按 topic
		if len(p.targets) == 0 {
			// 尝试在所有 active 记忆中寻找同类主题
			for _, e := range entries {
				if !e.IsActive() {
					continue
				}
				// "换成/改用X" 暗示旧的同一领域偏好应被取代
				// 这里靠语义匹配兜底（P1），P0 仅处理有明确 target 的
			}
			continue
		}

		// 有 target → 在旧记忆中寻找包含这些关键词的条目
		for _, e := range entries {
			if !e.IsActive() {
				continue
			}
			eLower := strings.ToLower(e.Content)
			for _, kw := range p.targets {
				if strings.Contains(eLower, kw) {
					return &dmem.ConflictResult{
						Type:          dmem.ConflictExplicit,
						ConflictingID: e.ID,
						OldContent:    e.Content,
						NewContent:    newContent,
						AutoResolved:  true,
						Resolution:    "显式否定，已自动取代旧记忆",
					}
				}
			}
		}
	}
	return nil
}

// ================================================================
// P1: 语义匹配 + 冲突检测
// ================================================================

// semanticSimilarityThreshold 语义相似度阈值
// 高于此值认为两条记忆在同一语义域，需要做冲突判断
const semanticSimilarityThreshold = 0.6

// DetectSemanticConflict P1: 用 embedding 相似度检测语义冲突
func (d *ConflictDetector) DetectSemanticConflict(ctx context.Context, newContent string, entries []Entry) *dmem.ConflictResult {
	newEmb, err := d.embedder.Embed(ctx, newContent)
	if err != nil {
		return nil
	}

	var bestEntry *Entry
	var bestSim float64

	for i := range entries {
		e := &entries[i]
		if !e.IsActive() {
			continue
		}

		var eEmb []float64
		if len(e.Embedding) > 0 {
			eEmb = e.Embedding
		} else {
			eEmb, err = d.embedder.Embed(ctx, e.Content)
			if err != nil {
				continue
			}
		}

		sim := rag.CosineSimilarity(newEmb, eEmb)
		if sim > bestSim {
			bestSim = sim
			bestEntry = e
		}
	}

	if bestEntry == nil || bestSim <= semanticSimilarityThreshold {
		return nil
	}

	// 完全重复内容不算冲突
	if strings.EqualFold(strings.TrimSpace(bestEntry.Content), strings.TrimSpace(newContent)) {
		return nil
	}

	return &dmem.ConflictResult{
		Type:          dmem.ConflictSemantic,
		ConflictingID: bestEntry.ID,
		OldContent:    bestEntry.Content,
		NewContent:    newContent,
		Similarity:    bestSim,
	}
}

// ================================================================
// P2: 置信度比较
// ================================================================

// CompareConfidence 比较新旧记忆的置信度，决定是否可自动裁决
// 返回：autoResolve=是否可自动处理，keepNew=是否保留新记忆
func CompareConfidence(newConf, oldConf float64) (autoResolve bool, keepNew bool) {
	if newConf <= 0 {
		newConf = 1.0
	}
	diff := newConf - oldConf
	if diff > 0.2 {
		return true, true // 新 > 旧，自动覆盖
	}
	if diff < -0.2 {
		return true, false // 旧 > 新，保留旧
	}
	return false, false // 差距不大，需要用户确认
}

// ComputeEmbedding 为一段文本计算 embedding 向量
func (d *ConflictDetector) ComputeEmbedding(ctx context.Context, content string) []float64 {
	emb, err := d.embedder.Embed(ctx, content)
	if err != nil {
		return nil
	}
	return emb
}
