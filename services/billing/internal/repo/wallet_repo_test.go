package repo_test

import (
	"context"
	"testing"

	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *repo.WalletRepo {
	t.Helper()
	dsn := "root:root123@tcp(127.0.0.1:3306)/tadpoles_billing?charset=utf8mb4&parseTime=True&loc=UTC"
	db, err := repo.NewDB(dsn)
	require.NoError(t, err)
	db.Exec("DELETE FROM wallets WHERE user_id LIKE 'test-%'")
	return repo.NewWalletRepo(db)
}

func TestWalletRepo_CreateAndGet(t *testing.T) {
	r := setupTestDB(t)
	ctx := context.Background()

	w := &domain.Wallet{
		ID:     domain.NewID(),
		UserID: "test-" + domain.NewID(),
	}
	err := r.Create(ctx, r.DB(), w)
	require.NoError(t, err)

	got, err := r.GetByUserID(ctx, w.UserID)
	require.NoError(t, err)
	assert.Equal(t, w.ID, got.ID)
	assert.Equal(t, int64(0), got.Balance)
	assert.Equal(t, int64(0), got.Frozen)
}

func TestWalletRepo_ForUpdate(t *testing.T) {
	r := setupTestDB(t)
	ctx := context.Background()

	w := &domain.Wallet{
		ID:      domain.NewID(),
		UserID:  "test-" + domain.NewID(),
		Balance: 1000,
	}
	require.NoError(t, r.Create(ctx, r.DB(), w))

	tx := r.DB().Begin()
	locked, err := r.GetByUserIDForUpdate(ctx, tx, w.UserID)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), locked.Balance)
	assert.Equal(t, int64(1000), locked.Available())
	tx.Rollback()
}
