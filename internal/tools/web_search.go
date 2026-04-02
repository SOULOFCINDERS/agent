package tools

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// ---------- WebSearchTool ----------

// WebSearchTool 联网搜索工具
// 支持多种搜索后端：
//   - DuckDuckGo HTML（默认，无需 API key）
//   - SerpAPI（设置 SERPAPI_KEY 环境变量）
type WebSearchTool struct {
	client *http.Client
}

func NewWebSearchTool() *WebSearchTool {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	if os.Getenv("LLM_SKIP_TLS") == "1" {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return &WebSearchTool{client: httpClient}
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	query := ""
	if v, ok := args["query"]; ok {
		query = fmt.Sprint(v)
	} else if v, ok := args["input"]; ok {
		query = fmt.Sprint(v)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("missing query parameter")
	}

	maxResults := 5
	if v, ok := args["max_results"]; ok {
		switch n := v.(type) {
		case float64:
			maxResults = int(n)
		case int:
			maxResults = n
		}
	}
	if maxResults <= 0 || maxResults > 10 {
		maxResults = 5
	}

	// 优先使用 SerpAPI（如果配置了 key）
	if serpKey := os.Getenv("SERPAPI_KEY"); serpKey != "" {
		return t.searchSerpAPI(ctx, query, maxResults, serpKey)
	}

	// 默认使用 DuckDuckGo HTML 解析
	return t.searchDuckDuckGo(ctx, query, maxResults)
}

// SearchResult 搜索结果
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// ---------- DuckDuckGo HTML ----------

func (t *WebSearchTool) searchDuckDuckGo(ctx context.Context, query string, maxResults int) (any, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	html := string(body)
	results := parseDuckDuckGoHTML(html, maxResults)

	if len(results) == 0 {
		return fmt.Sprintf("No results found for: %s", query), nil
	}

	return formatSearchResults(query, results), nil
}

// parseDuckDuckGoHTML 从 DuckDuckGo HTML 页面提取搜索结果
func parseDuckDuckGoHTML(html string, maxResults int) []SearchResult {
	var results []SearchResult

	// 匹配结果块: <a class="result__a" href="...">title</a>
	// 和 snippet: <a class="result__snippet" ...>snippet</a>
	linkRe := regexp.MustCompile(`<a\s+[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	snippetRe := regexp.MustCompile(`<a\s+[^>]*class="result__snippet"[^>]*>(.*?)</a>`)

	linkMatches := linkRe.FindAllStringSubmatch(html, maxResults*2)
	snippetMatches := snippetRe.FindAllStringSubmatch(html, maxResults*2)

	for i, m := range linkMatches {
		if len(results) >= maxResults {
			break
		}

		rawURL := m[1]
		title := stripHTML(m[2])

		// DuckDuckGo 的链接是重定向URL，提取真实URL
		realURL := extractDDGRealURL(rawURL)
		if realURL == "" {
			realURL = rawURL
		}

		snippet := ""
		if i < len(snippetMatches) {
			snippet = stripHTML(snippetMatches[i][1])
		}

		if title != "" && realURL != "" {
			results = append(results, SearchResult{
				Title:   title,
				URL:     realURL,
				Snippet: snippet,
			})
		}
	}

	return results
}

// extractDDGRealURL 从 DuckDuckGo 重定向 URL 提取真实 URL
func extractDDGRealURL(ddgURL string) string {
	// DuckDuckGo 使用 //duckduckgo.com/l/?uddg=ENCODED_URL&... 格式
	if strings.Contains(ddgURL, "uddg=") {
		u, err := url.Parse(ddgURL)
		if err != nil {
			return ""
		}
		realURL := u.Query().Get("uddg")
		if realURL != "" {
			return realURL
		}
	}
	return ddgURL
}

// ---------- SerpAPI ----------

func (t *WebSearchTool) searchSerpAPI(ctx context.Context, query string, maxResults int, apiKey string) (any, error) {
	searchURL := fmt.Sprintf(
		"https://serpapi.com/search.json?q=%s&num=%d&api_key=%s&engine=google",
		url.QueryEscape(query), maxResults, apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serpapi request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("serpapi error (status %d): %s", resp.StatusCode, string(body))
	}

	var serpResp struct {
		OrganicResults []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic_results"`
		AnswerBox *struct {
			Answer  string `json:"answer"`
			Snippet string `json:"snippet"`
			Title   string `json:"title"`
			Link    string `json:"link"`
		} `json:"answer_box"`
	}
	if err := json.Unmarshal(body, &serpResp); err != nil {
		return nil, fmt.Errorf("parse serpapi response: %w", err)
	}

	var results []SearchResult

	// 如果有 answer box，优先添加
	if serpResp.AnswerBox != nil {
		ab := serpResp.AnswerBox
		snippet := ab.Answer
		if snippet == "" {
			snippet = ab.Snippet
		}
		results = append(results, SearchResult{
			Title:   ab.Title,
			URL:     ab.Link,
			Snippet: snippet,
		})
	}

	for _, r := range serpResp.OrganicResults {
		if len(results) >= maxResults {
			break
		}
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.Link,
			Snippet: r.Snippet,
		})
	}

	if len(results) == 0 {
		return fmt.Sprintf("No results found for: %s", query), nil
	}

	return formatSearchResults(query, results), nil
}

// ---------- 工具函数 ----------

// stripHTML 移除 HTML 标签
func stripHTML(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, "")
	// 解码常见 HTML 实体
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#x27;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return strings.TrimSpace(s)
}

// formatSearchResults 格式化搜索结果为可读文本
func formatSearchResults(query string, results []SearchResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for \"%s\" (%d results):\n\n", query, len(results)))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("    URL: %s\n", r.URL))
		if r.Snippet != "" {
			sb.WriteString(fmt.Sprintf("    %s\n", r.Snippet))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
