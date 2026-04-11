package repo

import (
	"context"

	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WalletRepo struct {
	db *gorm.DB
}

func NewWalletRepo(db *gorm.DB) *WalletRepo {
	return &WalletRepo{db: db}
}

// GetByUserID reads wallet without locking (for cache-aside reads).
func (r *WalletRepo) GetByUserID(ctx context.Context, userID string) (*domain.Wallet, error) {
	var w domain.Wallet
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&w).Error
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// GetByUserIDForUpdate locks the wallet row for concurrent-safe mutation.
func (r *WalletRepo) GetByUserIDForUpdate(ctx context.Context, tx *gorm.DB, userID string) (*domain.Wallet, error) {
	var w domain.Wallet
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ?", userID).First(&w).Error
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *WalletRepo) Create(ctx context.Context, tx *gorm.DB, w *domain.Wallet) error {
	return tx.WithContext(ctx).Create(w).Error
}

func (r *WalletRepo) UpdateBalanceAndFrozen(ctx context.Context, tx *gorm.DB, w *domain.Wallet) error {
	return tx.WithContext(ctx).Model(w).Updates(map[string]interface{}{
		"balance":    w.Balance,
		"frozen":     w.Frozen,
		"total_used": w.TotalUsed,
	}).Error
}

func (r *WalletRepo) DB() *gorm.DB {
	return r.db
}
