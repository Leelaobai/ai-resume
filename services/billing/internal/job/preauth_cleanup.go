package job

import (
	"context"
	"log"
	"time"

	"github.com/Leelaobai/ai-resume/services/billing/internal/cache"
	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"gorm.io/gorm"
)

type PreAuthCleanup struct {
	paRepo     *repo.PreAuthRepo
	walletRepo *repo.WalletRepo
	cache      *cache.WalletCache
	interval   time.Duration
	batchSize  int
	stopCh     chan struct{}
}

func NewPreAuthCleanup(paRepo *repo.PreAuthRepo, walletRepo *repo.WalletRepo, cache *cache.WalletCache, interval time.Duration, batchSize int) *PreAuthCleanup {
	return &PreAuthCleanup{
		paRepo:     paRepo,
		walletRepo: walletRepo,
		cache:      cache,
		interval:   interval,
		batchSize:  batchSize,
		stopCh:     make(chan struct{}),
	}
}

func (j *PreAuthCleanup) Start() {
	go func() {
		// Run once immediately on start
		j.Run()
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

func (j *PreAuthCleanup) Stop() {
	close(j.stopCh)
}

// Run executes one cleanup cycle. Exported for testing.
func (j *PreAuthCleanup) Run() {
	ctx := context.Background()
	expired, err := j.paRepo.FindExpired(ctx, j.batchSize)
	if err != nil {
		log.Printf("[preauth-cleanup] find expired error: %v", err)
		return
	}

	for _, pa := range expired {
		err := j.walletRepo.DB().Transaction(func(tx *gorm.DB) error {
			affected, err := j.paRepo.CancelExpired(ctx, tx, pa.ID)
			if err != nil {
				return err
			}
			if affected == 0 {
				return nil // already handled by another instance
			}

			wallet, err := j.walletRepo.GetByUserIDForUpdate(ctx, tx, pa.UserID)
			if err != nil {
				return err
			}
			wallet.Frozen -= pa.FrozenCredits
			return j.walletRepo.UpdateBalanceAndFrozen(ctx, tx, wallet)
		})
		if err != nil {
			log.Printf("[preauth-cleanup] cleanup auth_id=%s error: %v", pa.ID, err)
			continue
		}
		j.cache.Invalidate(ctx, pa.UserID)
		log.Printf("[preauth-cleanup] cleaned auth_id=%s user=%s frozen=%d", pa.ID, pa.UserID, pa.FrozenCredits)
	}
}
