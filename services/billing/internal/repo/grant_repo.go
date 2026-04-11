package repo

import (
	"context"
	"time"

	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GrantRepo struct {
	db *gorm.DB
}

func NewGrantRepo(db *gorm.DB) *GrantRepo {
	return &GrantRepo{db: db}
}

func (r *GrantRepo) Create(ctx context.Context, tx *gorm.DB, g *domain.CreditGrant) error {
	return tx.WithContext(ctx).Create(g).Error
}

// GetActiveByUserIDForUpdate returns unexpired grants with remaining > 0,
// ordered by expires_at ASC (FIFO expiry), locked for update.
func (r *GrantRepo) GetActiveByUserIDForUpdate(ctx context.Context, tx *gorm.DB, userID string) ([]domain.CreditGrant, error) {
	var grants []domain.CreditGrant
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND remaining > 0 AND expires_at > ? AND expired_at IS NULL", userID, time.Now()).
		Order("expires_at ASC").Find(&grants).Error
	return grants, err
}

func (r *GrantRepo) UpdateRemaining(ctx context.Context, tx *gorm.DB, g *domain.CreditGrant) error {
	return tx.WithContext(ctx).Model(g).Update("remaining", g.Remaining).Error
}

// FindExpired returns expired grants with remaining > 0 (for cleanup job).
func (r *GrantRepo) FindExpired(ctx context.Context, limit int) ([]domain.CreditGrant, error) {
	var items []domain.CreditGrant
	err := r.db.WithContext(ctx).
		Where("expires_at < ? AND remaining > 0 AND expired_at IS NULL", time.Now()).
		Limit(limit).Find(&items).Error
	return items, err
}

// MarkExpired atomically marks one grant as expired (CAS pattern).
func (r *GrantRepo) MarkExpired(ctx context.Context, tx *gorm.DB, id string) (int64, error) {
	now := time.Now()
	result := tx.WithContext(ctx).Model(&domain.CreditGrant{}).
		Where("id = ? AND expired_at IS NULL AND remaining > 0", id).
		Updates(map[string]interface{}{
			"expired_at": now,
			"remaining":  0,
		})
	return result.RowsAffected, result.Error
}

// GetActiveByUserID returns active grants for display (no lock).
func (r *GrantRepo) GetActiveByUserID(ctx context.Context, userID string) ([]domain.CreditGrant, error) {
	var grants []domain.CreditGrant
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND remaining > 0 AND expires_at > ? AND expired_at IS NULL", userID, time.Now()).
		Order("expires_at ASC").Find(&grants).Error
	return grants, err
}
