package load_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Leelaobai/ai-resume/services/billing/internal/cache"
	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"github.com/Leelaobai/ai-resume/services/billing/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConcurrentPreAuth_NoOverDeduction verifies that concurrent PreAuth
// calls never freeze more credits than available (pessimistic locking).
func TestConcurrentPreAuth_NoOverDeduction(t *testing.T) {
	dsn := "root:root123@tcp(127.0.0.1:3306)/tadpoles_billing?charset=utf8mb4&parseTime=True&loc=UTC"
	db, err := repo.NewDB(dsn)
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6380"})
	wc := cache.NewWalletCache(rdb, 300*time.Second)

	walletRepo := repo.NewWalletRepo(db)
	txnRepo := repo.NewTransactionRepo(db)
	paRepo := repo.NewPreAuthRepo(db)
	grantRepo := repo.NewGrantRepo(db)

	svc := service.NewBillingService(walletRepo, txnRepo, paRepo, grantRepo, wc, 15*time.Minute)
	ctx := context.Background()

	// Create wallet with exactly 1000 credits
	userID := "loadtest-" + domain.NewID()
	w := &domain.Wallet{ID: domain.NewID(), UserID: userID, Balance: 1000}
	require.NoError(t, walletRepo.Create(ctx, db, w))

	// 50 goroutines each try to PreAuth 100 credits
	// Only 10 should succeed (1000 / 100 = 10)
	concurrency := 50
	creditsPerRequest := int64(100)

	var successCount int64
	var failCount int64
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _, err := svc.PreAuth(ctx, userID, "load-test", fmt.Sprintf("load-%s-%d", userID, idx), creditsPerRequest)
			if err != nil {
				atomic.AddInt64(&failCount, 1)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}
	wg.Wait()

	t.Logf("Success: %d, Failed: %d", successCount, failCount)

	// Exactly 10 should succeed
	assert.Equal(t, int64(10), successCount)
	assert.Equal(t, int64(40), failCount)

	// Verify wallet state: frozen = 1000, available = 0
	wallet, err := walletRepo.GetByUserID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), wallet.Frozen)
	assert.Equal(t, int64(0), wallet.Available())
}

// TestConcurrentPreAuthAndSettle verifies interleaved PreAuth+Settle correctness.
func TestConcurrentPreAuthAndSettle(t *testing.T) {
	dsn := "root:root123@tcp(127.0.0.1:3306)/tadpoles_billing?charset=utf8mb4&parseTime=True&loc=UTC"
	db, err := repo.NewDB(dsn)
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6380"})
	wc := cache.NewWalletCache(rdb, 300*time.Second)

	walletRepo := repo.NewWalletRepo(db)
	txnRepo := repo.NewTransactionRepo(db)
	paRepo := repo.NewPreAuthRepo(db)
	grantRepo := repo.NewGrantRepo(db)

	svc := service.NewBillingService(walletRepo, txnRepo, paRepo, grantRepo, wc, 15*time.Minute)
	ctx := context.Background()

	userID := "loadtest-" + domain.NewID()
	w := &domain.Wallet{ID: domain.NewID(), UserID: userID, Balance: 5000}
	require.NoError(t, walletRepo.Create(ctx, db, w))

	// 20 concurrent PreAuth → Settle cycles, each 100 credits
	cycles := 20
	var wg sync.WaitGroup
	var settledTotal int64

	for i := 0; i < cycles; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reqID := fmt.Sprintf("cycle-%s-%d", userID, idx)
			authID, _, err := svc.PreAuth(ctx, userID, "load-test", reqID, 200)
			if err != nil {
				return
			}
			_, err = svc.Settle(ctx, authID, 100, "load-test", "cycle test", reqID)
			if err == nil {
				atomic.AddInt64(&settledTotal, 100)
			}
		}(i)
	}
	wg.Wait()

	wallet, err := walletRepo.GetByUserID(ctx, userID)
	require.NoError(t, err)

	// Final balance should be exactly: 5000 - settledTotal
	expectedBalance := int64(5000) - settledTotal
	assert.Equal(t, expectedBalance, wallet.Balance)
	assert.Equal(t, int64(0), wallet.Frozen)
	assert.Equal(t, settledTotal, wallet.TotalUsed)

	t.Logf("Settled %d credits across %d cycles, final balance: %d", settledTotal, cycles, wallet.Balance)
}
