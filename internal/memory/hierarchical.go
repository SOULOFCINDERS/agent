package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SOULOFCINDERS/agent/internal/llm"
)

// ================================================================
// 分层摘要 (Hierarchical Summarization)
// ================================================================
//
// 设计思路：
//   L0: 原始消息（窗口内保留，不摘要）
//   L1: 每 ChunkRounds 轮对话生成一个 chunk summary（~100字）
//   L2: 每 L2MergeCount 个 L1 摘要合并为一个 session summary（~200字）
//
// 好处：
//   - 比单一摘要保留更多细节（可下钻到 L1 回溯）
//   - 增量友好：新一轮对话只需新增一个 L1 chunk，无需重新摘要
//   - 可灵活注入上下文：全局理解用 L2，细节回溯展开 L1

// ================================================================
// 数据结构
// ================================================================

// ChunkSummary L1 分段摘要
type ChunkSummary struct {
	ID          string    `json:"id"`
	RoundStart  int       `json:"round_start"`  // 覆盖的起始轮次
	RoundEnd    int       `json:"round_end"`    // 覆盖的结束轮次
	Summary     string    `json:"summary"`
	KeyEntities []string  `json:"key_entities,omitempty"` // 提到的关键实体
	CreatedAt   time.Time `json:"created_at"`
	TokenCount  int       `json:"token_count"`  // 原始消息的估算 token 数
}

// HierarchicalSummary 分层摘要全量状态
type HierarchicalSummary struct {
	SessionSummary string         `json:"session_summary"`  // L2: 全局摘要
	ChunkSummaries []ChunkSummary `json:"chunk_summaries"`  // L1: 分段摘要列表
	TotalRounds    int            `json:"total_rounds"`     // 已处理的总轮次数
	UpdatedAt      time.Time      `json:"updated_at"`
}

// HierarchicalConfig 分层摘要配置
type HierarchicalConfig struct {
	// ChunkRounds 每多少轮对话生成一个 L1 chunk summary，默认 5
	ChunkRounds int

	// L2MergeCount 每多少个 L1 chunk 触发一次 L2 全局摘要合并，默认 5
	L2MergeCount int

	// MaxL1Display 注入上下文时最多展示几个 L1 chunk，默认 3（最近的）
	MaxL1Display int
}

// HierarchicalCompressor 分层摘要压缩器
type HierarchicalCompressor struct {
	llmClient llm.Client
	cfg       HierarchicalConfig
	mu        sync.RWMutex
	state     HierarchicalSummary
	filePath  string
}

// NewHierarchicalCompressor 创建分层摘要压缩器
func NewHierarchicalCompressor(client llm.Client, dataDir string, cfg HierarchicalConfig) (*HierarchicalCompressor, error) {
	if cfg.ChunkRounds <= 0 {
		cfg.ChunkRounds = 5
	}
	if cfg.L2MergeCount <= 0 {
		cfg.L2MergeCount = 5
	}
	if cfg.MaxL1Display <= 0 {
		cfg.MaxL1Display = 3
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create hierarchical dir: %w", err)
	}

	hc := &HierarchicalCompressor{
		llmClient: client,
		cfg:       cfg,
		filePath:  filepath.Join(dataDir, "hierarchical_summary.json"),
	}

	// 加载已有状态
	_ = hc.load()

	return hc, nil
}

// ================================================================
// 核心方法
// ================================================================

// ProcessNewRounds 处理新一批轮次的对话，更新分层摘要
// rounds 是按轮次切好的消息组，每组以 user 消息开始
func (hc *HierarchicalCompressor) ProcessNewRounds(ctx context.Context, rounds [][]llm.Message) error {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	if len(rounds) == 0 {
		return nil
	}

	// 收集待处理的轮次
	pendingRounds := rounds
	startRound := hc.state.TotalRounds + 1

	// 按 ChunkRounds 分组，每组生成一个 L1 chunk summary
	for len(pendingRounds) >= hc.cfg.ChunkRounds {
		chunk := pendingRounds[:hc.cfg.ChunkRounds]
		pendingRounds = pendingRounds[hc.cfg.ChunkRounds:]

		endRound := startRound + hc.cfg.ChunkRounds - 1

		cs, err := hc.generateChunkSummary(ctx, chunk, startRound, endRound)
		if err != nil {
			// 降级：用 fallback 摘要
			cs = hc.fallbackChunkSummary(chunk, startRound, endRound)
		}

		hc.state.ChunkSummaries = append(hc.state.ChunkSummaries, cs)
		hc.state.TotalRounds = endRound
		startRound = endRound + 1
	}

	// 剩余不满一个 chunk 的轮次也记录轮次数（不生成 L1 摘要，留待下次）
	hc.state.TotalRounds += len(pendingRounds)

	// 检查是否需要触发 L2 全局摘要合并
	if len(hc.state.ChunkSummaries) >= hc.cfg.L2MergeCount {
		sessionSummary, err := hc.generateSessionSummary(ctx)
		if err == nil {
			hc.state.SessionSummary = sessionSummary
		}
	}

	hc.state.UpdatedAt = time.Now()
	return hc.save()
}

// GetContextInjection 生成用于注入 system prompt 的分层摘要文本
// 返回 L2 全局摘要 + 最近 N 个 L1 chunk 摘要
func (hc *HierarchicalCompressor) GetContextInjection() string {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	if len(hc.state.ChunkSummaries) == 0 && hc.state.SessionSummary == "" {
		return ""
	}

	var b strings.Builder

	// L2: 全局摘要
	if hc.state.SessionSummary != "" {
		b.WriteString("[全局对话摘要]\n")
		b.WriteString(hc.state.SessionSummary)
		b.WriteString("\n\n")
	}

	// L1: 最近的 chunk 摘要
	chunks := hc.state.ChunkSummaries
	start := 0
	if len(chunks) > hc.cfg.MaxL1Display {
		start = len(chunks) - hc.cfg.MaxL1Display
	}

	if start < len(chunks) {
		b.WriteString("[近期对话细节]\n")
		for _, cs := range chunks[start:] {
			b.WriteString(fmt.Sprintf("- 轮次%d~%d: %s\n", cs.RoundStart, cs.RoundEnd, cs.Summary))
		}
	}

	return b.String()
}

// GetState 获取当前分层摘要状态（用于调试/测试）
func (hc *HierarchicalCompressor) GetState() HierarchicalSummary {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	return hc.state
}

// ================================================================
// L1: Chunk Summary 生成
// ================================================================

func (hc *HierarchicalCompressor) generateChunkSummary(ctx context.Context, rounds [][]llm.Message, startRound, endRound int) (ChunkSummary, error) {
	var conv strings.Builder
	tokenCount := 0

	for _, round := range rounds {
		for _, m := range round {
			tokenCount += estimateStringTokens(m.Content)
			switch m.Role {
			case "user":
				conv.WriteString(fmt.Sprintf("用户: %s\n", truncateStr(m.Content, 400)))
			case "assistant":
				if m.Content != "" {
					conv.WriteString(fmt.Sprintf("助手: %s\n", truncateStr(m.Content, 400)))
				}
				for _, tc := range m.ToolCalls {
					conv.WriteString(fmt.Sprintf("工具调用: %s\n", tc.Function.Name))
				}
			case "tool":
				conv.WriteString(fmt.Sprintf("工具结果: %s\n", truncateStr(m.Content, 200)))
			}
		}
	}

	prompt := []llm.Message{
		{
			Role: "system",
			Content: "你是一个对话摘要助手。请将以下几轮对话压缩为一段简洁摘要。" +
				"\n\n要求：" +
				"\n1. 2-3句话，不超过100字" +
				"\n2. 保留关键操作、结果和实体名称" +
				"\n3. 以JSON格式返回: {\"summary\": \"...\", \"key_entities\": [\"...\"]}" +
				"\n\n只返回JSON，不要其他内容。",
		},
		{
			Role:    "user",
			Content: conv.String(),
		},
	}

	resp, err := hc.llmClient.Chat(ctx, prompt, nil)
	if err != nil {
		return ChunkSummary{}, fmt.Errorf("chunk summary LLM call: %w", err)
	}

	// 尝试解析 JSON
	var parsed struct {
		Summary     string   `json:"summary"`
		KeyEntities []string `json:"key_entities"`
	}

	content := strings.TrimSpace(resp.Message.Content)
	// 容错：移除可能的 markdown code fence
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// JSON 解析失败，直接用原文作为 summary
		parsed.Summary = resp.Message.Content
	}

	return ChunkSummary{
		ID:          fmt.Sprintf("chunk_%d_%d", startRound, endRound),
		RoundStart:  startRound,
		RoundEnd:    endRound,
		Summary:     parsed.Summary,
		KeyEntities: parsed.KeyEntities,
		CreatedAt:   time.Now(),
		TokenCount:  tokenCount,
	}, nil
}

func (hc *HierarchicalCompressor) fallbackChunkSummary(rounds [][]llm.Message, startRound, endRound int) ChunkSummary {
	var parts []string
	for _, round := range rounds {
		for _, m := range round {
			if m.Role == "user" {
				parts = append(parts, truncateStr(m.Content, 50))
			}
		}
	}

	summary := "用户讨论了: " + strings.Join(parts, "; ")
	if len([]rune(summary)) > 100 {
		summary = string([]rune(summary)[:97]) + "..."
	}

	return ChunkSummary{
		ID:         fmt.Sprintf("chunk_%d_%d", startRound, endRound),
		RoundStart: startRound,
		RoundEnd:   endRound,
		Summary:    summary,
		CreatedAt:  time.Now(),
	}
}

// ================================================================
// L2: Session Summary 生成
// ================================================================

func (hc *HierarchicalCompressor) generateSessionSummary(ctx context.Context) (string, error) {
	var chunks strings.Builder
	for _, cs := range hc.state.ChunkSummaries {
		chunks.WriteString(fmt.Sprintf("- 轮次%d~%d: %s\n", cs.RoundStart, cs.RoundEnd, cs.Summary))
	}

	existingCtx := ""
	if hc.state.SessionSummary != "" {
		existingCtx = fmt.Sprintf("\n\n已有全局摘要（需要更新）：\n%s", hc.state.SessionSummary)
	}

	prompt := []llm.Message{
		{
			Role: "system",
			Content: "你是一个对话摘要助手。请根据以下分段摘要，生成一份全局对话摘要。" +
				"\n\n要求：" +
				"\n1. 4-6句话，不超过200字" +
				"\n2. 概括用户的主要目标、关键决策和当前进度" +
				"\n3. 如果有旧的全局摘要，在其基础上更新而非重写",
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("分段摘要列表：\n%s%s\n\n请生成全局摘要：", chunks.String(), existingCtx),
		},
	}

	resp, err := hc.llmClient.Chat(ctx, prompt, nil)
	if err != nil {
		return "", fmt.Errorf("session summary LLM call: %w", err)
	}

	return resp.Message.Content, nil
}

// ================================================================
// 持久化
// ================================================================

func (hc *HierarchicalCompressor) save() error {
	data, err := json.MarshalIndent(hc.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hierarchical state: %w", err)
	}
	return os.WriteFile(hc.filePath, data, 0644)
}

func (hc *HierarchicalCompressor) load() error {
	data, err := os.ReadFile(hc.filePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &hc.state)
}

// ================================================================
// 工具函数
// ================================================================

// SplitIntoRounds 将消息列表按轮次切分（每个 user 消息开始一个新轮次）
func SplitIntoRounds(msgs []llm.Message) [][]llm.Message {
	var rounds [][]llm.Message
	var current []llm.Message

	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		if m.Role == "user" && len(current) > 0 {
			rounds = append(rounds, current)
			current = nil
		}
		current = append(current, m)
	}
	if len(current) > 0 {
		rounds = append(rounds, current)
	}

	return rounds
}
