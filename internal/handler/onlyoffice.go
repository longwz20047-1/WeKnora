package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// docTypeMap maps file extensions to ONLYOFFICE document types.
var docTypeMap = map[string]string{
	"docx": "word", "doc": "word", "odt": "word", "rtf": "word",
	"txt": "word", "docm": "word", "dotx": "word", "epub": "word",
	"html": "word", "htm": "word", "fb2": "word",
	"xlsx": "cell", "xls": "cell", "ods": "cell", "csv": "cell",
	"xlsm": "cell", "xltx": "cell",
	"pptx": "slide", "ppt": "slide", "odp": "slide",
	"pptm": "slide", "ppsx": "slide", "potx": "slide",
	"pdf": "pdf",
}

// editableTypes lists formats that support editing.
var editableTypes = map[string]bool{
	"docx": true, "xlsx": true, "pptx": true,
	"odt": true, "ods": true, "odp": true,
	"csv": true, "txt": true, "rtf": true,
}

// OnlyOfficeHandler manages ONLYOFFICE DocumentServer integration.
type OnlyOfficeHandler struct {
	cfg       *config.Config
	kgService interfaces.KnowledgeService
	fileSvc   interfaces.FileService
	redis     *redis.Client
}

// NewOnlyOfficeHandler always returns a valid instance (never nil).
func NewOnlyOfficeHandler(
	cfg *config.Config,
	kgService interfaces.KnowledgeService,
	fileSvc interfaces.FileService,
	redis *redis.Client,
) *OnlyOfficeHandler {
	return &OnlyOfficeHandler{
		cfg:       cfg,
		kgService: kgService,
		fileSvc:   fileSvc,
		redis:     redis,
	}
}

// Enabled returns whether ONLYOFFICE integration is configured.
func (h *OnlyOfficeHandler) Enabled() bool {
	return h.cfg.OnlyOffice != nil && h.cfg.OnlyOffice.JWTSecret != ""
}

// generateDocKey creates a document key for ONLYOFFICE caching.
func generateDocKey(knowledgeID string, updatedAt time.Time) string {
	hash := sha256.New()
	hash.Write([]byte(fmt.Sprintf("%s_%d", knowledgeID, updatedAt.UnixNano())))
	return fmt.Sprintf("%s_%s", knowledgeID, hex.EncodeToString(hash.Sum(nil))[:8])
}

// downloadCallbackFile downloads a file with SSRF protection and size limits.
func (h *OnlyOfficeHandler) downloadCallbackFile(downloadURL string) ([]byte, error) {
	client := secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
		Timeout:      5 * time.Minute,
		MaxRedirects: 5,
	})

	resp, err := client.Get(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("download failed (SSRF check may have blocked): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	const maxDownloadSize = 100 << 20 // 100MB
	limitedReader := io.LimitReader(resp.Body, int64(maxDownloadSize)+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(data)) > int64(maxDownloadSize) {
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxDownloadSize)
	}
	return data, nil
}

// withSaveLock acquires a Redis distributed lock for concurrent save safety.
func (h *OnlyOfficeHandler) withSaveLock(ctx context.Context, knowledgeID string, fn func() error) error {
	lockKey := fmt.Sprintf("onlyoffice:save:%s", knowledgeID)
	lockValue := uuid.New().String()

	acquired, err := h.redis.SetNX(ctx, lockKey, lockValue, 10*time.Second).Result()
	if err != nil {
		return fmt.Errorf("redis lock error: %w", err)
	}
	if !acquired {
		time.Sleep(500 * time.Millisecond)
		acquired, err = h.redis.SetNX(ctx, lockKey, lockValue, 10*time.Second).Result()
		if err != nil || !acquired {
			return fmt.Errorf("failed to acquire save lock for knowledge %s", knowledgeID)
		}
	}
	defer func() {
		script := `if redis.call("get",KEYS[1]) == ARGV[1] then return redis.call("del",KEYS[1]) else return 0 end`
		h.redis.Eval(ctx, script, []string{lockKey}, lockValue)
	}()

	return fn()
}

// GetEditorConfig returns ONLYOFFICE editor configuration for a document.
// GET /api/v1/onlyoffice/config/:id?mode=view|edit
func (h *OnlyOfficeHandler) GetEditorConfig(c *gin.Context) {
	if !h.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "ONLYOFFICE not configured"})
		return
	}

	knowledgeID := c.Param("id")
	mode := c.DefaultQuery("mode", "view")
	if mode != "view" && mode != "edit" {
		mode = "view"
	}

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, tenantID)
	userID := c.GetString(types.UserIDContextKey.String())

	knowledge, err := h.kgService.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "knowledge not found"})
		return
	}

	// Use knowledge owner's tenant for file access (handles shared KB)
	effectiveTenantID := knowledge.TenantID

	ext := strings.TrimPrefix(filepath.Ext(knowledge.FileName), ".")
	ext = strings.ToLower(ext)
	docType, ok := docTypeMap[ext]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unsupported file type: %s", ext)})
		return
	}

	canEdit := mode == "edit" && editableTypes[ext]
	effectiveMode := mode
	if !canEdit {
		effectiveMode = "view"
	}

	hmacToken := secutils.GenerateHMACToken(
		h.cfg.OnlyOffice.HMACSecret, knowledgeID, effectiveTenantID, 5*time.Minute,
	)

	fileURL := fmt.Sprintf("%s/api/v1/onlyoffice/file/%s?token=%s",
		h.cfg.OnlyOffice.InternalURL, knowledgeID, hmacToken)
	callbackURL := fmt.Sprintf("%s/api/v1/onlyoffice/callback",
		h.cfg.OnlyOffice.InternalURL)
	docKey := generateDocKey(knowledgeID, knowledge.UpdatedAt)

	editorConfig := map[string]interface{}{
		"document": map[string]interface{}{
			"fileType": ext,
			"key":      docKey,
			"title":    knowledge.FileName,
			"url":      fileURL,
			"permissions": map[string]interface{}{
				"edit":     canEdit,
				"download": true,
				"print":    true,
			},
		},
		"documentType": docType,
		"editorConfig": map[string]interface{}{
			"mode":        effectiveMode,
			"callbackUrl": callbackURL,
			"lang":        "zh",
			"user": map[string]interface{}{
				"id":   userID,
				"name": userID,
			},
			"customization": map[string]interface{}{
				"autosave":      true,
				"forcesave":     true,
				"compactHeader": true,
			},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(editorConfig))
	signedToken, err := token.SignedString([]byte(h.cfg.OnlyOffice.JWTSecret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to sign config"})
		return
	}
	editorConfig["token"] = signedToken

	c.JSON(http.StatusOK, gin.H{
		"config":        editorConfig,
		"onlyofficeUrl": h.cfg.OnlyOffice.ExternalURL,
	})
}

// ServeFile streams a document to ONLYOFFICE with HMAC token auth.
// GET /api/v1/onlyoffice/file/:id?token={hmac_token}
func (h *OnlyOfficeHandler) ServeFile(c *gin.Context) {
	if !h.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "ONLYOFFICE not configured"})
		return
	}

	knowledgeID := c.Param("id")
	token := c.Query("token")

	tokenKID, tokenTenantID, err := secutils.ValidateHMACToken(h.cfg.OnlyOffice.HMACSecret, token)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired token"})
		return
	}
	if tokenKID != knowledgeID {
		c.JSON(http.StatusForbidden, gin.H{"error": "token does not match requested document"})
		return
	}

	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, tokenTenantID)

	reader, filename, err := h.kgService.GetKnowledgeFile(ctx, knowledgeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	defer reader.Close()

	contentType := detectContentType(filename)
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filename}))

	c.Stream(func(w io.Writer) bool {
		if _, err := io.Copy(w, reader); err != nil {
			logger.Warnf(c.Request.Context(), "[ONLYOFFICE] file streaming error for %s: %v", knowledgeID, err)
		}
		return false
	})
}

// onlyofficeCallback represents the callback request from ONLYOFFICE.
type onlyofficeCallback struct {
	Key    string   `json:"key"`
	Status int      `json:"status"`
	URL    string   `json:"url"`
	Users  []string `json:"users"`
	Token  string   `json:"token"`
}

// HandleCallback processes save callbacks from ONLYOFFICE.
// POST /api/v1/onlyoffice/callback
func (h *OnlyOfficeHandler) HandleCallback(c *gin.Context) {
	if !h.Enabled() {
		c.JSON(http.StatusOK, gin.H{"error": 0})
		return
	}

	var cb onlyofficeCallback
	if err := c.ShouldBindJSON(&cb); err != nil {
		c.JSON(http.StatusOK, gin.H{"error": 0})
		return
	}

	ctx := c.Request.Context()

	// Verify ONLYOFFICE JWT (JWT_IN_BODY=true) — mandatory
	if cb.Token == "" {
		logger.Warnf(ctx, "[ONLYOFFICE] callback rejected: missing JWT token, key=%s", cb.Key)
		c.JSON(http.StatusOK, gin.H{"error": 0})
		return
	}
	parser := jwt.NewParser()
	_, err := parser.Parse(cb.Token, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(h.cfg.OnlyOffice.JWTSecret), nil
	})
	if err != nil {
		logger.Warnf(ctx, "[ONLYOFFICE] callback rejected: invalid JWT, key=%s err=%v", cb.Key, err)
		c.JSON(http.StatusOK, gin.H{"error": 0})
		return
	}

	// Status 4 = no changes, just acknowledge
	if cb.Status == 4 {
		c.JSON(http.StatusOK, gin.H{"error": 0})
		return
	}

	// Only process status 2 (save after close) and 6 (force save)
	if cb.Status != 2 && cb.Status != 6 {
		c.JSON(http.StatusOK, gin.H{"error": 0})
		return
	}

	// Extract knowledgeID from document key (format: "{knowledgeID}_{hash}")
	knowledgeID := cb.Key
	if idx := strings.LastIndex(cb.Key, "_"); idx > 0 {
		knowledgeID = cb.Key[:idx]
	}

	logger.Infof(ctx, "[ONLYOFFICE] callback received: status=%d key=%s", cb.Status, cb.Key)

	knowledge, err := h.kgService.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil {
		logger.Warnf(ctx, "[ONLYOFFICE] knowledge not found for callback: id=%s err=%v", knowledgeID, err)
		c.JSON(http.StatusOK, gin.H{"error": 0})
		return
	}

	ctx = context.WithValue(ctx, types.TenantIDContextKey, knowledge.TenantID)

	saveErr := h.withSaveLock(ctx, knowledgeID, func() error {
		data, err := h.downloadCallbackFile(cb.URL)
		if err != nil {
			return fmt.Errorf("download callback file: %w", err)
		}

		newPath, err := h.fileSvc.SaveBytes(ctx, data, knowledge.TenantID, knowledge.FileName, false)
		if err != nil {
			return fmt.Errorf("save file: %w", err)
		}

		// Re-read to get latest FilePath (concurrent safety)
		freshKnowledge, err := h.kgService.GetKnowledgeByIDOnly(ctx, knowledgeID)
		if err != nil {
			return fmt.Errorf("re-read knowledge: %w", err)
		}
		oldPath := freshKnowledge.FilePath

		freshKnowledge.FilePath = newPath
		freshKnowledge.FileSize = int64(len(data))
		if cb.Status == 2 {
			freshKnowledge.UpdatedAt = time.Now()
		}
		if err := h.kgService.UpdateKnowledge(ctx, freshKnowledge); err != nil {
			return fmt.Errorf("update knowledge: %w", err)
		}

		if oldPath != "" && oldPath != newPath {
			if delErr := h.fileSvc.DeleteFile(ctx, oldPath); delErr != nil {
				logger.Warnf(ctx, "[ONLYOFFICE] failed to delete old file %s: %v", oldPath, delErr)
			}
		}

		return nil
	})

	if saveErr != nil {
		logger.Errorf(ctx, "[ONLYOFFICE] callback save failed for %s: %v", knowledgeID, saveErr)
	}

	// Status 2 = final save (all editors closed) → trigger re-parse to update chunks & vectors
	if saveErr == nil && cb.Status == 2 {
		if _, reparseErr := h.kgService.ReparseKnowledge(ctx, knowledgeID); reparseErr != nil {
			logger.Warnf(ctx, "[ONLYOFFICE] reparse failed for %s: %v", knowledgeID, reparseErr)
		} else {
			logger.Infof(ctx, "[ONLYOFFICE] reparse queued for %s after final save", knowledgeID)
		}
	}

	c.JSON(http.StatusOK, gin.H{"error": 0})
}
