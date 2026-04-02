package tools

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ---------- WebFetchTool ----------

// WebFetchTool 抓取网页内容，提取正文纯文本
type WebFetchTool struct {
	client *http.Client
}

func NewWebFetchTool() *WebFetchTool {
	httpClient := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	if os.Getenv("LLM_SKIP_TLS") == "1" {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	return &WebFetchTool{client: httpClient}
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	rawURL := ""
	if v, ok := args["url"]; ok {
		rawURL = fmt.Sprint(v)
	} else if v, ok := args["input"]; ok {
		rawURL = fmt.Sprint(v)
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("missing url parameter")
	}

	// 自动补全 scheme
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = "https://" + rawURL
	}

	maxChars := 6000
	if v, ok := args["max_chars"]; ok {
		switch n := v.(type) {
		case float64:
			maxChars = int(n)
		case int:
			maxChars = n
		}
	}
	if maxChars <= 0 || maxChars > 20000 {
		maxChars = 6000
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d for %s", resp.StatusCode, rawURL)
	}

	// 限制读取大小（最多 1MB）
	limitReader := io.LimitReader(resp.Body, 1024*1024)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	text := ""

	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml") {
		text = extractTextFromHTML(string(body))
	} else if strings.Contains(contentType, "text/") || strings.Contains(contentType, "json") {
		text = string(body)
	} else {
		return fmt.Sprintf("[Binary content: %s, size: %d bytes]", contentType, len(body)), nil
	}

	// 确保是合法的 UTF-8
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "")
	}

	// 截断
	runes := []rune(text)
	if len(runes) > maxChars {
		text = string(runes[:maxChars]) + "\n\n... [truncated]"
	}

	if text == "" {
		return fmt.Sprintf("[Page at %s returned no readable text content]", rawURL), nil
	}

	return fmt.Sprintf("Content from %s:\n\n%s", rawURL, text), nil
}

// extractTextFromHTML 从 HTML 中提取可读文本
func extractTextFromHTML(html string) string {
	// 移除 script 和 style
	scriptRe := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRe.ReplaceAllString(html, "")
	styleRe := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRe.ReplaceAllString(html, "")

	// 移除 head
	headRe := regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
	html = headRe.ReplaceAllString(html, "")

	// 移除 nav、header、footer（通常是导航和页脚）
	navRe := regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`)
	html = navRe.ReplaceAllString(html, "")

	// 块级标签换行
	blockTags := regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|article|section|br|hr)[^>]*>`)
	html = blockTags.ReplaceAllString(html, "\n")

	// <br> 和 <br/> 也换行
	brRe := regexp.MustCompile(`(?i)<br\s*/?>`)
	html = brRe.ReplaceAllString(html, "\n")

	// 列表项加前缀
	liRe := regexp.MustCompile(`(?i)<li[^>]*>`)
	html = liRe.ReplaceAllString(html, "\n• ")

	// 提取链接文字和 URL
	linkRe := regexp.MustCompile(`(?i)<a[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	html = linkRe.ReplaceAllStringFunc(html, func(match string) string {
		m := linkRe.FindStringSubmatch(match)
		if len(m) >= 3 {
			text := stripHTMLTags(m[2])
			if text != "" {
				return text
			}
		}
		return ""
	})

	// 移除所有剩余 HTML 标签
	html = stripHTMLTags(html)

	// 解码 HTML 实体
	html = decodeHTMLEntities(html)

	// 清理空白
	lines := strings.Split(html, "\n")
	var cleaned []string
	blankCount := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 合并连续空格
		spaceRe := regexp.MustCompile(`\s+`)
		line = spaceRe.ReplaceAllString(line, " ")

		if line == "" {
			blankCount++
			if blankCount <= 2 {
				cleaned = append(cleaned, "")
			}
		} else {
			blankCount = 0
			cleaned = append(cleaned, line)
		}
	}

	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func stripHTMLTags(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(s, "")
}

func decodeHTMLEntities(s string) string {
	replacer := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", "\"",
		"&#x27;", "'",
		"&#39;", "'",
		"&nbsp;", " ",
		"&mdash;", "—",
		"&ndash;", "–",
		"&hellip;", "…",
		"&laquo;", "«",
		"&raquo;", "»",
		"&copy;", "©",
		"&reg;", "®",
		"&trade;", "™",
	)
	return replacer.Replace(s)
}
