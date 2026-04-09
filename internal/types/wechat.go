package types

import (
	"time"

	"gorm.io/gorm"
)

// WeChatConfig 企业微信应用配置
type WeChatConfig struct {
	// 唯一标识
	ID uint64 `json:"id" gorm:"primaryKey"`
	// 企业 CorpID
	CorpID string `json:"corp_id" gorm:"type:varchar(64);uniqueIndex;not null"`
	// 企业名称
	CorpName string `json:"corp_name" gorm:"type:varchar(255);not null"`
	// 应用 AgentID
	AgentID int64 `json:"agent_id" gorm:"not null"`
	// 应用 Secret（不序列化到 JSON）
	AgentSecret string `json:"-" gorm:"type:varchar(255);not null"`
	// 关联租户 ID（可空）
	TenantID *uint64 `json:"tenant_id" gorm:"index"`
	// OAuth 回调 URL
	CallbackURL string `json:"callback_url" gorm:"type:varchar(512)"`
	// 扫码登录重定向 URL
	QRCodeRedirectURL string `json:"qrcode_redirect_url" gorm:"type:varchar(512)"`
	// 是否启用
	IsEnabled bool `json:"is_enabled" gorm:"default:true"`
	// 创建时间
	CreatedAt time.Time `json:"created_at"`
	// 更新时间
	UpdatedAt time.Time `json:"updated_at"`
	// 软删除时间
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// OAuthBinding OAuth 绑定关系
type OAuthBinding struct {
	// 唯一标识（UUID）
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// 关联的用户 ID
	UserID string `json:"user_id" gorm:"type:varchar(36);not null;index"`
	// OAuth 提供者（如 wechat_work）
	Provider string `json:"provider" gorm:"type:varchar(50);not null"`
	// 提供者侧的用户 ID
	ProviderUserID string `json:"provider_user_id" gorm:"type:varchar(255);not null"`
	// 企业 CorpID
	CorpID string `json:"corp_id" gorm:"type:varchar(64);not null;index"`
	// 提供者侧的邮箱
	ProviderEmail string `json:"provider_email" gorm:"type:varchar(255)"`
	// 提供者侧的用户名
	ProviderName string `json:"provider_name" gorm:"type:varchar(255)"`
	// 提供者侧的头像 URL
	ProviderAvatar string `json:"provider_avatar" gorm:"type:varchar(512)"`
	// 扩展数据（JSONB）
	ExtraData string `json:"extra_data" gorm:"type:jsonb"`
	// 绑定类型（auto_bindback, new_account, manual_bindback）
	BindType string `json:"bind_type" gorm:"type:varchar(50);not null"`
	// 创建时间
	CreatedAt time.Time `json:"created_at"`
	// 更新时间
	UpdatedAt time.Time `json:"updated_at"`
	// 软删除时间
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// WeChatUserInfo 企业微信用户信息（从 SDK 映射）
type WeChatUserInfo struct {
	UserID     string `json:"userid"`
	Name       string `json:"name"`
	Mobile     string `json:"mobile"`
	Email      string `json:"email"`
	Avatar     string `json:"avatar"`
	Department []int  `json:"department"`
	Position   string `json:"position"`
	Gender     string `json:"gender"`
	Status     int    `json:"status"`
	Enable     int    `json:"enable"`
}
