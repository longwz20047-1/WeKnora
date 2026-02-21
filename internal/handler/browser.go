// Package handler provides HTTP handlers for the WeKnora API.
package handler

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/gin-gonic/gin"
	readability "github.com/go-shiori/go-readability"
	"github.com/google/uuid"

	drclient "github.com/Tencent/WeKnora/docreader/client"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// htmlToMarkdown converts an HTML string to Markdown using go-readability for content
// extraction and html-to-markdown/v2 for conversion. Returns the markdown text,
// the page title (empty if extraction fails), and any conversion error.
func htmlToMarkdown(htmlContent string, pageURL string) (markdown string, title string, err error) {
	parsedURL, _ := url.Parse(pageURL)

	// Try readability for article extraction and title.
	article, readErr := readability.FromReader(strings.NewReader(htmlContent), parsedURL)
	if readErr == nil {
		title = article.Title
	}

	// Convert readability's article content to Markdown first.
	if readErr == nil && article.Content != "" {
		md, convErr := htmltomarkdown.ConvertString(article.Content, converter.WithDomain(pageURL))
		if convErr == nil && len(md) >= 200 {
			return md, title, nil
		}
	}

	// Readability extracted too little (app pages, settings pages, etc.).
	// Fall back to converting the full HTML.
	markdown, err = htmltomarkdown.ConvertString(htmlContent, converter.WithDomain(pageURL))
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

	// Screencast: latest frame protected by frameMu
	frameMu   sync.RWMutex
	frameData string // base64-encoded JPEG
	frameSeq  int64  // incrementing sequence number
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
	cfg          *config.Config
	kgService    interfaces.KnowledgeService
	kbService    interfaces.KnowledgeBaseService
	modelService interfaces.ModelService
	docReader    *drclient.Client
	sessions     sessionStore
}

// NewBrowserHandler creates a new BrowserHandler.
func NewBrowserHandler(
	cfg *config.Config,
	kgService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
	modelService interfaces.ModelService,
	docReader *drclient.Client,
) *BrowserHandler {
	return &BrowserHandler{
		cfg:          cfg,
		kgService:    kgService,
		kbService:    kbService,
		modelService: modelService,
		docReader:    docReader,
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
		chromedp.EmulateViewport(1440, 900),
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

	// Set up screencast: listen for frames and start streaming.
	sess := &SessionInfo{
		TargetID:    tid,
		AllocCtx:    allocCtx,
		AllocCancel: allocCancel,
		TabCtx:      tabCtx,
		TabCancel:   tabCancel,
		CreatedAt:   time.Now(),
	}

	chromedp.ListenTarget(tabCtx, func(ev interface{}) {
		if frame, ok := ev.(*page.EventScreencastFrame); ok {
			sess.frameMu.Lock()
			sess.frameData = frame.Data
			sess.frameSeq++
			sess.frameMu.Unlock()
			// Acknowledge the frame asynchronously to avoid blocking the event loop.
			go func(sid int64) {
				_ = chromedp.Run(tabCtx, page.ScreencastFrameAck(sid))
			}(frame.SessionID)
		}
	})

	if scErr := chromedp.Run(tabCtx,
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(60).
			WithMaxWidth(1440).
			WithMaxHeight(900),
	); scErr != nil {
		logger.Warnf(ctx, "BrowserHandler.CreateSession: StartScreencast failed (preview unavailable): %v", scErr)
	}

	// Fallback: build DevTools inspector URL from ExternalURL.
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
	h.sessions.Set(sessionID, sess)

	logger.Infof(ctx, "BrowserHandler.CreateSession: session=%s target=%s screencast=started", sessionID, tid)
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
	// Use a context that preserves request values (tenant, user) but is NOT
	// canceled when the HTTP client disconnects.  This prevents long-running
	// DocReader OCR calls from being aborted by a frontend timeout.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Minute)
	defer cancel()

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

	// Reuse the existing tab context for capture commands.
	// Creating a new context with WithTargetID fails on Browserless because the
	// remote allocator's new WebSocket session cannot see targets from an earlier session.
	captureCtx := sess.TabCtx

	var results []captureResultItem

	// ── Text extraction via go-readability + html-to-markdown ──
	if req.ExtractText {
		result := h.captureText(ctx, captureCtx, req)
		results = append(results, result)
	}

	// ── Screenshot OCR ──
	if req.ScreenshotOCR {
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

// captureScreenshotOCR takes a full-page screenshot, saves it as image knowledge,
// and lets the existing processChunks pipeline handle OCR (PaddleOCR + VLM).
func (h *BrowserHandler) captureScreenshotOCR(
	ctx context.Context,
	captureCtx context.Context,
	req captureRequest,
) captureResultItem {
	var screenshotBuf []byte
	var currentURL string

	if err := chromedp.Run(captureCtx,
		chromedp.Location(&currentURL),
		chromedp.FullScreenshot(&screenshotBuf, 50), // JPEG quality 50
	); err != nil {
		logger.Errorf(ctx, "captureScreenshotOCR: chromedp run failed: %v", err)
		return captureResultItem{Method: "screenshot_ocr", Success: false, Error: "截图失败: " + err.Error()}
	}

	logger.Infof(ctx, "captureScreenshotOCR: raw screenshot size=%d bytes, url=%s", len(screenshotBuf), currentURL)

	// Compress large screenshots so they stay within DocReader limits.
	const maxImageBytes = 900 * 1024
	if len(screenshotBuf) > maxImageBytes {
		compressed, compErr := compressJPEG(screenshotBuf, maxImageBytes)
		if compErr != nil {
			logger.Warnf(ctx, "captureScreenshotOCR: compress failed: %v, using original", compErr)
		} else {
			logger.Infof(ctx, "captureScreenshotOCR: compressed %d -> %d bytes", len(screenshotBuf), len(compressed))
			screenshotBuf = compressed
		}
	}

	if currentURL == "" {
		currentURL = req.CurrentURL
	}

	// Build a descriptive file name.
	fileName := fmt.Sprintf("screenshot_%s.jpg", time.Now().Format("20060102_150405"))

	// Create image knowledge — uses the same pipeline as direct image uploads
	// (DocReader parse → processChunks with PaddleOCR).
	kg, createErr := h.kgService.CreateKnowledgeFromImageBytes(ctx, req.KnowledgeBaseID, screenshotBuf, fileName, req.TagID)
	if createErr != nil {
		logger.Errorf(ctx, "captureScreenshotOCR: CreateKnowledgeFromImageBytes failed: %v", createErr)
		return captureResultItem{Method: "screenshot_ocr", Success: false, Error: "创建知识失败: " + createErr.Error()}
	}

	logger.Infof(ctx, "captureScreenshotOCR: created image knowledge id=%s, fileName=%s", kg.ID, fileName)

	return captureResultItem{
		Method:      "screenshot_ocr",
		Success:     true,
		KnowledgeID: kg.ID,
		ContentLen:  len(screenshotBuf),
		Message:     "截图已提交，OCR 正在后台处理",
	}
}

// compressJPEG re-encodes a JPEG image at progressively lower quality until it
// fits within maxBytes. If quality 15 is still too large, it halves the image
// dimensions and tries again.
func compressJPEG(data []byte, maxBytes int) ([]byte, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode jpeg: %w", err)
	}

	// Try progressively lower quality.
	for _, q := range []int{40, 30, 20, 15} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, fmt.Errorf("encode jpeg q=%d: %w", q, err)
		}
		if buf.Len() <= maxBytes {
			return buf.Bytes(), nil
		}
	}

	// Still too large — halve the dimensions and retry at quality 30.
	bounds := img.Bounds()
	halfW, halfH := bounds.Dx()/2, bounds.Dy()/2
	resized := image.NewRGBA(image.Rect(0, 0, halfW, halfH))
	// Simple nearest-neighbor downscale.
	for y := 0; y < halfH; y++ {
		for x := 0; x < halfW; x++ {
			resized.Set(x, y, img.At(bounds.Min.X+x*2, bounds.Min.Y+y*2))
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 30}); err != nil {
		return nil, fmt.Errorf("encode resized jpeg: %w", err)
	}
	return buf.Bytes(), nil
}

// ─── ScreenStream (SSE) ─────────────────────────────────────────────────────

// ScreenStream godoc
// @Summary      获取浏览器屏幕流
// @Description  通过 SSE 推送 Browserless 页面的 screencast JPEG 帧（base64）
// @Tags         浏览器采集
// @Produce      text/event-stream
// @Param        id  path  string  true  "会话 ID"
// @Success      200  {string}  string  "SSE 帧流"
// @Failure      404  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /browser/screen/{id} [get]
func (h *BrowserHandler) ScreenStream(c *gin.Context) {
	id := c.Param("id")
	sess, ok := h.sessions.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	reqCtx := c.Request.Context()
	ticker := time.NewTicker(50 * time.Millisecond) // ~20 fps polling
	defer ticker.Stop()

	var lastSeq int64
	for {
		select {
		case <-reqCtx.Done():
			return
		case <-sess.TabCtx.Done():
			return
		case <-ticker.C:
			sess.frameMu.RLock()
			seq := sess.frameSeq
			data := sess.frameData
			sess.frameMu.RUnlock()

			if seq > lastSeq && data != "" {
				lastSeq = seq
				fmt.Fprintf(c.Writer, "data: %s\n\n", data)
				c.Writer.Flush()
			}
		}
	}
}

// ─── SendInput ───────────────────────────────────────────────────────────────

type browserInputEvent struct {
	Type      string  `json:"type" binding:"required"` // mousemove|mousedown|mouseup|wheel|keydown|keyup
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Button    string  `json:"button"`
	DeltaX    float64 `json:"deltaX"`
	DeltaY    float64 `json:"deltaY"`
	Key       string  `json:"key"`
	Code      string  `json:"code"`
	Text      string  `json:"text"`
	Modifiers int64   `json:"modifiers"`
}

// SendInput godoc
// @Summary      转发用户输入到浏览器
// @Description  将鼠标/键盘事件通过 CDP 转发到 Browserless 页面
// @Tags         浏览器采集
// @Accept       json
// @Param        id       path  string            true  "会话 ID"
// @Param        request  body  browserInputEvent  true  "输入事件"
// @Success      204
// @Failure      400  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /browser/input/{id} [post]
func (h *BrowserHandler) SendInput(c *gin.Context) {
	id := c.Param("id")
	sess, ok := h.sessions.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
		return
	}

	var ev browserInputEvent
	if err := c.ShouldBindJSON(&ev); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的输入事件"})
		return
	}

	var action chromedp.Action
	btn := mapMouseButton(ev.Button)

	switch ev.Type {
	case "mousemove":
		action = input.DispatchMouseEvent(input.MouseMoved, ev.X, ev.Y).
			WithButton(btn).
			WithModifiers(input.Modifier(ev.Modifiers))
	case "mousedown":
		action = input.DispatchMouseEvent(input.MousePressed, ev.X, ev.Y).
			WithButton(btn).
			WithClickCount(1).
			WithModifiers(input.Modifier(ev.Modifiers))
	case "mouseup":
		action = input.DispatchMouseEvent(input.MouseReleased, ev.X, ev.Y).
			WithButton(btn).
			WithClickCount(1).
			WithModifiers(input.Modifier(ev.Modifiers))
	case "wheel":
		action = input.DispatchMouseEvent(input.MouseWheel, ev.X, ev.Y).
			WithDeltaX(ev.DeltaX).
			WithDeltaY(ev.DeltaY).
			WithModifiers(input.Modifier(ev.Modifiers))
	case "keydown":
		action = input.DispatchKeyEvent(input.KeyDown).
			WithKey(ev.Key).
			WithCode(ev.Code).
			WithText(ev.Text).
			WithModifiers(input.Modifier(ev.Modifiers))
	case "keyup":
		action = input.DispatchKeyEvent(input.KeyUp).
			WithKey(ev.Key).
			WithCode(ev.Code).
			WithModifiers(input.Modifier(ev.Modifiers))
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的事件类型: " + ev.Type})
		return
	}

	if err := chromedp.Run(sess.TabCtx, action); err != nil {
		logger.Warnf(c.Request.Context(), "BrowserHandler.SendInput: %s failed: %v", ev.Type, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// mapMouseButton converts a JS button name to a CDP MouseButton constant.
func mapMouseButton(btn string) input.MouseButton {
	switch btn {
	case "left":
		return input.Left
	case "right":
		return input.Right
	case "middle":
		return input.Middle
	default:
		return input.None
	}
}
