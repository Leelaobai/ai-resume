package repo

import (
	"context"

	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"gorm.io/gorm"
)

type TransactionRepo struct {
	db *gorm.DB
}

func NewTransactionRepo(db *gorm.DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) Create(ctx context.Context, tx *gorm.DB, t *domain.CreditTransaction) error {
	return tx.WithContext(ctx).Create(t).Error
}

func (r *TransactionRepo) ListByUserID(ctx context.Context, userID string, txType string, page, pageSize int) ([]domain.CreditTransaction, int64, error) {
	var items []domain.CreditTransaction
	var total int64
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if txType != "" {
		q = q.Where("type = ?", txType)
	}
	q.Model(&domain.CreditTransaction{}).Count(&total)
	err := q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return items, total, err
}
