package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WeChatConfigRepository 企业微信配置仓库
type WeChatConfigRepository interface {
	// GetByCorpID 根据 CorpID 获取配置
	GetByCorpID(ctx context.Context, corpID string) (*types.WeChatConfig, error)
	// GetEnabled 获取已启用的配置
	GetEnabled(ctx context.Context) (*types.WeChatConfig, error)
	// Create 创建配置
	Create(ctx context.Context, config *types.WeChatConfig) error
	// Update 更新配置
	Update(ctx context.Context, config *types.WeChatConfig) error
}

// OAuthBindingRepository OAuth 绑定关系仓库
type OAuthBindingRepository interface {
	// CreateBinding 创建绑定关系
	CreateBinding(ctx context.Context, binding *types.OAuthBinding) error
	// GetByUserID 根据用户 ID 获取绑定
	GetByUserID(ctx context.Context, userID string) (*types.OAuthBinding, error)
	// GetByProviderUserID 根据提供者用户 ID 获取绑定
	GetByProviderUserID(ctx context.Context, provider, providerUserID, corpID string) (*types.OAuthBinding, error)
	// HasBinding 检查用户是否有绑定
	HasBinding(ctx context.Context, userID string) (bool, error)
	// DeleteByUserID 删除用户的绑定
	DeleteByUserID(ctx context.Context, userID string) error
}

// WeChatAuthService 企业微信认证服务
type WeChatAuthService interface {
	// GetConfig 获取企业微信配置
	GetConfig(ctx context.Context) (*types.WeChatConfig, error)
	// LoginByCode 通过 OAuth code 登录
	LoginByCode(ctx context.Context, code string) (*types.LoginResponse, error)
	// GenerateQRCode 生成扫码登录二维码
	GenerateQRCode(ctx context.Context) (ticket, qrcodeURL string, err error)
	// PollTicketStatus 轮询扫码状态
	PollTicketStatus(ctx context.Context, ticket string) (status string, loginResp *types.LoginResponse, err error)
	// HandleCallback OAuth 回调处理
	HandleCallback(ctx context.Context, code, state string) (*types.LoginResponse, error)
}
