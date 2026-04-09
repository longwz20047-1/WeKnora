package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// oauthBindingRepository implements OAuthBindingRepository interface
type oauthBindingRepository struct {
	db *gorm.DB
}

// NewOAuthBindingRepository creates a new OAuth binding repository
func NewOAuthBindingRepository(db *gorm.DB) interfaces.OAuthBindingRepository {
	return &oauthBindingRepository{db: db}
}

// CreateBinding creates an OAuth binding
func (r *oauthBindingRepository) CreateBinding(ctx context.Context, binding *types.OAuthBinding) error {
	return r.db.WithContext(ctx).Create(binding).Error
}

// GetByUserID gets an OAuth binding by user ID
func (r *oauthBindingRepository) GetByUserID(ctx context.Context, userID string) (*types.OAuthBinding, error) {
	var binding types.OAuthBinding
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &binding, nil
}

// GetByProviderUserID gets an OAuth binding by provider user ID
func (r *oauthBindingRepository) GetByProviderUserID(ctx context.Context, provider, providerUserID, corpID string) (*types.OAuthBinding, error) {
	var binding types.OAuthBinding
	if err := r.db.WithContext(ctx).
		Where("provider = ? AND provider_user_id = ? AND corp_id = ?", provider, providerUserID, corpID).
		First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &binding, nil
}

// HasBinding checks if a user has any OAuth binding
func (r *oauthBindingRepository) HasBinding(ctx context.Context, userID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.OAuthBinding{}).Where("user_id = ?", userID).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// DeleteByUserID deletes OAuth bindings by user ID
func (r *oauthBindingRepository) DeleteByUserID(ctx context.Context, userID string) error {
	result := r.db.WithContext(ctx).Where("user_id = ?", userID).Delete(&types.OAuthBinding{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("no binding found")
	}
	return nil
}
