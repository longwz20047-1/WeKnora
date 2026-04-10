package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/internal/wechat"
)

const (
	// qrTicketPrefix is the Redis key prefix for QR code tickets.
	qrTicketPrefix = "wechat:qr:"
	// qrTicketTTL is the expiry duration for QR code tickets in Redis.
	qrTicketTTL = 300 * time.Second
	// ticketStatusPending indicates the QR code has been generated but not yet scanned.
	ticketStatusPending = "pending"
	// ticketStatusScanned indicates the QR code has been scanned but login not yet confirmed.
	ticketStatusScanned = "scanned"
	// ticketStatusConfirmedPrefix is the prefix for a confirmed ticket value in Redis.
	ticketStatusConfirmedPrefix = "confirmed:"
)

// wechatAuthService implements interfaces.WeChatAuthService.
type wechatAuthService struct {
	userService   interfaces.UserService
	tenantService interfaces.TenantService
	userRepo      interfaces.UserRepository
	oauthRepo     interfaces.OAuthBindingRepository
	configRepo    interfaces.WeChatConfigRepository
	redisClient   *redis.Client
}

// NewWeChatAuthService creates a new WeChatAuthService instance.
func NewWeChatAuthService(
	userService interfaces.UserService,
	tenantService interfaces.TenantService,
	userRepo interfaces.UserRepository,
	oauthRepo interfaces.OAuthBindingRepository,
	configRepo interfaces.WeChatConfigRepository,
	redisClient *redis.Client,
) interfaces.WeChatAuthService {
	return &wechatAuthService{
		userService:   userService,
		tenantService: tenantService,
		userRepo:      userRepo,
		oauthRepo:     oauthRepo,
		configRepo:    configRepo,
		redisClient:   redisClient,
	}
}

// ---------------------------------------------------------------------------
// getOrCreateClient creates a wechat.Client from the enabled config.
// A new client is created per call — simple and correct when config may change.
// ---------------------------------------------------------------------------

func (s *wechatAuthService) getOrCreateClient(ctx context.Context) (*wechat.Client, *types.WeChatConfig, error) {
	config, err := s.configRepo.GetEnabled(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get wechat config: %w", err)
	}
	if config == nil {
		return nil, nil, errors.New("wechat work login is not configured")
	}

	client := wechat.NewClient(config.CorpID, config.AgentSecret, config.AgentID)
	return client, config, nil
}

// ---------------------------------------------------------------------------
// GetConfig returns the WeChat Work configuration.
// When no configuration exists it returns a disabled placeholder (no error).
// ---------------------------------------------------------------------------

func (s *wechatAuthService) GetConfig(ctx context.Context) (*types.WeChatConfig, error) {
	config, err := s.configRepo.GetEnabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("get wechat config: %w", err)
	}
	if config == nil {
		return &types.WeChatConfig{IsEnabled: false}, nil
	}
	return config, nil
}

// ---------------------------------------------------------------------------
// LoginByCode performs the core OAuth login-or-register flow.
// ---------------------------------------------------------------------------

func (s *wechatAuthService) LoginByCode(ctx context.Context, code string) (*types.LoginResponse, error) {
	// 1. Obtain wechat client and config.
	client, config, err := s.getOrCreateClient(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Exchange code for userid.
	userID, err := client.GetUserIdentityByCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("get user identity by code: %w", err)
	}
	logger.Infof(ctx, "WeChat OAuth: got userid %s", secutils.SanitizeForLog(userID))

	// 3. Get user detail from WeChat Work.
	wxUser, err := client.GetUserDetail(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get wechat user detail: %w", err)
	}

	// 4. Check existing OAuth binding.
	binding, err := s.oauthRepo.GetByProviderUserID(ctx, "wechat_work", userID, config.CorpID)
	if err != nil {
		return nil, fmt.Errorf("check oauth binding: %w", err)
	}

	if binding != nil {
		// Already bound — log the user in directly.
		return s.loginExistingBinding(ctx, binding)
	}

	// 5. Not bound yet — try email match or create new account.
	return s.loginNewBinding(ctx, config, wxUser)
}

// loginExistingBinding generates tokens for an already-bound user.
func (s *wechatAuthService) loginExistingBinding(ctx context.Context, binding *types.OAuthBinding) (*types.LoginResponse, error) {
	// Look up by the authoritative binding.UserID first, not email.
	user, err := s.userRepo.GetUserByID(ctx, binding.UserID)
	if err != nil {
		return nil, fmt.Errorf("find bound user %s: %w", binding.UserID, err)
	}

	return s.buildLoginResponse(ctx, user)
}

// loginNewBinding handles the case where no OAuth binding exists yet.
func (s *wechatAuthService) loginNewBinding(ctx context.Context, config *types.WeChatConfig, wxUser *types.WeChatUserInfo) (*types.LoginResponse, error) {
	// Try matching by email if the WeChat user has one.
	if wxUser.Email != "" {
		existingUser, err := s.userRepo.GetUserByEmail(ctx, wxUser.Email)
		if err != nil && !errors.Is(err, repository.ErrUserNotFound) {
			return nil, fmt.Errorf("lookup user by email: %w", err)
		}
		if existingUser != nil {
			// Auto bind-back: link existing account to this WeChat identity.
			if err := s.createBinding(ctx, existingUser.ID, config, wxUser, "auto_bindback"); err != nil {
				return nil, err
			}
			logger.Infof(ctx, "WeChat OAuth: auto bind-back user %s via email", secutils.SanitizeForLog(existingUser.Email))
			return s.buildLoginResponse(ctx, existingUser)
		}
	}

	// No existing user matched — create brand-new account.
	user, err := s.createNewUser(ctx, config, wxUser)
	if err != nil {
		return nil, err
	}

	if err := s.createBinding(ctx, user.ID, config, wxUser, "new_account"); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "WeChat OAuth: created new account %s", secutils.SanitizeForLog(user.Username))
	return s.buildLoginResponse(ctx, user)
}

// createNewUser provisions a new User + Tenant for a WeChat Work member.
func (s *wechatAuthService) createNewUser(ctx context.Context, config *types.WeChatConfig, wxUser *types.WeChatUserInfo) (*types.User, error) {
	// Determine username (handle conflicts).
	username := wxUser.Name
	if username == "" {
		username = wxUser.UserID
	}
	existing, _ := s.userRepo.GetUserByUsername(ctx, username)
	if existing != nil {
		// Use UserID suffix to ensure uniqueness across same-name users
		suffix := wxUser.UserID
		if len(suffix) > 6 {
			suffix = suffix[:6]
		}
		username = username + "_" + suffix
	}

	// Determine email — use real one if available, otherwise generate a placeholder.
	email := wxUser.Email
	if email == "" {
		corpPrefix := config.CorpID
		if len(corpPrefix) > 8 {
			corpPrefix = corpPrefix[:8]
		}
		email = fmt.Sprintf("%s_%s@wechat.local", wxUser.UserID, corpPrefix)
	}

	// Generate a random password (the user will never see it).
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, fmt.Errorf("generate random password: %w", err)
	}
	randomPassword := base64.StdEncoding.EncodeToString(randomBytes)
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(randomPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	// Create tenant first.
	name := wxUser.Name
	if name == "" {
		name = wxUser.UserID
	}
	tenant, err := s.tenantService.CreateTenant(ctx, &types.Tenant{
		// Sanitize to strip control characters (reuses log sanitiser — safe for display names too).
		Name:        fmt.Sprintf("%s's Workspace", secutils.SanitizeForLog(name)),
		Description: "Default workspace",
		Status:      "active",
	})
	if err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}

	user := &types.User{
		ID:           uuid.New().String(),
		Username:     username,
		Email:        email,
		PasswordHash: string(hashedPassword),
		TenantID:     tenant.ID,
		IsActive:     true,
		Avatar:       wxUser.Avatar,
	}
	if err := s.userRepo.CreateUser(ctx, user); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	return user, nil
}

// createBinding persists a new OAuthBinding record.
func (s *wechatAuthService) createBinding(ctx context.Context, localUserID string, config *types.WeChatConfig, wxUser *types.WeChatUserInfo, bindType string) error {
	extraData, err := json.Marshal(map[string]interface{}{
		"department": wxUser.Department,
		"position":   wxUser.Position,
		"mobile":     wxUser.Mobile,
	})
	if err != nil {
		return fmt.Errorf("marshal extra data: %w", err)
	}

	binding := &types.OAuthBinding{
		ID:             uuid.New().String(),
		UserID:         localUserID,
		Provider:       "wechat_work",
		ProviderUserID: wxUser.UserID,
		CorpID:         config.CorpID,
		ProviderEmail:  wxUser.Email,
		ProviderName:   wxUser.Name,
		ProviderAvatar: wxUser.Avatar,
		ExtraData:      string(extraData),
		BindType:       bindType,
	}
	if err := s.oauthRepo.CreateBinding(ctx, binding); err != nil {
		return fmt.Errorf("create oauth binding: %w", err)
	}
	return nil
}

// buildLoginResponse generates JWT tokens and assembles a LoginResponse.
func (s *wechatAuthService) buildLoginResponse(ctx context.Context, user *types.User) (*types.LoginResponse, error) {
	accessToken, refreshToken, err := s.userService.GenerateTokens(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("generate tokens: %w", err)
	}

	tenant, err := s.tenantService.GetTenantByID(ctx, user.TenantID)
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}

	return &types.LoginResponse{
		Success:      true,
		Message:      "Login successful",
		User:         user,
		Tenant:       tenant,
		Token:        accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// ---------------------------------------------------------------------------
// GenerateQRCode creates a QR-code ticket for scan-to-login.
// ---------------------------------------------------------------------------

func (s *wechatAuthService) GenerateQRCode(ctx context.Context) (ticket, qrcodeURL string, err error) {
	config, err := s.configRepo.GetEnabled(ctx)
	if err != nil {
		return "", "", fmt.Errorf("get wechat config: %w", err)
	}
	if config == nil {
		return "", "", errors.New("wechat work login is not configured")
	}

	// Generate a unique ticket.
	ticket = uuid.New().String()

	// Store ticket in Redis as pending.
	key := qrTicketPrefix + ticket
	if err := s.redisClient.Set(ctx, key, ticketStatusPending, qrTicketTTL).Err(); err != nil {
		return "", "", fmt.Errorf("store qr ticket in redis: %w", err)
	}

	// Build WeChat Work QR code scan-to-login URL.
	// Docs: https://developer.work.weixin.qq.com/document/path/91019
	redirectURI := config.QRCodeRedirectURL
	if redirectURI == "" {
		redirectURI = config.CallbackURL
	}
	qrcodeURL = fmt.Sprintf(
		"https://open.work.weixin.qq.com/wwopen/sso/qrConnect?appid=%s&agentid=%d&redirect_uri=%s&state=%s",
		url.QueryEscape(config.CorpID),
		config.AgentID,
		url.QueryEscape(redirectURI),
		url.QueryEscape(ticket),
	)

	logger.Infof(ctx, "WeChat OAuth: generated QR ticket %s", ticket)
	return ticket, qrcodeURL, nil
}

// ---------------------------------------------------------------------------
// PollTicketStatus checks the scan-to-login status by reading the Redis ticket.
// ---------------------------------------------------------------------------

func (s *wechatAuthService) PollTicketStatus(ctx context.Context, ticket string) (status string, loginResp *types.LoginResponse, err error) {
	key := qrTicketPrefix + ticket
	val, err := s.redisClient.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "expired", nil, nil
		}
		return "", nil, fmt.Errorf("get qr ticket from redis: %w", err)
	}

	switch {
	case val == ticketStatusPending:
		return "pending", nil, nil
	case val == ticketStatusScanned:
		return "scanned", nil, nil
	case strings.HasPrefix(val, ticketStatusConfirmedPrefix):
		// Consume the ticket atomically — prevent replay of JWT tokens.
		s.redisClient.Del(ctx, key)

		jsonData := val[len(ticketStatusConfirmedPrefix):]
		var resp types.LoginResponse
		if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
			return "", nil, fmt.Errorf("unmarshal login response from ticket: %w", err)
		}
		return "confirmed", &resp, nil
	default:
		// Unknown state — treat as expired.
		return "expired", nil, nil
	}
}

// ---------------------------------------------------------------------------
// HandleCallback processes the OAuth callback (WeChat redirects here after scan).
// ---------------------------------------------------------------------------

func (s *wechatAuthService) HandleCallback(ctx context.Context, code, state string) (*types.LoginResponse, error) {
	ticket := state
	key := qrTicketPrefix + ticket

	// Validate ticket exists in Redis (CSRF protection).
	val, err := s.redisClient.Get(ctx, key).Result()
	if err != nil || (val != ticketStatusPending && val != ticketStatusScanned) {
		return nil, fmt.Errorf("invalid or expired callback state")
	}

	// Mark ticket as scanned.
	if err := s.redisClient.Set(ctx, key, ticketStatusScanned, qrTicketTTL).Err(); err != nil {
		logger.Errorf(ctx, "WeChat OAuth: failed to update ticket to scanned: %v", err)
	}

	// Perform the actual login.
	loginResp, err := s.LoginByCode(ctx, code)
	if err != nil {
		return nil, err
	}

	// Store confirmed result so PollTicketStatus can return it.
	respJSON, err := json.Marshal(loginResp)
	if err != nil {
		return nil, fmt.Errorf("marshal login response: %w", err)
	}
	confirmedVal := ticketStatusConfirmedPrefix + string(respJSON)
	if err := s.redisClient.Set(ctx, key, confirmedVal, qrTicketTTL).Err(); err != nil {
		logger.Errorf(ctx, "WeChat OAuth: failed to update ticket to confirmed: %v", err)
	}

	return loginResp, nil
}
