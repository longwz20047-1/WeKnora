package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
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
	cfg        *config.Config
	kgService  interfaces.KnowledgeService
	tenantSvc  interfaces.TenantService
	fileSvc    interfaces.FileService
	redis      *redis.Client
}

// NewOnlyOfficeHandler always returns a valid instance (never nil).
func NewOnlyOfficeHandler(
	cfg *config.Config,
	kgService interfaces.KnowledgeService,
	tenantSvc interfaces.TenantService,
	fileSvc interfaces.FileService,
	redis *redis.Client,
) *OnlyOfficeHandler {
	return &OnlyOfficeHandler{
		cfg:        cfg,
		kgService:  kgService,
		tenantSvc:  tenantSvc,
		fileSvc:    fileSvc,
		redis:      redis,
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

// downloadCallbackFile downloads the modified file from ONLYOFFICE.
// Rewrites the download URL to use the Docker-internal ONLYOFFICE address
// since the callback URL from ONLYOFFICE uses the external address which
// is not reachable from inside the Docker network.
func (h *OnlyOfficeHandler) downloadCallbackFile(downloadURL string) ([]byte, error) {
	// Rewrite external URL to internal Docker address
	downloadURL = h.rewriteToInternalURL(downloadURL)

	client := &http.Client{
		Timeout: 2 * time.Minute,
	}

	resp, err := client.Get(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
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

// rewriteToInternalURL replaces the host:port of a download URL with the
// Docker-internal ONLYOFFICE address. The callback URL from ONLYOFFICE uses
// the external address (e.g. http://192.168.100.30:8443/...) which the app
// container cannot reach. Rewrite it to http://onlyoffice:80/...
func (h *OnlyOfficeHandler) rewriteToInternalURL(downloadURL string) string {
	dsURL := h.cfg.OnlyOffice.DocServerURL
	if dsURL == "" {
		return downloadURL
	}
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return downloadURL
	}
	internal, err := url.Parse(dsURL)
	if err != nil {
		return downloadURL
	}
	parsed.Scheme = internal.Scheme
	parsed.Host = internal.Host
	return parsed.String()
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

	// Prevent editing while document is being parsed to avoid file corruption
	if mode == "edit" && (knowledge.ParseStatus == types.ParseStatusPending || knowledge.ParseStatus == types.ParseStatusProcessing) {
		logger.Infof(ctx, "[ONLYOFFICE] downgrading edit to view: knowledge %s has parse_status=%s", knowledgeID, knowledge.ParseStatus)
		mode = "view"
	}

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
	token, err := parser.Parse(cb.Token, func(token *jwt.Token) (interface{}, error) {
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

	// When JWT_IN_BODY=true, the actual callback data is inside the JWT payload,
	// not at the top level of the JSON body. Extract fields from JWT claims.
	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		// The payload may be nested under a "payload" key or flat at root
		data := claims
		if nested, ok := claims["payload"].(map[string]interface{}); ok {
			data = jwt.MapClaims(nested)
		}
		if cb.Status == 0 {
			if s, ok := data["status"].(float64); ok {
				cb.Status = int(s)
			}
		}
		if cb.Key == "" {
			if k, ok := data["key"].(string); ok {
				cb.Key = k
			}
		}
		if cb.URL == "" {
			if u, ok := data["url"].(string); ok {
				cb.URL = u
			}
		}
	}

	// Status 4 = document closed with no changes since last save.
	// If a prior status 6 (autosave) already saved new content, we still need
	// to trigger reparse so vectors/chunks reflect the latest file.
	if cb.Status == 4 {
		logger.Infof(ctx, "[ONLYOFFICE] callback status=4 (closed, no new changes), key=%s", cb.Key)

		knowledgeID := cb.Key
		if idx := strings.LastIndex(cb.Key, "_"); idx > 0 {
			knowledgeID = cb.Key[:idx]
		}

		// Check if a prior status 6 saved new content (dirty flag set by status 6 handler)
		dirtyKey := fmt.Sprintf("onlyoffice:dirty:%s", knowledgeID)
		dirty := false
		if h.redis != nil {
			if val, err := h.redis.GetDel(ctx, dirtyKey).Result(); err == nil && val == "1" {
				dirty = true
			}
		}

		if dirty {
			knowledge, err := h.kgService.GetKnowledgeByIDOnly(ctx, knowledgeID)
			if err != nil {
				logger.Warnf(ctx, "[ONLYOFFICE] knowledge not found for status 4 reparse: id=%s err=%v", knowledgeID, err)
				c.JSON(http.StatusOK, gin.H{"error": 0})
				return
			}
			ctx = context.WithValue(ctx, types.TenantIDContextKey, knowledge.TenantID)
			tenant, err := h.tenantSvc.GetTenantByID(ctx, knowledge.TenantID)
			if err != nil {
				logger.Warnf(ctx, "[ONLYOFFICE] tenant not found for status 4: tenantID=%d err=%v", knowledge.TenantID, err)
				c.JSON(http.StatusOK, gin.H{"error": 0})
				return
			}
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
			knowledge.UpdatedAt = time.Now()
			if err := h.kgService.UpdateKnowledge(ctx, knowledge); err != nil {
				logger.Warnf(ctx, "[ONLYOFFICE] failed to update knowledge for status 4: %v", err)
			}
			// Defer reparse if document is currently being parsed
			if knowledge.ParseStatus == types.ParseStatusPending || knowledge.ParseStatus == types.ParseStatusProcessing {
				if h.redis != nil {
					editedKey := fmt.Sprintf("onlyoffice:edited-during-parse:%s", knowledgeID)
					h.redis.Set(ctx, editedKey, "1", 1*time.Hour)
				}
				logger.Infof(ctx, "[ONLYOFFICE] reparse deferred for %s on status 4 (parse_status=%s)", knowledgeID, knowledge.ParseStatus)
			} else if _, reparseErr := h.kgService.ReparseKnowledge(ctx, knowledgeID); reparseErr != nil {
				logger.Warnf(ctx, "[ONLYOFFICE] reparse failed for %s on status 4: %v", knowledgeID, reparseErr)
			} else {
				logger.Infof(ctx, "[ONLYOFFICE] reparse queued for %s after document close (status 4, dirty)", knowledgeID)
			}
		} else {
			logger.Infof(ctx, "[ONLYOFFICE] no prior autosave for %s, skipping reparse on status 4", knowledgeID)
		}

		c.JSON(http.StatusOK, gin.H{"error": 0})
		return
	}

	// Only process status 2 (save after close) and 6 (force save)
	if cb.Status != 2 && cb.Status != 6 {
		logger.Infof(ctx, "[ONLYOFFICE] callback ignored: status=%d key=%s", cb.Status, cb.Key)
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

	// ReparseKnowledge (and its downstream calls) require *types.Tenant in context.
	tenant, err := h.tenantSvc.GetTenantByID(ctx, knowledge.TenantID)
	if err != nil {
		logger.Warnf(ctx, "[ONLYOFFICE] tenant not found for callback: tenantID=%d err=%v", knowledge.TenantID, err)
		c.JSON(http.StatusOK, gin.H{"error": 0})
		return
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	saveErr := h.withSaveLock(ctx, knowledgeID, func() error {
		data, err := h.downloadCallbackFile(cb.URL)
		if err != nil {
			return fmt.Errorf("download callback file: %w", err)
		}

		// Re-read to get latest FilePath (concurrent safety)
		freshKnowledge, err := h.kgService.GetKnowledgeByIDOnly(ctx, knowledgeID)
		if err != nil {
			return fmt.Errorf("re-read knowledge: %w", err)
		}

		// Overwrite the file at the existing path to preserve all cached references.
		// Using SaveBytes would generate a new random path, breaking ONLYOFFICE's
		// document cache and any in-flight file requests.
		if err := h.fileSvc.OverwriteBytes(ctx, data, freshKnowledge.FilePath); err != nil {
			return fmt.Errorf("overwrite file: %w", err)
		}

		freshKnowledge.FileSize = int64(len(data))
		if cb.Status == 2 {
			freshKnowledge.UpdatedAt = time.Now()
		}
		if err := h.kgService.UpdateKnowledge(ctx, freshKnowledge); err != nil {
			return fmt.Errorf("update knowledge: %w", err)
		}

		return nil
	})

	if saveErr != nil {
		logger.Errorf(ctx, "[ONLYOFFICE] callback save failed for %s: %v", knowledgeID, saveErr)
	} else {
		logger.Infof(ctx, "[ONLYOFFICE] callback save succeeded for %s (status=%d)", knowledgeID, cb.Status)

		// Mark document as dirty on status 6 (autosave) so that a subsequent
		// status 4 (close without new changes) knows to trigger reparse.
		if cb.Status == 6 && h.redis != nil {
			dirtyKey := fmt.Sprintf("onlyoffice:dirty:%s", knowledgeID)
			h.redis.Set(ctx, dirtyKey, "1", 24*time.Hour)
		}
	}

	// Status 2 = final save (all editors closed) → trigger re-parse to update chunks & vectors
	if saveErr == nil && cb.Status == 2 {
		// Clear dirty flag since we're handling reparse now
		if h.redis != nil {
			h.redis.Del(ctx, fmt.Sprintf("onlyoffice:dirty:%s", knowledgeID))
		}

		// Re-read knowledge to get current parse_status
		freshKnowledge, err := h.kgService.GetKnowledgeByIDOnly(ctx, knowledgeID)
		if err != nil {
			logger.Warnf(ctx, "[ONLYOFFICE] failed to re-read knowledge for reparse check: %v", err)
		} else if freshKnowledge.ParseStatus == types.ParseStatusPending || freshKnowledge.ParseStatus == types.ParseStatusProcessing {
			// Document is currently being parsed — defer reparse until processing completes
			if h.redis != nil {
				editedKey := fmt.Sprintf("onlyoffice:edited-during-parse:%s", knowledgeID)
				h.redis.Set(ctx, editedKey, "1", 1*time.Hour)
			}
			logger.Infof(ctx, "[ONLYOFFICE] file saved for %s but reparse deferred (parse_status=%s)", knowledgeID, freshKnowledge.ParseStatus)
		} else {
			if _, reparseErr := h.kgService.ReparseKnowledge(ctx, knowledgeID); reparseErr != nil {
				logger.Warnf(ctx, "[ONLYOFFICE] reparse failed for %s: %v", knowledgeID, reparseErr)
			} else {
				logger.Infof(ctx, "[ONLYOFFICE] reparse queued for %s after final save", knowledgeID)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"error": 0})
}
