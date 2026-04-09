package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// wechatConfigRepository implements WeChatConfigRepository interface
type wechatConfigRepository struct {
	db *gorm.DB
}

// NewWeChatConfigRepository creates a new WeChat config repository
func NewWeChatConfigRepository(db *gorm.DB) interfaces.WeChatConfigRepository {
	return &wechatConfigRepository{db: db}
}

// GetByCorpID gets a WeChat config by CorpID
func (r *wechatConfigRepository) GetByCorpID(ctx context.Context, corpID string) (*types.WeChatConfig, error) {
	var config types.WeChatConfig
	if err := r.db.WithContext(ctx).Where("corp_id = ?", corpID).First(&config).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &config, nil
}

// GetEnabled gets the first enabled WeChat config
func (r *wechatConfigRepository) GetEnabled(ctx context.Context) (*types.WeChatConfig, error) {
	var config types.WeChatConfig
	if err := r.db.WithContext(ctx).Where("is_enabled = ?", true).First(&config).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &config, nil
}

// Create creates a WeChat config
func (r *wechatConfigRepository) Create(ctx context.Context, config *types.WeChatConfig) error {
	return r.db.WithContext(ctx).Create(config).Error
}

// Update updates a WeChat config
func (r *wechatConfigRepository) Update(ctx context.Context, config *types.WeChatConfig) error {
	return r.db.WithContext(ctx).Save(config).Error
}
