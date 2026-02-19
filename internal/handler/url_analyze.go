// Package handler provides HTTP handlers for the WeKnora API.
package handler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// AnalyzeResult 是 URL 分析接口的响应结构
type AnalyzeResult struct {
	URL            string  `json:"url"`
	Reachable      bool    `json:"reachable"`
	ContentType    string  `json:"content_type,omitempty"`
	PageType       string  `json:"page_type,omitempty"`
	Title          string  `json:"title,omitempty"`
	Description    string  `json:"description,omitempty"`
	Recommendation string  `json:"recommendation"`
	Reason         string  `json:"reason"`
	Confidence     float64 `json:"confidence"`
}

// isInternalURL 检查 URL 是否指向内网或不允许访问的地址（SSRF 防护）
func isInternalURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return true
	}
	host := u.Hostname()
	if host == "" {
		return true
	}
	// localhost 直接拦截
	if host == "localhost" {
		return true
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		// DNS 解析失败视为内网（保守策略）
		return true
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return true
		}
	}
	return false
}

var analyzeHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

const analyzeUserAgent = "Mozilla/5.0 (compatible; WeKnoraBot/1.0)"

// analyzeURL 对目标 URL 发起 HEAD（降级到 GET）探测，返回分析结果
func analyzeURL(ctx context.Context, rawURL string) AnalyzeResult {
	result := AnalyzeResult{URL: rawURL}

	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		result.Reachable = false
		result.Recommendation = "manual"
		result.Reason = "URL 格式无效，请检查后重试"
		result.Confidence = 0.95
		return result
	}
	headReq.Header.Set("User-Agent", analyzeUserAgent)

	headResp, headErr := analyzeHTTPClient.Do(headReq)
	if headErr != nil || headResp == nil {
		result.Reachable = false
		result.Recommendation = "manual"
		result.Reason = "页面无法访问，建议手动采集"
		result.Confidence = 0.85
		return result
	}
	defer headResp.Body.Close()

	statusCode := headResp.StatusCode
	contentType := headResp.Header.Get("Content-Type")
	// 记录重定向后的最终 URL
	if headResp.Request != nil && headResp.Request.URL != nil {
		result.URL = headResp.Request.URL.String()
	}

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		result.Reachable = true
		result.ContentType = contentType
		result.PageType = "login_required"
		result.Recommendation = "manual"
		result.Reason = fmt.Sprintf("页面需要认证（HTTP %d），需手动采集", statusCode)
		result.Confidence = 0.95
		return result
	case statusCode >= 400:
		result.Reachable = false
		result.Recommendation = "manual"
		result.Reason = fmt.Sprintf("页面返回错误状态（HTTP %d）", statusCode)
		result.Confidence = 0.90
		return result
	case statusCode >= 200 && statusCode < 300:
		// 正常，继续分析
	default:
		result.Reachable = false
		result.Recommendation = "manual"
		result.Reason = fmt.Sprintf("页面状态异常（HTTP %d）", statusCode)
		result.Confidence = 0.80
		return result
	}

	result.Reachable = true
	ct := strings.ToLower(contentType)

	// PDF 文件直接推荐自动采集
	if strings.Contains(ct, "application/pdf") {
		result.ContentType = contentType
		result.PageType = "pdf"
		result.Recommendation = "auto"
		result.Reason = "PDF 文件，推荐自动采集"
		result.Confidence = 0.90
		return result
	}

	// 非 HTML 类型（JSON、二进制等）
	if contentType != "" && !strings.Contains(ct, "text/html") {
		result.ContentType = contentType
		result.PageType = "other"
		result.Recommendation = "auto"
		result.Reason = "可下载内容，推荐自动采集"
		result.Confidence = 0.75
		return result
	}

	// text/html 或无 Content-Type — GET 完整页面做深度分析
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		result.ContentType = contentType
		result.PageType = "static_html"
		result.Recommendation = "auto"
		result.Reason = "页面可访问，推荐自动采集"
		result.Confidence = 0.70
		return result
	}
	getReq.Header.Set("User-Agent", analyzeUserAgent)

	getResp, err := analyzeHTTPClient.Do(getReq)
	if err != nil || getResp == nil {
		result.ContentType = contentType
		result.PageType = "static_html"
		result.Recommendation = "auto"
		result.Reason = "页面可访问，推荐自动采集"
		result.Confidence = 0.70
		return result
	}
	defer getResp.Body.Close()

	// 最多读取 1 MB 用于分析
	body, _ := io.ReadAll(io.LimitReader(getResp.Body, 1<<20))
	htmlResult := analyzeHTMLContent(body)
	htmlResult.URL = result.URL
	if htmlResult.ContentType == "" {
		htmlResult.ContentType = getResp.Header.Get("Content-Type")
	}
	return htmlResult
}

// analyzeHTMLContent 分析 HTML 内容，检测登录页面、Cloudflare 验证等
func analyzeHTMLContent(body []byte) AnalyzeResult {
	html := string(body)
	title := extractTitle(html)
	description := extractMetaDescription(html)

	hasLoginForm := regexp.MustCompile(
		`(?i)<input[^>]+type=["']password["']`).MatchString(html)
	isCloudflareBlock := strings.Contains(html, "cf-browser-verification") ||
		strings.Contains(html, "challenge-platform")

	base := AnalyzeResult{
		Reachable:   true,
		ContentType: "text/html",
		Title:       title,
		Description: description,
	}

	if isCloudflareBlock {
		base.PageType = "login_required"
		base.Recommendation = "manual"
		base.Reason = "检测到安全验证页面（Cloudflare），需手动采集"
		base.Confidence = 0.90
		return base
	}
	if hasLoginForm {
		base.PageType = "login_required"
		base.Recommendation = "manual"
		base.Reason = "检测到登录页面，需手动采集"
		base.Confidence = 0.90
		return base
	}

	base.PageType = "static_html"
	base.Recommendation = "auto"
	base.Reason = "页面可访问，推荐自动采集"
	base.Confidence = 0.80
	return base
}

func extractTitle(html string) string {
	re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func extractMetaDescription(html string) string {
	re := regexp.MustCompile(`(?i)<meta[^>]+name=["']description["'][^>]+content=["']([^"']*)["']`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// AnalyzeURLResult extends AnalyzeResult with duplicate detection info.
type AnalyzeURLResult struct {
	AnalyzeResult
	IsDuplicate          bool   `json:"is_duplicate"`
	ExistingKnowledgeID  string `json:"existing_knowledge_id,omitempty"`
}

// AnalyzeURL godoc
// @Summary      分析 URL 可采集性
// @Description  对目标 URL 进行 HTTP 探测，返回推荐采集方式（auto/manual）。若提供 kb_id 则同时检查是否为重复 URL。
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        request  body      object{url=string,kb_id=string}  true  "URL 请求"
// @Success      200      {object}  AnalyzeURLResult
// @Failure      400      {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/url/analyze [post]
func (h *KnowledgeHandler) AnalyzeURL(c *gin.Context) {
	var req struct {
		URL  string `json:"url" binding:"required,url"`
		KBID string `json:"kb_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 URL"})
		return
	}
	if isInternalURL(req.URL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "不允许访问内网地址"})
		return
	}
	ctx := c.Request.Context()
	result := AnalyzeURLResult{AnalyzeResult: analyzeURL(ctx, req.URL)}

	// If kb_id is provided, check whether this URL already exists in the knowledge base.
	if req.KBID != "" {
		if tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64); ok && tenantID > 0 {
			exists, existing, err := h.kgService.GetRepository().CheckKnowledgeExists(ctx, tenantID, req.KBID, &types.KnowledgeCheckParams{
				Type: "url",
				URL:  req.URL,
			})
			if err == nil && exists && existing != nil {
				result.IsDuplicate = true
				result.ExistingKnowledgeID = existing.ID
			}
		}
	}

	c.JSON(http.StatusOK, result)
}
