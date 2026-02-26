package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// ---------------------------------------------------------------------------
// Mocks — embed interface to satisfy all methods, override only what we need
// ---------------------------------------------------------------------------

type mockKnowledgeService struct {
	interfaces.KnowledgeService // embed for unused methods
	knowledge                   *types.Knowledge
	err                         error
	fileData                    []byte
	fileName                    string
	fileErr                     error
	updateErr                   error
	updated                     *types.Knowledge
}

func (m *mockKnowledgeService) GetKnowledgeByIDOnly(_ context.Context, _ string) (*types.Knowledge, error) {
	return m.knowledge, m.err
}

func (m *mockKnowledgeService) GetKnowledgeFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	if m.fileErr != nil {
		return nil, "", m.fileErr
	}
	return io.NopCloser(bytes.NewReader(m.fileData)), m.fileName, nil
}

func (m *mockKnowledgeService) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	m.updated = k
	return m.updateErr
}

type mockFileService struct {
	interfaces.FileService // embed for unused methods
	savePath               string
	saveErr                error
	deleteErr              error
	deleted                string
	overwriteErr           error
	overwritten            string
}

func (m *mockFileService) SaveBytes(_ context.Context, _ []byte, _ uint64, _ string, _ bool) (string, error) {
	return m.savePath, m.saveErr
}

func (m *mockFileService) OverwriteBytes(_ context.Context, _ []byte, path string) error {
	m.overwritten = path
	return m.overwriteErr
}

func (m *mockFileService) DeleteFile(_ context.Context, path string) error {
	m.deleted = path
	return m.deleteErr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func testConfig(jwtSecret, hmacSecret string) *config.Config {
	return &config.Config{
		OnlyOffice: &config.OnlyOfficeConfig{
			JWTSecret:   jwtSecret,
			HMACSecret:  hmacSecret,
			InternalURL: "http://onlyoffice:80",
			ExternalURL: "http://localhost:8080",
		},
	}
}

func testKnowledge() *types.Knowledge {
	return &types.Knowledge{
		ID:        "kid-123",
		TenantID:  1,
		FileName:  "test.docx",
		FilePath:  "/old/path/test.docx",
		FileSize:  1024,
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func newGinContext(method, path string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	return c, w
}

func parseJSON(w *httptest.ResponseRecorder) map[string]interface{} {
	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	return result
}

// ---------------------------------------------------------------------------
// Tests: Enabled
// ---------------------------------------------------------------------------

func TestEnabled_WithConfig(t *testing.T) {
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), nil, nil, nil, nil)
	if !h.Enabled() {
		t.Error("expected Enabled()=true with valid config")
	}
}

func TestEnabled_NilConfig(t *testing.T) {
	h := NewOnlyOfficeHandler(&config.Config{}, nil, nil, nil, nil)
	if h.Enabled() {
		t.Error("expected Enabled()=false with nil OnlyOffice config")
	}
}

func TestEnabled_EmptyJWTSecret(t *testing.T) {
	h := NewOnlyOfficeHandler(testConfig("", "hmac"), nil, nil, nil)
	if h.Enabled() {
		t.Error("expected Enabled()=false with empty JWTSecret")
	}
}

// ---------------------------------------------------------------------------
// Tests: generateDocKey
// ---------------------------------------------------------------------------

func TestGenerateDocKey_Deterministic(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k1 := generateDocKey("kid-1", ts)
	k2 := generateDocKey("kid-1", ts)
	if k1 != k2 {
		t.Errorf("expected deterministic key, got %q vs %q", k1, k2)
	}
	if !strings.HasPrefix(k1, "kid-1_") {
		t.Errorf("key should start with knowledgeID_, got %q", k1)
	}
	parts := strings.SplitN(k1, "_", 2)
	if len(parts) != 2 || len(parts[1]) != 8 {
		t.Errorf("expected 8 char hash suffix, got %q", k1)
	}
}

func TestGenerateDocKey_DifferentTimestamps(t *testing.T) {
	k1 := generateDocKey("kid-1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	k2 := generateDocKey("kid-1", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if k1 == k2 {
		t.Error("different timestamps should produce different keys")
	}
}

// ---------------------------------------------------------------------------
// Tests: GetEditorConfig
// ---------------------------------------------------------------------------

func TestGetEditorConfig_Disabled(t *testing.T) {
	h := NewOnlyOfficeHandler(&config.Config{}, nil, nil, nil, nil)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/config/kid-123?mode=view", nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-123"}}

	h.GetEditorConfig(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetEditorConfig_UnsupportedType(t *testing.T) {
	kgSvc := &mockKnowledgeService{
		knowledge: &types.Knowledge{
			ID: "kid-1", TenantID: 1, FileName: "data.xyz",
			UpdatedAt: time.Now(),
		},
	}
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), kgSvc, nil, nil, nil)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/config/kid-1?mode=view", nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-1"}}
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	c.Set(types.UserIDContextKey.String(), "user-1")

	h.GetEditorConfig(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unsupported type, got %d", w.Code)
	}
}

func TestGetEditorConfig_ViewMode_EditableType(t *testing.T) {
	kgSvc := &mockKnowledgeService{knowledge: testKnowledge()}
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), kgSvc, nil, nil, nil)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/config/kid-123?mode=view", nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-123"}}
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	c.Set(types.UserIDContextKey.String(), "user-1")

	h.GetEditorConfig(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	result := parseJSON(w)
	cfg, ok := result["config"].(map[string]interface{})
	if !ok {
		t.Fatal("expected config in response")
	}
	edCfg := cfg["editorConfig"].(map[string]interface{})
	if edCfg["mode"] != "view" {
		t.Errorf("expected mode=view, got %v", edCfg["mode"])
	}
	doc := cfg["document"].(map[string]interface{})
	perms := doc["permissions"].(map[string]interface{})
	if perms["edit"] != false {
		t.Error("view mode should have edit=false")
	}
}

func TestGetEditorConfig_EditMode_EditableType(t *testing.T) {
	kgSvc := &mockKnowledgeService{knowledge: testKnowledge()}
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), kgSvc, nil, nil, nil)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/config/kid-123?mode=edit", nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-123"}}
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	c.Set(types.UserIDContextKey.String(), "user-1")

	h.GetEditorConfig(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	cfg := parseJSON(w)["config"].(map[string]interface{})
	edCfg := cfg["editorConfig"].(map[string]interface{})
	if edCfg["mode"] != "edit" {
		t.Errorf("expected mode=edit for docx, got %v", edCfg["mode"])
	}
	doc := cfg["document"].(map[string]interface{})
	perms := doc["permissions"].(map[string]interface{})
	if perms["edit"] != true {
		t.Error("edit mode for docx should have edit=true")
	}
}

func TestGetEditorConfig_EditMode_NonEditableType(t *testing.T) {
	kg := testKnowledge()
	kg.FileName = "legacy.doc" // .doc is NOT in editableTypes
	kgSvc := &mockKnowledgeService{knowledge: kg}
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), kgSvc, nil, nil, nil)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/config/kid-123?mode=edit", nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-123"}}
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	c.Set(types.UserIDContextKey.String(), "user-1")

	h.GetEditorConfig(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	cfg := parseJSON(w)["config"].(map[string]interface{})
	edCfg := cfg["editorConfig"].(map[string]interface{})
	// I5 fix: mode should be "view" for non-editable types even when edit requested
	if edCfg["mode"] != "view" {
		t.Errorf("expected mode=view for non-editable .doc, got %v", edCfg["mode"])
	}
	doc := cfg["document"].(map[string]interface{})
	perms := doc["permissions"].(map[string]interface{})
	if perms["edit"] != false {
		t.Error(".doc should have edit=false")
	}
}

func TestGetEditorConfig_HasJWTToken(t *testing.T) {
	kgSvc := &mockKnowledgeService{knowledge: testKnowledge()}
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), kgSvc, nil, nil, nil)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/config/kid-123?mode=view", nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-123"}}
	c.Set(types.TenantIDContextKey.String(), uint64(1))
	c.Set(types.UserIDContextKey.String(), "user-1")

	h.GetEditorConfig(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	cfg := parseJSON(w)["config"].(map[string]interface{})
	tok, ok := cfg["token"].(string)
	if !ok || tok == "" {
		t.Error("response config should contain a non-empty JWT token")
	}
	parser := jwt.NewParser()
	_, err := parser.Parse(tok, func(token *jwt.Token) (interface{}, error) {
		return []byte("secret"), nil
	})
	if err != nil {
		t.Errorf("config JWT token should be valid: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: ServeFile
// ---------------------------------------------------------------------------

func TestServeFile_Disabled(t *testing.T) {
	h := NewOnlyOfficeHandler(&config.Config{}, nil, nil, nil, nil)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/file/kid-123?token=x", nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-123"}}

	h.ServeFile(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestServeFile_InvalidToken(t *testing.T) {
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), nil, nil, nil, nil)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/file/kid-123?token=invalid", nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-123"}}

	h.ServeFile(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestServeFile_TokenIDMismatch(t *testing.T) {
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), nil, nil, nil, nil)
	token := secutils.GenerateHMACToken("hmac", "other-id", 1, 5*time.Minute)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/file/kid-123?token="+token, nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-123"}}

	h.ServeFile(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
	result := parseJSON(w)
	if result["error"] != "token does not match requested document" {
		t.Errorf("unexpected error: %v", result["error"])
	}
}

func TestServeFile_ValidToken_StreamsFile(t *testing.T) {
	fileContent := []byte("hello world document content")
	kgSvc := &mockKnowledgeService{
		fileData: fileContent,
		fileName: "test.docx",
	}
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), kgSvc, nil, nil, nil)
	token := secutils.GenerateHMACToken("hmac", "kid-123", 1, 5*time.Minute)
	c, w := newGinContext("GET", "/api/v1/onlyoffice/file/kid-123?token="+token, nil)
	c.Params = gin.Params{{Key: "id", Value: "kid-123"}}

	h.ServeFile(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), fileContent) {
		t.Errorf("expected file content in body, got %q", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Tests: HandleCallback — JWT verification (C1 fix)
// ---------------------------------------------------------------------------

func TestHandleCallback_Disabled(t *testing.T) {
	h := NewOnlyOfficeHandler(&config.Config{}, nil, nil, nil, nil)
	c, w := newGinContext("POST", "/api/v1/onlyoffice/callback", strings.NewReader(`{}`))

	h.HandleCallback(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleCallback_MissingToken_Rejected(t *testing.T) {
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), nil, nil, nil, nil)
	body := `{"key":"kid-123_abc12345","status":2,"url":"http://example.com/file"}`
	c, w := newGinContext("POST", "/api/v1/onlyoffice/callback", strings.NewReader(body))

	h.HandleCallback(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (ONLYOFFICE protocol), got %d", w.Code)
	}
}

func TestHandleCallback_EmptyToken_Rejected(t *testing.T) {
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), nil, nil, nil, nil)
	body := `{"key":"kid-123_abc12345","status":2,"url":"http://example.com/file","token":""}`
	c, w := newGinContext("POST", "/api/v1/onlyoffice/callback", strings.NewReader(body))

	h.HandleCallback(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleCallback_InvalidJWT_Rejected(t *testing.T) {
	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), nil, nil, nil, nil)
	body := `{"key":"kid-123_abc12345","status":2,"url":"http://example.com/file","token":"invalid.jwt.token"}`
	c, w := newGinContext("POST", "/api/v1/onlyoffice/callback", strings.NewReader(body))

	h.HandleCallback(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleCallback_WrongSecret_Rejected(t *testing.T) {
	wrongToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"key":    "kid-123_abc12345",
		"status": 2,
	})
	signed, _ := wrongToken.SignedString([]byte("wrong-secret"))

	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), nil, nil, nil, nil)
	body := `{"key":"kid-123_abc12345","status":2,"url":"http://example.com/file","token":"` + signed + `"}`
	c, w := newGinContext("POST", "/api/v1/onlyoffice/callback", strings.NewReader(body))

	h.HandleCallback(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleCallback_Status4_NoOp(t *testing.T) {
	validToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"key":    "kid-123_abc12345",
		"status": 4,
	})
	signed, _ := validToken.SignedString([]byte("secret"))

	h := NewOnlyOfficeHandler(testConfig("secret", "hmac"), nil, nil, nil, nil)
	body := `{"key":"kid-123_abc12345","status":4,"url":"","token":"` + signed + `"}`
	c, w := newGinContext("POST", "/api/v1/onlyoffice/callback", strings.NewReader(body))

	h.HandleCallback(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	result := parseJSON(w)
	if result["error"] != float64(0) {
		t.Errorf("expected error=0, got %v", result["error"])
	}
}

// ---------------------------------------------------------------------------
// Tests: Document key extraction
// ---------------------------------------------------------------------------

func TestDocKeyExtraction(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{"standard UUID key", "550e8400-e29b-41d4-a716-446655440000_abcd1234", "550e8400-e29b-41d4-a716-446655440000"},
		{"simple key", "kid-123_abc12345", "kid-123"},
		{"no underscore", "kid123", "kid123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			knowledgeID := tt.key
			if idx := strings.LastIndex(tt.key, "_"); idx > 0 {
				knowledgeID = tt.key[:idx]
			}
			if knowledgeID != tt.expected {
				t.Errorf("extracted %q, want %q", knowledgeID, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: docTypeMap and editableTypes consistency
// ---------------------------------------------------------------------------

func TestDocTypeMap_Coverage(t *testing.T) {
	for ext := range editableTypes {
		if _, ok := docTypeMap[ext]; !ok {
			t.Errorf("editableType %q not in docTypeMap", ext)
		}
	}
}

func TestDocTypeMap_AllCategories(t *testing.T) {
	categories := map[string]bool{}
	for _, cat := range docTypeMap {
		categories[cat] = true
	}
	expected := []string{"word", "cell", "slide", "pdf"}
	for _, cat := range expected {
		if !categories[cat] {
			t.Errorf("expected category %q in docTypeMap", cat)
		}
	}
}
