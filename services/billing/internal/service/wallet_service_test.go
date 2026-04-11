package service_test

import (
	"context"
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

func TestCreateWallet_WithBonus(t *testing.T) {
	dsn := "root:root123@tcp(127.0.0.1:3306)/tadpoles_billing?charset=utf8mb4&parseTime=True&loc=UTC"
	db, err := repo.NewDB(dsn)
	require.NoError(t, err)

	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6380"})
	wc := cache.NewWalletCache(rdb, 300*time.Second)

	walletRepo := repo.NewWalletRepo(db)
	txnRepo := repo.NewTransactionRepo(db)
	grantRepo := repo.NewGrantRepo(db)

	svc := service.NewWalletService(walletRepo, txnRepo, grantRepo, wc, 500, 7)
	ctx := context.Background()
	userID := "test-" + domain.NewID()

	walletID, err := svc.CreateWallet(ctx, userID)
	require.NoError(t, err)
	assert.NotEmpty(t, walletID)

	details, err := svc.GetWalletDetails(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(500), details.Balance)
	assert.Equal(t, int64(500), details.Available)
	assert.Equal(t, 1, len(details.Grants))
	assert.Equal(t, int64(500), details.Grants[0].Credits)
}
