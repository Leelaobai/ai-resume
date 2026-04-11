package job

import (
	"context"
	"log"
	"time"

	"github.com/Leelaobai/ai-resume/services/billing/internal/cache"
	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GrantCleanup struct {
	grantRepo  *repo.GrantRepo
	walletRepo *repo.WalletRepo
	txnRepo    *repo.TransactionRepo
	cache      *cache.WalletCache
	interval   time.Duration
	stopCh     chan struct{}
}

func NewGrantCleanup(grantRepo *repo.GrantRepo, walletRepo *repo.WalletRepo, txnRepo *repo.TransactionRepo, cache *cache.WalletCache, interval time.Duration) *GrantCleanup {
	return &GrantCleanup{
		grantRepo:  grantRepo,
		walletRepo: walletRepo,
		txnRepo:    txnRepo,
		cache:      cache,
		interval:   interval,
		stopCh:     make(chan struct{}),
	}
}

func (j *GrantCleanup) Start() {
	go func() {
		ticker := time.NewTicker(j.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				j.Run()
			case <-j.stopCh:
				return
			}
		}
	}()
}

func (j *GrantCleanup) Stop() {
	close(j.stopCh)
}

// Run executes one cleanup cycle. Exported for testing.
func (j *GrantCleanup) Run() {
	ctx := context.Background()
	expired, err := j.grantRepo.FindExpired(ctx, 100)
	if err != nil {
		log.Printf("[grant-cleanup] find expired error: %v", err)
		return
	}

	for _, grant := range expired {
		err := j.walletRepo.DB().Transaction(func(tx *gorm.DB) error {
			// Lock wallet and check freeze safety
			var wallet domain.Wallet
			if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("user_id = ?", grant.UserID).First(&wallet).Error; err != nil {
				return err
			}

			// Safety check: don't expire if it would violate balance >= frozen
			if wallet.Balance-grant.Remaining < wallet.Frozen {
				log.Printf("[grant-cleanup] skip grant_id=%s: balance(%d)-remaining(%d) < frozen(%d)",
					grant.ID, wallet.Balance, grant.Remaining, wallet.Frozen)
				return nil // skip, will retry next cycle
			}

			// CAS: mark grant as expired
			affected, err := j.grantRepo.MarkExpired(ctx, tx, grant.ID)
			if err != nil {
				return err
			}
			if affected == 0 {
				return nil
			}

			// Deduct from wallet
			wallet.Balance -= grant.Remaining
			if err := j.walletRepo.UpdateBalanceAndFrozen(ctx, tx, &wallet); err != nil {
				return err
			}

			// Write audit trail
			txn := &domain.CreditTransaction{
				ID:           domain.NewID(),
				UserID:       grant.UserID,
				Type:         "adjustment",
				Amount:       -grant.Remaining,
				BalanceAfter: wallet.Balance,
				Description:  "赠送积分过期",
			}
			return j.txnRepo.Create(ctx, tx, txn)
		})
		if err != nil {
			log.Printf("[grant-cleanup] cleanup grant_id=%s error: %v", grant.ID, err)
			continue
		}
		j.cache.Invalidate(ctx, grant.UserID)
	}
}
