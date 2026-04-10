package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ================================================================
// 摘要质量验证 (Summary Quality Verifier)
// ================================================================
//
// 功能：
//   1. 从原始消息中提取关键事实 (key facts)
//   2. 检查摘要是否覆盖了这些关键事实
//   3. 覆盖率不足时，自动增强摘要
//
// 使用场景：
//   - 在 Compressor 生成摘要后调用，作为质量兜底
//   - 可选启用（有额外 LLM 调用开销）

// ================================================================
// 数据结构
// ================================================================

// VerifyResult 验证结果
type VerifyResult struct {
	KeyFacts      []string `json:"key_facts"`       // 提取的关键事实
	CoveredFacts  []string `json:"covered_facts"`   // 摘要中已覆盖的事实
	MissingFacts  []string `json:"missing_facts"`   // 摘要中遗漏的事实
	CoverageRate  float64  `json:"coverage_rate"`   // 覆盖率 (0~1)
	OriginalScore float64  `json:"original_score"`  // 原始摘要得分
	Enhanced      bool     `json:"enhanced"`        // 是否进行了增强
}

// VerifierConfig 验证器配置
type VerifierConfig struct {
	// MinCoverage 最低覆盖率阈值，低于此值触发增强。默认 0.7
	MinCoverage float64

	// MaxFacts 最多提取的关键事实数。默认 8
	MaxFacts int

	// Enabled 是否启用验证（验证有额外 LLM 开销）。默认 false
	Enabled bool
}

// SummaryVerifier 摘要质量验证器
type SummaryVerifier struct {
	llmClient llm.Client
	cfg       VerifierConfig
}

// NewSummaryVerifier 创建摘要验证器
func NewSummaryVerifier(client llm.Client, cfg VerifierConfig) *SummaryVerifier {
	if cfg.MinCoverage <= 0 {
		cfg.MinCoverage = 0.7
	}
	if cfg.MaxFacts <= 0 {
		cfg.MaxFacts = 8
	}
	return &SummaryVerifier{
		llmClient: client,
		cfg:       cfg,
	}
}

// IsEnabled 验证器是否启用
func (v *SummaryVerifier) IsEnabled() bool {
	return v.cfg.Enabled
}

// ================================================================
// 核心方法
// ================================================================

// VerifyAndEnhance 验证摘要质量，必要时增强
// 返回（可能增强后的）摘要和验证结果
func (v *SummaryVerifier) VerifyAndEnhance(ctx context.Context, summary string, originalMsgs []llm.Message) (string, *VerifyResult, error) {
	if !v.cfg.Enabled {
		return summary, nil, nil
	}

	// Step 1: 提取关键事实
	keyFacts, err := v.extractKeyFacts(ctx, originalMsgs)
	if err != nil {
		// 提取失败，不阻塞主流程，直接返回原摘要
		return summary, nil, nil
	}

	if len(keyFacts) == 0 {
		return summary, &VerifyResult{
			CoverageRate:  1.0,
			OriginalScore: 1.0,
		}, nil
	}

	// Step 2: 检查覆盖率
	result := v.checkCoverage(summary, keyFacts)

	// Step 3: 覆盖率不足时增强
	if result.CoverageRate < v.cfg.MinCoverage && len(result.MissingFacts) > 0 {
		enhanced, err := v.enhanceSummary(ctx, summary, result.MissingFacts)
		if err == nil {
			result.Enhanced = true
			return enhanced, result, nil
		}
		// 增强失败，返回原摘要
	}

	return summary, result, nil
}

// ================================================================
// 内部方法
// ================================================================

// extractKeyFacts 用 LLM 从原始消息中提取关键事实
func (v *SummaryVerifier) extractKeyFacts(ctx context.Context, msgs []llm.Message) ([]string, error) {
	var conv strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			conv.WriteString(fmt.Sprintf("用户: %s\n", truncateStr(m.Content, 300)))
		case "assistant":
			if m.Content != "" {
				conv.WriteString(fmt.Sprintf("助手: %s\n", truncateStr(m.Content, 300)))
			}
			for _, tc := range m.ToolCalls {
				conv.WriteString(fmt.Sprintf("工具调用: %s\n", tc.Function.Name))
			}
		case "tool":
			conv.WriteString(fmt.Sprintf("工具结果: %s\n", truncateStr(m.Content, 200)))
		}
	}

	prompt := []llm.Message{
		{
			Role: "system",
			Content: fmt.Sprintf("你是一个信息提取助手。请从以下对话中提取最重要的关键事实。"+
				"\n\n要求："+
				"\n1. 最多提取 %d 条关键事实"+
				"\n2. 每条事实是一个完整的短句"+
				"\n3. 优先提取：用户意图、关键决策、操作结果、数值数据"+
				"\n4. 以JSON数组格式返回: [\"事实1\", \"事实2\", ...]"+
				"\n\n只返回JSON数组，不要其他内容。", v.cfg.MaxFacts),
		},
		{
			Role:    "user",
			Content: conv.String(),
		},
	}

	resp, err := v.llmClient.Chat(ctx, prompt, nil)
	if err != nil {
		return nil, fmt.Errorf("extract key facts LLM call: %w", err)
	}

	content := strings.TrimSpace(resp.Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var facts []string
	if err := json.Unmarshal([]byte(content), &facts); err != nil {
		// JSON 解析失败，尝试按行分割
		lines := strings.Split(content, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			line = strings.TrimLeft(line, "- •·0123456789.)")
			line = strings.TrimSpace(line)
			if line != "" {
				facts = append(facts, line)
			}
		}
	}

	// 限制数量
	if len(facts) > v.cfg.MaxFacts {
		facts = facts[:v.cfg.MaxFacts]
	}

	return facts, nil
}

// checkCoverage 检查摘要对关键事实的覆盖率（基于关键词匹配）
func (v *SummaryVerifier) checkCoverage(summary string, keyFacts []string) *VerifyResult {
	result := &VerifyResult{
		KeyFacts: keyFacts,
	}

	summaryLower := strings.ToLower(summary)

	for _, fact := range keyFacts {
		// 提取事实中的关键词
		factKeywords := extractFactKeywords(fact)
		matchCount := 0
		for _, kw := range factKeywords {
			if strings.Contains(summaryLower, strings.ToLower(kw)) {
				matchCount++
			}
		}

		// 如果超过一半的关键词被覆盖，认为该事实已覆盖
		if len(factKeywords) > 0 && float64(matchCount)/float64(len(factKeywords)) >= 0.5 {
			result.CoveredFacts = append(result.CoveredFacts, fact)
		} else {
			result.MissingFacts = append(result.MissingFacts, fact)
		}
	}

	if len(keyFacts) > 0 {
		result.CoverageRate = float64(len(result.CoveredFacts)) / float64(len(keyFacts))
	} else {
		result.CoverageRate = 1.0
	}
	result.OriginalScore = result.CoverageRate

	return result
}

// enhanceSummary 增强摘要，补充遗漏的关键事实
func (v *SummaryVerifier) enhanceSummary(ctx context.Context, summary string, missingFacts []string) (string, error) {
	prompt := []llm.Message{
		{
			Role: "system",
			Content: "你是一个对话摘要增强助手。以下摘要遗漏了一些关键信息，请在保持简洁的前提下补充这些信息。" +
				"\n\n要求：" +
				"\n1. 在原摘要基础上自然地融入遗漏信息" +
				"\n2. 保持整体简洁，不超过300字" +
				"\n3. 不要出现'补充'、'遗漏'等元描述词汇",
		},
		{
			Role: "user",
			Content: fmt.Sprintf("原摘要：\n%s\n\n遗漏的关键信息：\n- %s\n\n请生成增强后的摘要：",
				summary, strings.Join(missingFacts, "\n- ")),
		},
	}

	resp, err := v.llmClient.Chat(ctx, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("enhance summary LLM call: %w", err)
	}

	return resp.Message.Content, nil
}

// extractFactKeywords 从一条事实中提取关键词（去除停用词后的实词）
func extractFactKeywords(fact string) []string {
	// 停用词集合（中英文）
	stopWords := map[string]bool{
		"的": true, "了": true, "在": true, "是": true, "和": true,
		"有": true, "就": true, "不": true, "也": true, "都": true,
		"而": true, "及": true, "与": true, "或": true, "等": true,
		"被": true, "把": true, "从": true, "到": true, "对": true,
		"为": true, "以": true, "中": true, "上": true, "下": true,
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "can": true, "to": true, "of": true,
		"in": true, "for": true, "on": true, "with": true, "at": true,
		"by": true, "from": true, "that": true, "this": true, "it": true,
		"and": true, "or": true, "but": true, "if": true, "not": true,
	}

	// 简单分词
	fact = strings.ToLower(fact)
	var keywords []string

	// 英文词
	for _, w := range strings.Fields(fact) {
		w = strings.Trim(w, `,.!?;:"'()[]{}、，。！？；：“”‘’（）【】`)
		if len(w) >= 2 && !stopWords[w] {
			keywords = append(keywords, w)
		}
	}

	// 中文：提取连续中文字符作为关键词（≥2字）
	var cjkBuf strings.Builder
	for _, r := range fact {
		if r >= 0x4e00 && r <= 0x9fff {
			cjkBuf.WriteRune(r)
		} else {
			if cjkBuf.Len() > 0 {
				cjkStr := cjkBuf.String()
				runes := []rune(cjkStr)
				if len(runes) >= 2 {
					// 全词 + 二元组
					keywords = append(keywords, cjkStr)
					for i := 0; i+2 <= len(runes); i++ {
						bigram := string(runes[i : i+2])
						if !stopWords[bigram] {
							keywords = append(keywords, bigram)
						}
					}
				}
				cjkBuf.Reset()
			}
		}
	}
	if cjkBuf.Len() > 0 {
		cjkStr := cjkBuf.String()
		runes := []rune(cjkStr)
		if len(runes) >= 2 {
			keywords = append(keywords, cjkStr)
			for i := 0; i+2 <= len(runes); i++ {
				bigram := string(runes[i : i+2])
				if !stopWords[bigram] {
					keywords = append(keywords, bigram)
				}
			}
		}
	}

	return keywords
}
