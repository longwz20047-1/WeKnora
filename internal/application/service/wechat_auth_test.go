package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---------------------------------------------------------------------------
// Mock: UserService
// ---------------------------------------------------------------------------

type mockUserServiceForWechat struct {
	generateTokensFn func(ctx context.Context, user *types.User) (string, string, error)
}

func (m *mockUserServiceForWechat) Register(ctx context.Context, req *types.RegisterRequest) (*types.User, error) {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) Login(ctx context.Context, req *types.LoginRequest) (*types.LoginResponse, error) {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) UpdateUser(ctx context.Context, user *types.User) error {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) DeleteUser(ctx context.Context, id string) error {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) ChangePassword(ctx context.Context, userID string, oldPassword, newPassword string) error {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) ValidatePassword(ctx context.Context, userID string, password string) error {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) GenerateTokens(ctx context.Context, user *types.User) (string, string, error) {
	if m.generateTokensFn != nil {
		return m.generateTokensFn(ctx, user)
	}
	return "access-token", "refresh-token", nil
}
func (m *mockUserServiceForWechat) ValidateToken(ctx context.Context, token string) (*types.User, error) {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) RefreshToken(ctx context.Context, refreshToken string) (string, string, error) {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) RevokeToken(ctx context.Context, token string) error {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) GetCurrentUser(ctx context.Context) (*types.User, error) {
	panic("not implemented")
}
func (m *mockUserServiceForWechat) SearchUsers(ctx context.Context, query string, limit int) ([]*types.User, error) {
	panic("not implemented")
}

// ---------------------------------------------------------------------------
// Mock: TenantService
// ---------------------------------------------------------------------------

type mockTenantServiceForWechat struct {
	createTenantFn  func(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error)
	getTenantByIDFn func(ctx context.Context, id uint64) (*types.Tenant, error)
}

func (m *mockTenantServiceForWechat) CreateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error) {
	if m.createTenantFn != nil {
		return m.createTenantFn(ctx, tenant)
	}
	tenant.ID = 1
	return tenant, nil
}
func (m *mockTenantServiceForWechat) GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error) {
	if m.getTenantByIDFn != nil {
		return m.getTenantByIDFn(ctx, id)
	}
	return &types.Tenant{ID: id, Name: "Test Tenant"}, nil
}
func (m *mockTenantServiceForWechat) ListTenants(ctx context.Context) ([]*types.Tenant, error) {
	panic("not implemented")
}
func (m *mockTenantServiceForWechat) UpdateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error) {
	panic("not implemented")
}
func (m *mockTenantServiceForWechat) DeleteTenant(ctx context.Context, id uint64) error {
	panic("not implemented")
}
func (m *mockTenantServiceForWechat) UpdateAPIKey(ctx context.Context, id uint64) (string, error) {
	panic("not implemented")
}
func (m *mockTenantServiceForWechat) ExtractTenantIDFromAPIKey(apiKey string) (uint64, error) {
	panic("not implemented")
}
func (m *mockTenantServiceForWechat) ListAllTenants(ctx context.Context) ([]*types.Tenant, error) {
	panic("not implemented")
}
func (m *mockTenantServiceForWechat) SearchTenants(ctx context.Context, keyword string, tenantID uint64, page, pageSize int) ([]*types.Tenant, int64, error) {
	panic("not implemented")
}
func (m *mockTenantServiceForWechat) GetTenantByIDForUser(ctx context.Context, tenantID uint64, userID string) (*types.Tenant, error) {
	panic("not implemented")
}

// ---------------------------------------------------------------------------
// Mock: UserRepository
// ---------------------------------------------------------------------------

type mockUserRepoForWechat struct {
	getUserByEmailFn    func(ctx context.Context, email string) (*types.User, error)
	getUserByUsernameFn func(ctx context.Context, username string) (*types.User, error)
	createUserFn        func(ctx context.Context, user *types.User) error
}

func (m *mockUserRepoForWechat) CreateUser(ctx context.Context, user *types.User) error {
	if m.createUserFn != nil {
		return m.createUserFn(ctx, user)
	}
	return nil
}
func (m *mockUserRepoForWechat) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	panic("not implemented")
}
func (m *mockUserRepoForWechat) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	if m.getUserByEmailFn != nil {
		return m.getUserByEmailFn(ctx, email)
	}
	return nil, errors.New("user not found")
}
func (m *mockUserRepoForWechat) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	if m.getUserByUsernameFn != nil {
		return m.getUserByUsernameFn(ctx, username)
	}
	return nil, errors.New("user not found")
}
func (m *mockUserRepoForWechat) UpdateUser(ctx context.Context, user *types.User) error {
	panic("not implemented")
}
func (m *mockUserRepoForWechat) DeleteUser(ctx context.Context, id string) error {
	panic("not implemented")
}
func (m *mockUserRepoForWechat) ListUsers(ctx context.Context, offset, limit int) ([]*types.User, error) {
	panic("not implemented")
}
func (m *mockUserRepoForWechat) SearchUsers(ctx context.Context, query string, limit int) ([]*types.User, error) {
	panic("not implemented")
}

// ---------------------------------------------------------------------------
// Mock: OAuthBindingRepository
// ---------------------------------------------------------------------------

type mockOAuthBindingRepoForWechat struct {
	createBindingFn      func(ctx context.Context, binding *types.OAuthBinding) error
	getByProviderUserIDFn func(ctx context.Context, provider, providerUserID, corpID string) (*types.OAuthBinding, error)
}

func (m *mockOAuthBindingRepoForWechat) CreateBinding(ctx context.Context, binding *types.OAuthBinding) error {
	if m.createBindingFn != nil {
		return m.createBindingFn(ctx, binding)
	}
	return nil
}
func (m *mockOAuthBindingRepoForWechat) GetByUserID(ctx context.Context, userID string) (*types.OAuthBinding, error) {
	panic("not implemented")
}
func (m *mockOAuthBindingRepoForWechat) GetByProviderUserID(ctx context.Context, provider, providerUserID, corpID string) (*types.OAuthBinding, error) {
	if m.getByProviderUserIDFn != nil {
		return m.getByProviderUserIDFn(ctx, provider, providerUserID, corpID)
	}
	return nil, nil
}
func (m *mockOAuthBindingRepoForWechat) HasBinding(ctx context.Context, userID string) (bool, error) {
	panic("not implemented")
}
func (m *mockOAuthBindingRepoForWechat) DeleteByUserID(ctx context.Context, userID string) error {
	panic("not implemented")
}

// ---------------------------------------------------------------------------
// Mock: WeChatConfigRepository
// ---------------------------------------------------------------------------

type mockWeChatConfigRepoForWechat struct {
	getEnabledFn func(ctx context.Context) (*types.WeChatConfig, error)
}

func (m *mockWeChatConfigRepoForWechat) GetByCorpID(ctx context.Context, corpID string) (*types.WeChatConfig, error) {
	panic("not implemented")
}
func (m *mockWeChatConfigRepoForWechat) GetEnabled(ctx context.Context) (*types.WeChatConfig, error) {
	if m.getEnabledFn != nil {
		return m.getEnabledFn(ctx)
	}
	return nil, nil
}
func (m *mockWeChatConfigRepoForWechat) Create(ctx context.Context, config *types.WeChatConfig) error {
	panic("not implemented")
}
func (m *mockWeChatConfigRepoForWechat) Update(ctx context.Context, config *types.WeChatConfig) error {
	panic("not implemented")
}

// ---------------------------------------------------------------------------
// Compile-time interface compliance checks
// ---------------------------------------------------------------------------

var _ interfaces.UserService = (*mockUserServiceForWechat)(nil)
var _ interfaces.TenantService = (*mockTenantServiceForWechat)(nil)
var _ interfaces.UserRepository = (*mockUserRepoForWechat)(nil)
var _ interfaces.OAuthBindingRepository = (*mockOAuthBindingRepoForWechat)(nil)
var _ interfaces.WeChatConfigRepository = (*mockWeChatConfigRepoForWechat)(nil)

// ---------------------------------------------------------------------------
// Helper: create service with default mocks
// ---------------------------------------------------------------------------

func newTestWeChatAuthService(
	configRepo *mockWeChatConfigRepoForWechat,
) interfaces.WeChatAuthService {
	if configRepo == nil {
		configRepo = &mockWeChatConfigRepoForWechat{}
	}
	return NewWeChatAuthService(
		&mockUserServiceForWechat{},
		&mockTenantServiceForWechat{},
		&mockUserRepoForWechat{},
		&mockOAuthBindingRepoForWechat{},
		configRepo,
		nil, // redisClient — not needed for non-QR tests
	)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestGetConfig_NoConfig(t *testing.T) {
	// When GetEnabled returns nil (no config row), GetConfig should return
	// a disabled placeholder instead of an error.
	configRepo := &mockWeChatConfigRepoForWechat{
		getEnabledFn: func(ctx context.Context) (*types.WeChatConfig, error) {
			return nil, nil
		},
	}

	svc := newTestWeChatAuthService(configRepo)
	cfg, err := svc.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.IsEnabled {
		t.Error("expected IsEnabled to be false when no config exists")
	}
}

func TestGetConfig_Enabled(t *testing.T) {
	expected := &types.WeChatConfig{
		ID:          1,
		CorpID:      "corp123",
		CorpName:    "Test Corp",
		AgentID:     1000001,
		AgentSecret: "secret",
		CallbackURL: "https://example.com/callback",
		IsEnabled:   true,
	}

	configRepo := &mockWeChatConfigRepoForWechat{
		getEnabledFn: func(ctx context.Context) (*types.WeChatConfig, error) {
			return expected, nil
		},
	}

	svc := newTestWeChatAuthService(configRepo)
	cfg, err := svc.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.CorpID != "corp123" {
		t.Errorf("expected CorpID %q, got %q", "corp123", cfg.CorpID)
	}
	if cfg.CorpName != "Test Corp" {
		t.Errorf("expected CorpName %q, got %q", "Test Corp", cfg.CorpName)
	}
	if cfg.AgentID != 1000001 {
		t.Errorf("expected AgentID %d, got %d", 1000001, cfg.AgentID)
	}
	if !cfg.IsEnabled {
		t.Error("expected IsEnabled to be true")
	}
}

func TestGetConfig_RepoError(t *testing.T) {
	configRepo := &mockWeChatConfigRepoForWechat{
		getEnabledFn: func(ctx context.Context) (*types.WeChatConfig, error) {
			return nil, errors.New("database connection failed")
		},
	}

	svc := newTestWeChatAuthService(configRepo)
	_, err := svc.GetConfig(context.Background())
	if err == nil {
		t.Fatal("expected error when repository fails")
	}
}

func TestNewWeChatAuthService(t *testing.T) {
	svc := NewWeChatAuthService(
		&mockUserServiceForWechat{},
		&mockTenantServiceForWechat{},
		&mockUserRepoForWechat{},
		&mockOAuthBindingRepoForWechat{},
		&mockWeChatConfigRepoForWechat{},
		nil,
	)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestWeChatAuthService_ImplementsInterface(t *testing.T) {
	// Compile-time check that wechatAuthService satisfies WeChatAuthService.
	var _ interfaces.WeChatAuthService = (*wechatAuthService)(nil)

	// Also verify the constructor returns the interface type.
	svc := NewWeChatAuthService(
		&mockUserServiceForWechat{},
		&mockTenantServiceForWechat{},
		&mockUserRepoForWechat{},
		&mockOAuthBindingRepoForWechat{},
		&mockWeChatConfigRepoForWechat{},
		nil,
	)

	// Ensure the concrete type is *wechatAuthService.
	if _, ok := svc.(*wechatAuthService); !ok {
		t.Errorf("expected *wechatAuthService, got %T", svc)
	}
}
