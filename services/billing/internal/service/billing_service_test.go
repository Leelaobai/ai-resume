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

func setupBillingTest(t *testing.T) (*service.BillingService, *repo.WalletRepo, *repo.GrantRepo, string) {
	t.Helper()
	dsn := "root:root123@tcp(127.0.0.1:3306)/tadpoles_billing?charset=utf8mb4&parseTime=True&loc=UTC"
	db, err := repo.NewDB(dsn)
	require.NoError(t, err)

	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6380"})
	require.NoError(t, rdb.Ping(context.Background()).Err())

	walletRepo := repo.NewWalletRepo(db)
	txnRepo := repo.NewTransactionRepo(db)
	paRepo := repo.NewPreAuthRepo(db)
	grantRepo := repo.NewGrantRepo(db)
	wc := cache.NewWalletCache(rdb, 300*time.Second)

	svc := service.NewBillingService(walletRepo, txnRepo, paRepo, grantRepo, wc, 15*time.Minute)

	userID := "test-" + domain.NewID()
	w := &domain.Wallet{ID: domain.NewID(), UserID: userID, Balance: 10000}
	require.NoError(t, walletRepo.Create(context.Background(), db, w))

	return svc, walletRepo, grantRepo, userID
}

func TestPreAuth_Success(t *testing.T) {
	svc, walletRepo, _, userID := setupBillingTest(t)
	ctx := context.Background()

	authID, frozen, err := svc.PreAuth(ctx, userID, "resume-agent", "req-"+domain.NewID(), 500)
	require.NoError(t, err)
	assert.NotEmpty(t, authID)
	assert.Equal(t, int64(500), frozen)

	w, _ := walletRepo.GetByUserID(ctx, userID)
	assert.Equal(t, int64(500), w.Frozen)
	assert.Equal(t, int64(9500), w.Available())
}

func TestPreAuth_InsufficientCredits(t *testing.T) {
	svc, _, _, userID := setupBillingTest(t)
	ctx := context.Background()

	_, _, err := svc.PreAuth(ctx, userID, "resume-agent", "req-"+domain.NewID(), 20000)
	assert.ErrorIs(t, err, service.ErrInsufficientCredits)
}

func TestPreAuth_Idempotent(t *testing.T) {
	svc, walletRepo, _, userID := setupBillingTest(t)
	ctx := context.Background()
	requestID := "req-" + domain.NewID()

	authID1, _, err := svc.PreAuth(ctx, userID, "resume-agent", requestID, 500)
	require.NoError(t, err)

	authID2, _, err := svc.PreAuth(ctx, userID, "resume-agent", requestID, 500)
	require.NoError(t, err)
	assert.Equal(t, authID1, authID2)

	w, _ := walletRepo.GetByUserID(ctx, userID)
	assert.Equal(t, int64(500), w.Frozen)
}

func TestSettle_Success(t *testing.T) {
	svc, walletRepo, _, userID := setupBillingTest(t)
	ctx := context.Background()
	requestID := "req-" + domain.NewID()

	authID, _, err := svc.PreAuth(ctx, userID, "resume-agent", requestID, 500)
	require.NoError(t, err)

	txnID, err := svc.Settle(ctx, authID, 350, "resume-agent", "test settle", requestID)
	require.NoError(t, err)
	assert.NotEmpty(t, txnID)

	w, _ := walletRepo.GetByUserID(ctx, userID)
	assert.Equal(t, int64(9650), w.Balance)
	assert.Equal(t, int64(0), w.Frozen)
	assert.Equal(t, int64(350), w.TotalUsed)
}

func TestRollback_Success(t *testing.T) {
	svc, walletRepo, _, userID := setupBillingTest(t)
	ctx := context.Background()

	authID, _, err := svc.PreAuth(ctx, userID, "resume-agent", "req-"+domain.NewID(), 500)
	require.NoError(t, err)

	err = svc.Rollback(ctx, authID)
	require.NoError(t, err)

	w, _ := walletRepo.GetByUserID(ctx, userID)
	assert.Equal(t, int64(10000), w.Balance)
	assert.Equal(t, int64(0), w.Frozen)
}

func TestFullCycle_GrantConsumedFirst(t *testing.T) {
	svc, walletRepo, grantRepo, userID := setupBillingTest(t)
	ctx := context.Background()

	dsn := "root:root123@tcp(127.0.0.1:3306)/tadpoles_billing?charset=utf8mb4&parseTime=True&loc=UTC"
	db, _ := repo.NewDB(dsn)

	grant := &domain.CreditGrant{
		ID:        domain.NewID(),
		UserID:    userID,
		Type:      "registration",
		Credits:   200,
		Remaining: 200,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	require.NoError(t, grantRepo.Create(ctx, db, grant))
	db.Model(&domain.Wallet{}).Where("user_id = ?", userID).Update("balance", 10200)

	authID, _, err := svc.PreAuth(ctx, userID, "resume-agent", "req-"+domain.NewID(), 500)
	require.NoError(t, err)

	_, err = svc.Settle(ctx, authID, 300, "resume-agent", "test", "req-settle-"+domain.NewID())
	require.NoError(t, err)

	w, _ := walletRepo.GetByUserID(ctx, userID)
	assert.Equal(t, int64(9900), w.Balance)

	grants, _ := grantRepo.GetActiveByUserID(ctx, userID)
	assert.Equal(t, 0, len(grants))
}
