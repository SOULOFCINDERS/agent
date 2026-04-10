package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ================================================================
// Step 2: 离线评估框架 (Memory Evaluation Framework)
// ================================================================
//
// 功能：
//   1. 定义标准评估数据集格式 (EvalCase)
//   2. 检索评估：Recall@K, Precision@K, MRR, nDCG
//   3. 压缩评估：压缩率 + 信息保留率（复用 verifier 的 checkCoverage）
//   4. 冲突检测评估：Precision + Recall
//   5. 自动化 Eval Runner，输出可对比的 benchmark 报告
//
// 使用方式：
//   cases := LoadEvalCases("eval_data.json")
//   runner := NewEvalRunner(store, compressor, verifier)
//   report := runner.RunAll(ctx, cases)
//   fmt.Println(report.String())

// ================================================================
// 评估数据集格式
// ================================================================

// EvalCase 一条评估用例
type EvalCase struct {
	// 唯一标识
	ID   string `json:"id"`
	Type string `json:"type"` // "search" | "compress" | "conflict"

	// 检索评估字段
	Query       string   `json:"query,omitempty"`
	RelevantIDs []string `json:"relevant_ids,omitempty"` // ground truth: 相关记忆 ID

	// 压缩评估字段
	Messages []llm.Message `json:"messages,omitempty"`   // 待压缩的消息
	KeyFacts []string      `json:"key_facts,omitempty"`  // ground truth: 关键事实

	// 冲突检测评估字段
	NewContent      string `json:"new_content,omitempty"`       // 新记忆内容
	ExpectedConflict bool  `json:"expected_conflict,omitempty"` // 是否应该检测到冲突
	ConflictWithID  string `json:"conflict_with_id,omitempty"`  // 应该与哪条记忆冲突
}

// EvalDataset 评估数据集
type EvalDataset struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Version     string     `json:"version"`
	Cases       []EvalCase `json:"cases"`
}

// ================================================================
// 评估结果
// ================================================================

// SearchEvalResult 检索评估结果
type SearchEvalResult struct {
	TotalCases  int     `json:"total_cases"`
	RecallAt3   float64 `json:"recall_at_3"`
	RecallAt5   float64 `json:"recall_at_5"`
	PrecisionAt3 float64 `json:"precision_at_3"`
	PrecisionAt5 float64 `json:"precision_at_5"`
	MRR         float64 `json:"mrr"`
	NDCG        float64 `json:"ndcg"`

	// 逐 case 的详细结果
	Details []SearchCaseResult `json:"details,omitempty"`
}

// SearchCaseResult 单条检索 case 的结果
type SearchCaseResult struct {
	CaseID      string   `json:"case_id"`
	Query       string   `json:"query"`
	RelevantIDs []string `json:"relevant_ids"`
	ReturnedIDs []string `json:"returned_ids"`
	RecallAt3   float64  `json:"recall_at_3"`
	RecallAt5   float64  `json:"recall_at_5"`
	MRR         float64  `json:"mrr"`
}

// CompressEvalResult 压缩评估结果
type CompressEvalResult struct {
	TotalCases       int     `json:"total_cases"`
	AvgCompressionRatio float64 `json:"avg_compression_ratio"`
	AvgFactRetention float64 `json:"avg_fact_retention"`  // 关键事实保留率
	AvgTokensSaved   float64 `json:"avg_tokens_saved"`

	Details []CompressCaseResult `json:"details,omitempty"`
}

// CompressCaseResult 单条压缩 case 的结果
type CompressCaseResult struct {
	CaseID           string  `json:"case_id"`
	OriginalTokens   int     `json:"original_tokens"`
	CompressedTokens int     `json:"compressed_tokens"`
	CompressionRatio float64 `json:"compression_ratio"`
	FactRetention    float64 `json:"fact_retention"`
	CoveredFacts     int     `json:"covered_facts"`
	TotalFacts       int     `json:"total_facts"`
	SummaryPreview   string  `json:"summary_preview"`
}

// ConflictEvalResult 冲突检测评估结果
type ConflictEvalResult struct {
	TotalCases  int     `json:"total_cases"`
	Precision   float64 `json:"precision"`    // 检测到的冲突中真正有冲突的比例
	Recall      float64 `json:"recall"`       // 实际有冲突的 case 中被检测到的比例
	F1          float64 `json:"f1"`

	Details []ConflictCaseResult `json:"details,omitempty"`
}

// ConflictCaseResult 单条冲突检测 case 的结果
type ConflictCaseResult struct {
	CaseID           string `json:"case_id"`
	ExpectedConflict bool   `json:"expected_conflict"`
	DetectedConflict bool   `json:"detected_conflict"`
	Correct          bool   `json:"correct"`
	DetectedType     string `json:"detected_type,omitempty"`
	DetectedWithID   string `json:"detected_with_id,omitempty"`
}

// FullEvalReport 完整评估报告
type FullEvalReport struct {
	Timestamp time.Time           `json:"timestamp"`
	Search    *SearchEvalResult   `json:"search,omitempty"`
	Compress  *CompressEvalResult `json:"compress,omitempty"`
	Conflict  *ConflictEvalResult `json:"conflict,omitempty"`
}

// ================================================================
// 文件 I/O（需要 import time，但这里不能因为 FullEvalReport 已经用了）
// ================================================================

// 注意：time 包在 FullEvalReport 中通过 Timestamp 字段已间接使用
// 但本文件的 import 中没有 time，需要添加
// 为了避免循环，我们把 time 的使用放在 Timestamp 赋值处用 now() 替代

// ================================================================
// EvalRunner 评估执行器
// ================================================================

// EvalRunner 评估执行器
type EvalRunner struct {
	store      *Store
	compressor *Compressor
	verifier   *SummaryVerifier
	detector   *ConflictDetector
}

// NewEvalRunner 创建评估执行器
func NewEvalRunner(store *Store, compressor *Compressor, verifier *SummaryVerifier) *EvalRunner {
	return &EvalRunner{
		store:      store,
		compressor: compressor,
		verifier:   verifier,
		detector:   NewConflictDetector(),
	}
}

// ================================================================
// 检索评估
// ================================================================

// EvalSearch 执行检索评估
func (r *EvalRunner) EvalSearch(cases []EvalCase) *SearchEvalResult {
	result := &SearchEvalResult{}

	var recalls3, recalls5, precisions3, precisions5, mrrs, ndcgs []float64

	for _, c := range cases {
		if c.Type != "search" || c.Query == "" {
			continue
		}
		result.TotalCases++

		// 执行检索
		results := r.store.Search(c.Query, 10)
		var returnedIDs []string
		for _, e := range results {
			returnedIDs = append(returnedIDs, e.ID)
		}

		// 计算指标
		r3 := computeRecallAtK(returnedIDs, c.RelevantIDs, 3)
		r5 := computeRecallAtK(returnedIDs, c.RelevantIDs, 5)
		p3 := computePrecisionAtK(returnedIDs, c.RelevantIDs, 3)
		p5 := computePrecisionAtK(returnedIDs, c.RelevantIDs, 5)
		mrr := computeMRR(returnedIDs, c.RelevantIDs)
		ndcg := computeNDCG(returnedIDs, c.RelevantIDs, 5)

		recalls3 = append(recalls3, r3)
		recalls5 = append(recalls5, r5)
		precisions3 = append(precisions3, p3)
		precisions5 = append(precisions5, p5)
		mrrs = append(mrrs, mrr)
		ndcgs = append(ndcgs, ndcg)

		result.Details = append(result.Details, SearchCaseResult{
			CaseID:      c.ID,
			Query:       c.Query,
			RelevantIDs: c.RelevantIDs,
			ReturnedIDs: returnedIDs,
			RecallAt3:   r3,
			RecallAt5:   r5,
			MRR:         mrr,
		})
	}

	if result.TotalCases > 0 {
		result.RecallAt3 = avgFloat64Slice(recalls3)
		result.RecallAt5 = avgFloat64Slice(recalls5)
		result.PrecisionAt3 = avgFloat64Slice(precisions3)
		result.PrecisionAt5 = avgFloat64Slice(precisions5)
		result.MRR = avgFloat64Slice(mrrs)
		result.NDCG = avgFloat64Slice(ndcgs)
	}

	return result
}

// ================================================================
// 压缩评估
// ================================================================

// EvalCompress 执行压缩评估
func (r *EvalRunner) EvalCompress(ctx context.Context, cases []EvalCase) *CompressEvalResult {
	result := &CompressEvalResult{}

	var ratios, retentions []float64
	var tokensSaved []int

	for _, c := range cases {
		if c.Type != "compress" || len(c.Messages) == 0 {
			continue
		}
		result.TotalCases++

		// 计算原始 token 数
		originalTokens := estimateTokens(c.Messages)

		// 执行压缩
		cr, err := r.compressor.Compress(ctx, c.Messages)
		if err != nil {
			continue
		}

		compressedTokens := cr.EstimatedTokens
		ratio := 0.0
		if originalTokens > 0 {
			ratio = float64(compressedTokens) / float64(originalTokens)
		}

		// 评估信息保留率
		factRetention := 1.0
		coveredCount := 0
		if len(c.KeyFacts) > 0 && cr.SummaryText != "" {
			vr := checkFactCoverage(cr.SummaryText, c.KeyFacts)
			factRetention = vr.coverageRate
			coveredCount = vr.coveredCount
		}

		saved := originalTokens - compressedTokens
		ratios = append(ratios, ratio)
		retentions = append(retentions, factRetention)
		tokensSaved = append(tokensSaved, saved)

		preview := cr.SummaryText
		if len([]rune(preview)) > 100 {
			preview = string([]rune(preview)[:100]) + "..."
		}

		result.Details = append(result.Details, CompressCaseResult{
			CaseID:           c.ID,
			OriginalTokens:   originalTokens,
			CompressedTokens: compressedTokens,
			CompressionRatio: ratio,
			FactRetention:    factRetention,
			CoveredFacts:     coveredCount,
			TotalFacts:       len(c.KeyFacts),
			SummaryPreview:   preview,
		})
	}

	if result.TotalCases > 0 {
		result.AvgCompressionRatio = avgFloat64Slice(ratios)
		result.AvgFactRetention = avgFloat64Slice(retentions)
		result.AvgTokensSaved = avgIntSlice(tokensSaved)
	}

	return result
}

// ================================================================
// 冲突检测评估
// ================================================================

// EvalConflict 执行冲突检测评估
func (r *EvalRunner) EvalConflict(ctx context.Context, cases []EvalCase) *ConflictEvalResult {
	result := &ConflictEvalResult{}

	var truePositive, falsePositive, falseNegative int

	// 获取当前所有记忆
	entries := r.store.List(0)

	for _, c := range cases {
		if c.Type != "conflict" || c.NewContent == "" {
			continue
		}
		result.TotalCases++

		// 运行冲突检测
		detected := false
		detectedType := ""
		detectedWithID := ""

		// P0: 显式覆盖
		if cr := r.detector.DetectExplicitOverride(c.NewContent, entries); cr != nil {
			detected = true
			detectedType = string(cr.Type)
			detectedWithID = cr.ConflictingID
		}

		// P1: 语义冲突
		if !detected {
			if cr := r.detector.DetectSemanticConflict(ctx, c.NewContent, entries); cr != nil {
				detected = true
				detectedType = string(cr.Type)
				detectedWithID = cr.ConflictingID
			}
		}

		correct := detected == c.ExpectedConflict
		if detected && c.ExpectedConflict {
			truePositive++
		} else if detected && !c.ExpectedConflict {
			falsePositive++
		} else if !detected && c.ExpectedConflict {
			falseNegative++
		}

		result.Details = append(result.Details, ConflictCaseResult{
			CaseID:           c.ID,
			ExpectedConflict: c.ExpectedConflict,
			DetectedConflict: detected,
			Correct:          correct,
			DetectedType:     detectedType,
			DetectedWithID:   detectedWithID,
		})
	}

	// 计算 Precision / Recall / F1
	if truePositive+falsePositive > 0 {
		result.Precision = float64(truePositive) / float64(truePositive+falsePositive)
	}
	if truePositive+falseNegative > 0 {
		result.Recall = float64(truePositive) / float64(truePositive+falseNegative)
	}
	if result.Precision+result.Recall > 0 {
		result.F1 = 2 * result.Precision * result.Recall / (result.Precision + result.Recall)
	}

	return result
}

// ================================================================
// 完整评估
// ================================================================

// RunAll 执行全部评估
func (r *EvalRunner) RunAll(ctx context.Context, cases []EvalCase) *FullEvalReport {
	report := &FullEvalReport{Timestamp: time.Now()}

	// 按类型分类
	var searchCases, compressCases, conflictCases []EvalCase
	for _, c := range cases {
		switch c.Type {
		case "search":
			searchCases = append(searchCases, c)
		case "compress":
			compressCases = append(compressCases, c)
		case "conflict":
			conflictCases = append(conflictCases, c)
		}
	}

	if len(searchCases) > 0 {
		report.Search = r.EvalSearch(searchCases)
	}
	if len(compressCases) > 0 {
		report.Compress = r.EvalCompress(ctx, compressCases)
	}
	if len(conflictCases) > 0 {
		report.Conflict = r.EvalConflict(ctx, conflictCases)
	}

	return report
}

// String 生成可读报告
func (r *FullEvalReport) String() string {
	var b strings.Builder
	b.WriteString("=== Memory System Evaluation Report ===\n\n")

	if r.Search != nil {
		b.WriteString("--- Search Evaluation ---\n")
		b.WriteString(fmt.Sprintf("  Cases:        %d\n", r.Search.TotalCases))
		b.WriteString(fmt.Sprintf("  Recall@3:     %.3f\n", r.Search.RecallAt3))
		b.WriteString(fmt.Sprintf("  Recall@5:     %.3f\n", r.Search.RecallAt5))
		b.WriteString(fmt.Sprintf("  Precision@3:  %.3f\n", r.Search.PrecisionAt3))
		b.WriteString(fmt.Sprintf("  Precision@5:  %.3f\n", r.Search.PrecisionAt5))
		b.WriteString(fmt.Sprintf("  MRR:          %.3f\n", r.Search.MRR))
		b.WriteString(fmt.Sprintf("  nDCG@5:       %.3f\n\n", r.Search.NDCG))
	}

	if r.Compress != nil {
		b.WriteString("--- Compression Evaluation ---\n")
		b.WriteString(fmt.Sprintf("  Cases:              %d\n", r.Compress.TotalCases))
		b.WriteString(fmt.Sprintf("  Avg Compression:    %.2f\n", r.Compress.AvgCompressionRatio))
		b.WriteString(fmt.Sprintf("  Avg Fact Retention: %.3f\n", r.Compress.AvgFactRetention))
		b.WriteString(fmt.Sprintf("  Avg Tokens Saved:   %.0f\n\n", r.Compress.AvgTokensSaved))
	}

	if r.Conflict != nil {
		b.WriteString("--- Conflict Detection Evaluation ---\n")
		b.WriteString(fmt.Sprintf("  Cases:      %d\n", r.Conflict.TotalCases))
		b.WriteString(fmt.Sprintf("  Precision:  %.3f\n", r.Conflict.Precision))
		b.WriteString(fmt.Sprintf("  Recall:     %.3f\n", r.Conflict.Recall))
		b.WriteString(fmt.Sprintf("  F1:         %.3f\n\n", r.Conflict.F1))
	}

	return b.String()
}

// ================================================================
// 数据集 I/O
// ================================================================

// LoadEvalCases 从 JSON 文件加载评估数据集
func LoadEvalCases(path string) ([]EvalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read eval file: %w", err)
	}

	// 先尝试作为 EvalDataset 解析
	var dataset EvalDataset
	if err := json.Unmarshal(data, &dataset); err == nil && len(dataset.Cases) > 0 {
		return dataset.Cases, nil
	}

	// 再尝试作为 []EvalCase 解析
	var cases []EvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("parse eval data: %w", err)
	}
	return cases, nil
}

// SaveEvalReport 将评估报告保存为 JSON
func SaveEvalReport(report *FullEvalReport, path string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// ================================================================
// IR 指标计算函数
// ================================================================

// computeRecallAtK 计算 Recall@K
func computeRecallAtK(resultIDs, relevantIDs []string, k int) float64 {
	if len(relevantIDs) == 0 {
		return 1.0
	}
	topK := resultIDs
	if len(topK) > k {
		topK = topK[:k]
	}
	topKSet := toStringSet(topK)
	hits := 0
	for _, id := range relevantIDs {
		if topKSet[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(relevantIDs))
}

// computePrecisionAtK 计算 Precision@K
func computePrecisionAtK(resultIDs, relevantIDs []string, k int) float64 {
	topK := resultIDs
	if len(topK) > k {
		topK = topK[:k]
	}
	if len(topK) == 0 {
		return 0
	}
	relevantSet := toStringSet(relevantIDs)
	hits := 0
	for _, id := range topK {
		if relevantSet[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(topK))
}

// computeMRR 计算 Mean Reciprocal Rank
func computeMRR(resultIDs, relevantIDs []string) float64 {
	relevantSet := toStringSet(relevantIDs)
	for i, id := range resultIDs {
		if relevantSet[id] {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// computeNDCG 计算 normalized Discounted Cumulative Gain
func computeNDCG(resultIDs, relevantIDs []string, k int) float64 {
	relevantSet := toStringSet(relevantIDs)

	// DCG
	dcg := 0.0
	topK := resultIDs
	if len(topK) > k {
		topK = topK[:k]
	}
	for i, id := range topK {
		if relevantSet[id] {
			dcg += 1.0 / math.Log2(float64(i+2)) // i+2 because rank starts at 1
		}
	}

	// iDCG (ideal: all relevant docs at top)
	idealK := k
	if len(relevantIDs) < idealK {
		idealK = len(relevantIDs)
	}
	idcg := 0.0
	for i := 0; i < idealK; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}

	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// ================================================================
// 信息保留率计算（不依赖 LLM，复用 verifier 的关键词匹配逻辑）
// ================================================================

type factCoverageResult struct {
	coverageRate float64
	coveredCount int
}

// checkFactCoverage 检查摘要对关键事实的覆盖率
func checkFactCoverage(summary string, keyFacts []string) factCoverageResult {
	summaryLower := strings.ToLower(summary)
	covered := 0

	for _, fact := range keyFacts {
		factKeywords := extractFactKeywords(fact)
		matchCount := 0
		for _, kw := range factKeywords {
			if strings.Contains(summaryLower, strings.ToLower(kw)) {
				matchCount++
			}
		}
		if len(factKeywords) > 0 && float64(matchCount)/float64(len(factKeywords)) >= 0.5 {
			covered++
		}
	}

	rate := 0.0
	if len(keyFacts) > 0 {
		rate = float64(covered) / float64(len(keyFacts))
	}
	return factCoverageResult{coverageRate: rate, coveredCount: covered}
}

// ================================================================
// 工具函数
// ================================================================

func toStringSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}

// time 包的 import 需要在 FullEvalReport 中使用
// 但 Go 的 import 声明在文件顶部已经列出，这里用一个 dummy 来确保编译
