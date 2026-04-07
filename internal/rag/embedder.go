package rag

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// ---- TF-IDF 轻量 Embedder（零外部依赖） ----

// TFIDFEmbedder 基于 TF-IDF 的轻量嵌入器
// 优点：零外部依赖、速度极快、离线可用
// 缺点：不如神经网络 embedding 语义丰富
// 适合原型验证和不想依赖外部 API 的场景
type TFIDFEmbedder struct {
	dim        int
	vocabulary map[string]int // 词 → 维度索引
	idf        map[string]float64
	docCount   int
}

// NewTFIDFEmbedder 创建 TF-IDF embedder
// dim: 向量维度（hash 映射后的维度，推荐 256-1024）
func NewTFIDFEmbedder(dim int) *TFIDFEmbedder {
	if dim <= 0 {
		dim = 512
	}
	return &TFIDFEmbedder{
		dim:        dim,
		vocabulary: make(map[string]int),
		idf:        make(map[string]float64),
	}
}

func (e *TFIDFEmbedder) Dimension() int { return e.dim }

func (e *TFIDFEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	tokens := tokenize(text)
	vec := make([]float64, e.dim)

	// TF (term frequency)
	tf := make(map[string]int)
	for _, t := range tokens {
		tf[t]++
	}
	total := float64(len(tokens))
	if total == 0 {
		return vec, nil
	}

	for term, count := range tf {
		// hash term to dimension
		idx := hashToDim(term, e.dim)
		weight := float64(count) / total
		// 应用 IDF（如果有）
		if idfVal, ok := e.idf[term]; ok {
			weight *= idfVal
		}
		vec[idx] += weight
	}

	// L2 normalize
	normalize(vec)
	return vec, nil
}

func (e *TFIDFEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	results := make([][]float64, len(texts))
	for i, text := range texts {
		vec, err := e.Embed(ctx, text)
		if err != nil {
			return nil, fmt.Errorf("embed text %d: %w", i, err)
		}
		results[i] = vec
	}
	return results, nil
}

// UpdateIDF 用一批文档更新 IDF 值
func (e *TFIDFEmbedder) UpdateIDF(documents []string) {
	docFreq := make(map[string]int)
	e.docCount = len(documents)

	for _, doc := range documents {
		seen := make(map[string]bool)
		for _, t := range tokenize(doc) {
			if !seen[t] {
				docFreq[t]++
				seen[t] = true
			}
		}
	}

	for term, df := range docFreq {
		e.idf[term] = math.Log(float64(e.docCount+1) / float64(df+1))
	}
}

// ---- API Embedder（通过 OpenAI 兼容的 Embedding API）----

// APIEmbedder 通过 OpenAI 兼容 Embedding API 获取向量
type APIEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	dim     int
	client  *http.Client
}

// NewAPIEmbedder 创建 API embedder
// 支持 OpenAI、DashScope（通义千问）、Ollama 等兼容 API
func NewAPIEmbedder(baseURL, apiKey, model string) *APIEmbedder {
	if model == "" {
		model = "text-embedding-3-small" // OpenAI 默认
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	if os.Getenv("LLM_SKIP_TLS") == "1" {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	dim := 1536 // OpenAI default
	// DashScope embedding 模型维度不同
	if strings.Contains(model, "text-embedding-v") {
		dim = 1536
	}

	return &APIEmbedder{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		dim:     dim,
		client:  httpClient,
	}
}

func (e *APIEmbedder) Dimension() int { return e.dim }

func (e *APIEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	vecs, err := e.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (e *APIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	reqBody := map[string]any{
		"model": e.model,
		"input": texts,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(e.baseURL, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	results := make([][]float64, len(texts))
	for _, d := range apiResp.Data {
		if d.Index < len(results) {
			results[d.Index] = d.Embedding
			if e.dim == 0 || e.dim != len(d.Embedding) {
				e.dim = len(d.Embedding)
			}
		}
	}

	return results, nil
}

// ---- 公共工具函数 ----

// tokenize 简单分词：按空白和标点分割，转小写
func tokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if isTokenChar(r) {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tok := current.String()
				if utf8.RuneCountInString(tok) >= 2 { // 过滤单字符
					tokens = append(tokens, tok)
				}
				current.Reset()
			}
			// CJK 字符逐字作为 token
			if isCJK(r) {
				tokens = append(tokens, string(r))
			}
		}
	}
	if current.Len() > 0 {
		tok := current.String()
		if utf8.RuneCountInString(tok) >= 2 {
			tokens = append(tokens, tok)
		}
	}

	return tokens
}

func isTokenChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}

// hashToDim 将字符串 hash 到 [0, dim) 的维度
func hashToDim(s string, dim int) int {
	h := uint64(0)
	for _, r := range s {
		h = h*31 + uint64(r)
	}
	return int(h % uint64(dim))
}

// normalize L2 归一化
func normalize(vec []float64) {
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
}

// CosineSimilarity 计算两个向量的余弦相似度
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
