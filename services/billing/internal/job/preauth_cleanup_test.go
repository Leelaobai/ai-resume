package job_test

import (
	"context"
	"testing"
	"time"

	"github.com/Leelaobai/ai-resume/services/billing/internal/cache"
	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"github.com/Leelaobai/ai-resume/services/billing/internal/job"
	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreAuthCleanup_ExpiresAndReleasesFrozen(t *testing.T) {
	dsn := "root:root123@tcp(127.0.0.1:3306)/tadpoles_billing?charset=utf8mb4&parseTime=True&loc=UTC"
	db, err := repo.NewDB(dsn)
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6380"})
	wc := cache.NewWalletCache(rdb, 300*time.Second)

	walletRepo := repo.NewWalletRepo(db)
	paRepo := repo.NewPreAuthRepo(db)
	ctx := context.Background()

	// Create wallet with frozen balance
	userID := "test-" + domain.NewID()
	w := &domain.Wallet{ID: domain.NewID(), UserID: userID, Balance: 1000, Frozen: 500}
	require.NoError(t, walletRepo.Create(ctx, db, w))

	// Create expired pre_auth
	pa := &domain.PreAuth{
		ID:            domain.NewID(),
		UserID:        userID,
		ServiceName:   "test",
		RequestID:     "req-" + domain.NewID(),
		FrozenCredits: 500,
		Status:        domain.PreAuthStatusPending,
		ExpiresAt:     time.Now().Add(-1 * time.Minute),
	}
	require.NoError(t, paRepo.Create(ctx, db, pa))

	// Run cleanup directly
	cleanup := job.NewPreAuthCleanup(paRepo, walletRepo, wc, time.Hour, 100)
	cleanup.Run()

	wallet, err := walletRepo.GetByUserID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), wallet.Frozen)
	assert.Equal(t, int64(1000), wallet.Balance)
}
