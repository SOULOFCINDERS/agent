package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ---------- 飞书 Token 管理 ----------

type feishuAuth struct {
	mu          sync.Mutex
	appID       string
	appSecret   string
	token       string
	expireAt    time.Time
	client      *http.Client
}

func newFeishuAuth(appID, appSecret string) *feishuAuth {
	return &feishuAuth{
		appID:     appID,
		appSecret: appSecret,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *feishuAuth) getToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.token != "" && time.Now().Before(a.expireAt) {
		return a.token, nil
	}

	body, _ := json.Marshal(map[string]string{
		"app_id":     a.appID,
		"app_secret": a.appSecret,
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch tenant_access_token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("feishu auth error: %s", result.Msg)
	}

	a.token = result.TenantAccessToken
	// 提前 5 分钟过期
	a.expireAt = time.Now().Add(time.Duration(result.Expire-300) * time.Second)
	return a.token, nil
}

// ---------- 飞书文档读取工具 ----------

type FeishuReadDocTool struct {
	auth *feishuAuth
}

func NewFeishuReadDocTool() *FeishuReadDocTool {
	appID := os.Getenv("FEISHU_APP_ID")
	appSecret := os.Getenv("FEISHU_APP_SECRET")
	return &FeishuReadDocTool{
		auth: newFeishuAuth(appID, appSecret),
	}
}

func (t *FeishuReadDocTool) Name() string { return "feishu_read_doc" }

func (t *FeishuReadDocTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	docID, _ := pickString(args, "document_id", "doc_id", "input")
	docID = strings.TrimSpace(docID)
	if docID == "" {
		return nil, fmt.Errorf("missing document_id")
	}

	// 如果传入的是完整 URL，提取 document_id
	docID = extractDocID(docID)

	token, err := t.auth.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth failed: %w", err)
	}

	url := fmt.Sprintf("https://open.feishu.cn/open-apis/docx/v1/documents/%s/raw_content", docID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := t.auth.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch doc: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("feishu API error (code %d): %s", result.Code, result.Msg)
	}

	return result.Data.Content, nil
}

// ---------- 飞书文档创建工具 ----------

type FeishuCreateDocTool struct {
	auth *feishuAuth
}

func NewFeishuCreateDocTool() *FeishuCreateDocTool {
	appID := os.Getenv("FEISHU_APP_ID")
	appSecret := os.Getenv("FEISHU_APP_SECRET")
	return &FeishuCreateDocTool{
		auth: newFeishuAuth(appID, appSecret),
	}
}

func (t *FeishuCreateDocTool) Name() string { return "feishu_create_doc" }

func (t *FeishuCreateDocTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	title, _ := pickString(args, "title", "input")
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("missing title")
	}

	folderToken, _ := pickString(args, "folder_token")

	token, err := t.auth.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth failed: %w", err)
	}

	payload := map[string]any{"title": title}
	if folderToken != "" {
		payload["folder_token"] = folderToken
	}

	body, _ := json.Marshal(payload)
	url := "https://open.feishu.cn/open-apis/docx/v1/documents"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.auth.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create doc: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Document struct {
				DocumentID string `json:"document_id"`
				Title      string `json:"title"`
			} `json:"document"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("feishu API error (code %d): %s", result.Code, result.Msg)
	}

	return map[string]any{
		"document_id": result.Data.Document.DocumentID,
		"title":       result.Data.Document.Title,
		"url":         fmt.Sprintf("https://bytedance.larkoffice.com/docx/%s", result.Data.Document.DocumentID),
	}, nil
}

// ---------- 辅助函数 ----------

// extractDocID 从飞书 URL 中提取 document_id
// 支持格式:
//   https://xxx.larkoffice.com/docx/ABC123
//   https://xxx.larkoffice.com/wiki/ABC123
//   ABC123 (直接返回)
func extractDocID(input string) string {
	input = strings.TrimSpace(input)
	// 尝试从 URL 中提取
	for _, prefix := range []string{"/docx/", "/wiki/", "/docs/"} {
		if idx := strings.Index(input, prefix); idx >= 0 {
			id := input[idx+len(prefix):]
			// 去掉 query string 和 fragment
			if q := strings.IndexAny(id, "?#"); q >= 0 {
				id = id[:q]
			}
			return strings.TrimRight(id, "/")
		}
	}
	return input
}
