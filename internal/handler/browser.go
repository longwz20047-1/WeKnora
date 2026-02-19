// Package handler provides HTTP handlers for the WeKnora API.
package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/gin-gonic/gin"
	readability "github.com/go-shiori/go-readability"
	"github.com/google/uuid"

	drclient "github.com/Tencent/WeKnora/docreader/client"
	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// htmlToMarkdown converts an HTML string to Markdown using go-readability for content
// extraction and html-to-markdown/v2 for conversion. Returns the markdown text,
// the page title (empty if extraction fails), and any conversion error.
func htmlToMarkdown(htmlContent string, pageURL string) (markdown string, title string, err error) {
	parsedURL, _ := url.Parse(pageURL)

	article, readErr := readability.FromReader(strings.NewReader(htmlContent), parsedURL)
	contentHTML := htmlContent
	if readErr == nil {
		contentHTML = article.Content
		title = article.Title
	}

	markdown, err = htmltomarkdown.ConvertString(contentHTML, converter.WithDomain(pageURL))
	return markdown, title, err
}

// ─── Session store ────────────────────────────────────────────────────────────

// SessionInfo holds the chromedp contexts for an active Browserless session.
// TabCtx (and its cancel) keeps the allocated browser tab alive (owner = true).
// AllocCtx (and its cancel) keeps the underlying WebSocket connection alive.
type SessionInfo struct {
	TargetID    target.ID
	AllocCtx    context.Context    //nolint:containedctx
	AllocCancel context.CancelFunc
	TabCtx      context.Context    //nolint:containedctx
	TabCancel   context.CancelFunc
	CreatedAt   time.Time
}

type sessionStore struct {
	mu   sync.RWMutex
	data map[string]*SessionInfo
}

func (s *sessionStore) Set(id string, info *SessionInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = info
}

func (s *sessionStore) Get(id string) (*SessionInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	info, ok := s.data[id]
	return info, ok
}

func (s *sessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if info, ok := s.data[id]; ok {
		info.TabCancel()
		info.AllocCancel()
		delete(s.data, id)
	}
}

// ─── Handler ─────────────────────────────────────────────────────────────────

// BrowserHandler manages Browserless sessions and page-capture requests.
type BrowserHandler struct {
	cfg       *config.Config
	kgService interfaces.KnowledgeService
	docReader *drclient.Client
	sessions  sessionStore
}

// NewBrowserHandler creates a new BrowserHandler.
func NewBrowserHandler(
	cfg *config.Config,
	kgService interfaces.KnowledgeService,
	docReader *drclient.Client,
) *BrowserHandler {
	return &BrowserHandler{
		cfg:       cfg,
		kgService: kgService,
		docReader: docReader,
		sessions: sessionStore{
			data: make(map[string]*SessionInfo),
		},
	}
}

// browserlessWSURL builds the Browserless v2 WebSocket URL from config.
// It converts http(s):// → ws(s):// and appends the chromium endpoint + token.
func (h *BrowserHandler) browserlessWSURL() (string, error) {
	if h.cfg.Browserless == nil || h.cfg.Browserless.URL == "" {
		return "", fmt.Errorf("browserless is not configured")
	}
	base := h.cfg.Browserless.URL
	// Normalise scheme
	switch {
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + base[len("http://"):]
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + base[len("https://"):]
	}
	wsURL := strings.TrimRight(base, "/") + "/chromium"
	if h.cfg.Browserless.Token != "" {
		wsURL += "?token=" + url.QueryEscape(h.cfg.Browserless.Token)
	}
	return wsURL, nil
}

// ─── CreateSession ────────────────────────────────────────────────────────────

type createSessionRequest struct {
	URL string `json:"url" binding:"required"`
}

type createSessionResponse struct {
	SessionID    string `json:"session_id"`
	DebuggerURL  string `json:"debugger_url"`
	TargetID     string `json:"target_id"`
	InitialURL   string `json:"initial_url"`
}

// CreateSession godoc
// @Summary      创建 Browserless 会话
// @Description  打开 Browserless 无头浏览器并导航到指定 URL，返回会话 ID
// @Tags         浏览器采集
// @Accept       json
// @Produce      json
// @Param        request  body      createSessionRequest   true  "请求体"
// @Success      200      {object}  createSessionResponse
// @Failure      400      {object}  map[string]interface{}
// @Failure      500      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /browser/session [post]
func (h *BrowserHandler) CreateSession(c *gin.Context) {
	ctx := c.Request.Context()

	var req createSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
		return
	}

	wsURL, err := h.browserlessWSURL()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Connect to Browserless via WebSocket allocator.
	// context.Background() is intentional: the session outlives the HTTP request.
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(
		context.Background(), wsURL, chromedp.NoModifyURL,
	)

	tabCtx, tabCancel := chromedp.NewContext(allocCtx)

	// Navigate to the initial URL and capture page info.
	var currentURL string
	if runErr := chromedp.Run(tabCtx,
		chromedp.Navigate(req.URL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Location(&currentURL),
	); runErr != nil {
		tabCancel()
		allocCancel()
		logger.Errorf(ctx, "BrowserHandler.CreateSession: navigate failed: %v", runErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法打开页面，请检查链接或 Browserless 配置"})
		return
	}

	// Retrieve the target ID from Browserless.
	targets, err := chromedp.Targets(tabCtx)
	if err != nil || len(targets) == 0 {
		tabCancel()
		allocCancel()
		logger.Errorf(ctx, "BrowserHandler.CreateSession: get targets failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "获取浏览器 Target 失败"})
		return
	}

	// Pick the first page target owned by this context.
	var tid target.ID
	for _, t := range targets {
		if t.Type == "page" {
			tid = t.TargetID
			break
		}
	}
	if tid == "" {
		tabCancel()
		allocCancel()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "未找到页面 Target"})
		return
	}

	// Build debugger URL from ExternalURL (for the browser DevTools UI).
	debuggerURL := ""
	if h.cfg.Browserless != nil && h.cfg.Browserless.ExternalURL != "" {
		ext := strings.TrimRight(h.cfg.Browserless.ExternalURL, "/")
		debuggerURL = fmt.Sprintf("%s/devtools/inspector.html?ws=%s/devtools/page/%s",
			ext,
			strings.TrimPrefix(strings.TrimPrefix(ext, "https://"), "http://"),
			string(tid),
		)
	}

	sessionID := uuid.New().String()
	h.sessions.Set(sessionID, &SessionInfo{
		TargetID:    tid,
		AllocCtx:    allocCtx,
		AllocCancel: allocCancel,
		TabCtx:      tabCtx,
		TabCancel:   tabCancel,
		CreatedAt:   time.Now(),
	})

	logger.Infof(ctx, "BrowserHandler.CreateSession: session=%s target=%s", sessionID, tid)
	c.JSON(http.StatusOK, createSessionResponse{
		SessionID:   sessionID,
		DebuggerURL: debuggerURL,
		TargetID:    string(tid),
		InitialURL:  currentURL,
	})
}

// ─── CloseSession ─────────────────────────────────────────────────────────────

// CloseSession godoc
// @Summary      关闭 Browserless 会话
// @Description  关闭指定的 Browserless 会话并释放资源
// @Tags         浏览器采集
// @Param        id  path  string  true  "会话 ID"
// @Success      204
// @Failure      404  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /browser/session/{id} [delete]
func (h *BrowserHandler) CloseSession(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")

	if _, ok := h.sessions.Get(id); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
		return
	}

	h.sessions.Delete(id)
	logger.Infof(ctx, "BrowserHandler.CloseSession: session=%s closed", id)
	c.Status(http.StatusNoContent)
}

// ─── Capture ──────────────────────────────────────────────────────────────────

type captureRequest struct {
	SessionID          string `json:"session_id" binding:"required"`
	KnowledgeBaseID    string `json:"knowledge_base_id" binding:"required"`
	TagID              string `json:"tag_id"`
	Title              string `json:"title"`
	CurrentURL         string `json:"current_url"`
	ExtractText        bool   `json:"extract_text"`
	ScreenshotOCR      bool   `json:"screenshot_ocr"`
	ReplaceKnowledgeID string `json:"replace_knowledge_id"`
}

type captureResultItem struct {
	Method      string `json:"method"`
	Success     bool   `json:"success"`
	KnowledgeID string `json:"knowledge_id,omitempty"`
	ContentLen  int    `json:"content_len,omitempty"`
	Message     string `json:"message,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Capture godoc
// @Summary      采集当前页面内容
// @Description  从已建立的 Browserless 会话中提取当前页面的文本/截图并存入知识库
// @Tags         浏览器采集
// @Accept       json
// @Produce      json
// @Param        request  body      captureRequest  true  "采集请求"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  map[string]interface{}
// @Failure      404      {object}  map[string]interface{}
// @Security     Bearer
// @Router       /browser/capture [post]
func (h *BrowserHandler) Capture(c *gin.Context) {
	ctx := c.Request.Context()

	var req captureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
		return
	}

	sess, ok := h.sessions.Get(req.SessionID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在或已过期"})
		return
	}

	// Attach to the existing target without claiming ownership.
	captureCtx, captureCancel := chromedp.NewContext(
		sess.AllocCtx,
		chromedp.WithTargetID(sess.TargetID),
	)
	defer captureCancel()

	var results []captureResultItem

	// ── Text extraction via go-readability + html-to-markdown ──
	if req.ExtractText {
		result := h.captureText(ctx, captureCtx, req)
		results = append(results, result)
	}

	// ── Screenshot OCR via DocReader ──
	if req.ScreenshotOCR && h.docReader != nil {
		result := h.captureScreenshotOCR(ctx, captureCtx, req)
		results = append(results, result)
	}

	if len(results) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要选择一种采集方式（extract_text 或 screenshot_ocr）"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// captureText extracts page HTML, converts to Markdown, and stores as knowledge.
func (h *BrowserHandler) captureText(
	ctx context.Context,
	captureCtx context.Context,
	req captureRequest,
) captureResultItem {
	var htmlContent, currentURL string

	if err := chromedp.Run(captureCtx,
		chromedp.Location(&currentURL),
		chromedp.OuterHTML("html", &htmlContent, chromedp.ByQuery),
	); err != nil {
		logger.Errorf(ctx, "captureText: chromedp run failed: %v", err)
		return captureResultItem{Method: "text", Success: false, Error: "获取页面 HTML 失败: " + err.Error()}
	}

	// Use caller-supplied URL as fallback.
	if currentURL == "" {
		currentURL = req.CurrentURL
	}

	md, extractedTitle, err := htmlToMarkdown(htmlContent, currentURL)
	if err != nil {
		logger.Errorf(ctx, "captureText: htmlToMarkdown failed: %v", err)
		return captureResultItem{Method: "text", Success: false, Error: "HTML 转 Markdown 失败: " + err.Error()}
	}

	title := req.Title
	if title == "" {
		title = extractedTitle
	}
	if title == "" {
		title = currentURL
	}

	if req.ReplaceKnowledgeID != "" {
		if replErr := h.kgService.ReplaceKnowledgeContent(ctx, req.ReplaceKnowledgeID, md); replErr != nil {
			logger.Errorf(ctx, "captureText: ReplaceKnowledgeContent failed: %v", replErr)
			return captureResultItem{Method: "text", Success: false, Error: "替换内容失败: " + replErr.Error()}
		}
		return captureResultItem{
			Method:      "text",
			Success:     true,
			KnowledgeID: req.ReplaceKnowledgeID,
			ContentLen:  len(md),
			Message:     "内容已更新",
		}
	}

	kg, createErr := h.kgService.CreateKnowledgeFromExtracted(ctx, req.KnowledgeBaseID, title, md, req.TagID)
	if createErr != nil {
		logger.Errorf(ctx, "captureText: CreateKnowledgeFromExtracted failed: %v", createErr)
		return captureResultItem{Method: "text", Success: false, Error: "创建知识失败: " + createErr.Error()}
	}

	return captureResultItem{
		Method:      "text",
		Success:     true,
		KnowledgeID: kg.ID,
		ContentLen:  len(md),
		Message:     "采集成功",
	}
}

// captureScreenshotOCR takes a full-page screenshot, sends it to DocReader for
// OCR, and stores the extracted text as knowledge.
func (h *BrowserHandler) captureScreenshotOCR(
	ctx context.Context,
	captureCtx context.Context,
	req captureRequest,
) captureResultItem {
	var screenshotBuf []byte
	var currentURL string

	if err := chromedp.Run(captureCtx,
		chromedp.Location(&currentURL),
		chromedp.FullScreenshot(&screenshotBuf, 90),
	); err != nil {
		logger.Errorf(ctx, "captureScreenshotOCR: chromedp run failed: %v", err)
		return captureResultItem{Method: "screenshot_ocr", Success: false, Error: "截图失败: " + err.Error()}
	}

	if currentURL == "" {
		currentURL = req.CurrentURL
	}

	// Send screenshot bytes to DocReader for OCR.
	resp, err := h.docReader.ReadFromFile(ctx, buildScreenshotReadRequest(screenshotBuf))
	if err != nil {
		logger.Errorf(ctx, "captureScreenshotOCR: DocReader OCR failed: %v", err)
		return captureResultItem{Method: "screenshot_ocr", Success: false, Error: "OCR 失败: " + err.Error()}
	}

	// Collect text from returned chunks.
	var sb strings.Builder
	for _, chunk := range resp.GetChunks() {
		sb.WriteString(chunk.GetContent())
		sb.WriteString("\n\n")
	}
	text := strings.TrimSpace(sb.String())

	title := req.Title
	if title == "" {
		title = currentURL
	}

	if req.ReplaceKnowledgeID != "" {
		if replErr := h.kgService.ReplaceKnowledgeContent(ctx, req.ReplaceKnowledgeID, text); replErr != nil {
			return captureResultItem{Method: "screenshot_ocr", Success: false, Error: "替换内容失败: " + replErr.Error()}
		}
		return captureResultItem{
			Method:      "screenshot_ocr",
			Success:     true,
			KnowledgeID: req.ReplaceKnowledgeID,
			ContentLen:  len(text),
			Message:     "内容已更新（OCR）",
		}
	}

	kg, createErr := h.kgService.CreateKnowledgeFromExtracted(ctx, req.KnowledgeBaseID, title, text, req.TagID)
	if createErr != nil {
		return captureResultItem{Method: "screenshot_ocr", Success: false, Error: "创建知识失败: " + createErr.Error()}
	}

	return captureResultItem{
		Method:      "screenshot_ocr",
		Success:     true,
		KnowledgeID: kg.ID,
		ContentLen:  len(text),
		Message:     "截图 OCR 采集成功",
	}
}

// buildScreenshotReadRequest creates a DocReader ReadFromFileRequest for PNG screenshot bytes.
func buildScreenshotReadRequest(screenshotBytes []byte) *proto.ReadFromFileRequest {
	return &proto.ReadFromFileRequest{
		FileContent: screenshotBytes,
		FileName:    "screenshot.png",
		FileType:    "png",
		ReadConfig: &proto.ReadConfig{
			ChunkSize:    500,
			ChunkOverlap: 50,
		},
	}
}
