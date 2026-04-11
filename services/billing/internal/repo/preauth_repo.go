package repo

import (
	"context"
	"time"

	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PreAuthRepo struct {
	db *gorm.DB
}

func NewPreAuthRepo(db *gorm.DB) *PreAuthRepo {
	return &PreAuthRepo{db: db}
}

func (r *PreAuthRepo) Create(ctx context.Context, tx *gorm.DB, pa *domain.PreAuth) error {
	return tx.WithContext(ctx).Create(pa).Error
}

func (r *PreAuthRepo) GetByIDForUpdate(ctx context.Context, tx *gorm.DB, id string) (*domain.PreAuth, error) {
	var pa domain.PreAuth
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND status = ?", id, domain.PreAuthStatusPending).First(&pa).Error
	if err != nil {
		return nil, err
	}
	return &pa, nil
}

func (r *PreAuthRepo) GetByRequestID(ctx context.Context, requestID string) (*domain.PreAuth, error) {
	var pa domain.PreAuth
	err := r.db.WithContext(ctx).Where("request_id = ?", requestID).First(&pa).Error
	if err != nil {
		return nil, err
	}
	return &pa, nil
}

func (r *PreAuthRepo) UpdateStatus(ctx context.Context, tx *gorm.DB, pa *domain.PreAuth) error {
	return tx.WithContext(ctx).Model(pa).Updates(map[string]interface{}{
		"status":         pa.Status,
		"settled_at":     pa.SettledAt,
		"transaction_id": pa.TransactionID,
	}).Error
}

// CancelExpired atomically cancels one expired pre_auth (CAS pattern).
func (r *PreAuthRepo) CancelExpired(ctx context.Context, tx *gorm.DB, id string) (int64, error) {
	result := tx.WithContext(ctx).Model(&domain.PreAuth{}).
		Where("id = ? AND status = ?", id, domain.PreAuthStatusPending).
		Update("status", domain.PreAuthStatusCancelled)
	return result.RowsAffected, result.Error
}

// FindExpired returns expired pending pre_auths (for cleanup job).
func (r *PreAuthRepo) FindExpired(ctx context.Context, limit int) ([]domain.PreAuth, error) {
	var items []domain.PreAuth
	err := r.db.WithContext(ctx).
		Where("status = ? AND expires_at < ?", domain.PreAuthStatusPending, time.Now()).
		Limit(limit).Find(&items).Error
	return items, err
}
