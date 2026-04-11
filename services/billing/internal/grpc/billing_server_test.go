package grpc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Leelaobai/ai-resume/services/billing/internal/cache"
	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	billinggrpc "github.com/Leelaobai/ai-resume/services/billing/internal/grpc"
	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"github.com/Leelaobai/ai-resume/services/billing/internal/service"
	pb "github.com/Leelaobai/ai-resume/services/billing/proto/billing/v1"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func startTestGRPCServer(t *testing.T) (pb.BillingServiceClient, func()) {
	t.Helper()
	dsn := "root:root123@tcp(127.0.0.1:3306)/tadpoles_billing?charset=utf8mb4&parseTime=True&loc=UTC"
	db, err := repo.NewDB(dsn)
	require.NoError(t, err)

	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6380"})
	wc := cache.NewWalletCache(rdb, 300*time.Second)

	walletRepo := repo.NewWalletRepo(db)
	txnRepo := repo.NewTransactionRepo(db)
	paRepo := repo.NewPreAuthRepo(db)
	grantRepo := repo.NewGrantRepo(db)

	billingSvc := service.NewBillingService(walletRepo, txnRepo, paRepo, grantRepo, wc, 15*time.Minute)
	walletSvc := service.NewWalletService(walletRepo, txnRepo, grantRepo, wc, 500, 7)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	pb.RegisterBillingServiceServer(srv, billinggrpc.NewBillingServer(billingSvc, walletSvc))
	go srv.Serve(lis)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	client := pb.NewBillingServiceClient(conn)
	cleanup := func() {
		conn.Close()
		srv.GracefulStop()
	}
	return client, cleanup
}

func TestGRPC_FullCycle(t *testing.T) {
	client, cleanup := startTestGRPCServer(t)
	defer cleanup()
	ctx := context.Background()
	userID := "test-" + domain.NewID()

	// CreateWallet
	createResp, err := client.CreateWallet(ctx, &pb.CreateWalletRequest{UserId: userID})
	require.NoError(t, err)
	assert.NotEmpty(t, createResp.WalletId)

	// GetBalance — should have 500 bonus
	balResp, err := client.GetBalance(ctx, &pb.GetBalanceRequest{UserId: userID})
	require.NoError(t, err)
	assert.Equal(t, int64(500), balResp.Balance)
	assert.Equal(t, int64(500), balResp.Available)

	// PreAuth
	reqID := "req-" + domain.NewID()
	paResp, err := client.PreAuth(ctx, &pb.PreAuthRequest{
		UserId:           userID,
		ServiceName:      "resume-agent",
		RequestId:        reqID,
		EstimatedCredits: 200,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, paResp.AuthId)

	// Settle
	settleResp, err := client.Settle(ctx, &pb.SettleRequest{
		AuthId:        paResp.AuthId,
		ActualCredits: 150,
		ServiceName:   "resume-agent",
		Description:   "test call",
		RequestId:     reqID,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, settleResp.TransactionId)

	// Verify final balance: 500 - 150 = 350
	balResp, err = client.GetBalance(ctx, &pb.GetBalanceRequest{UserId: userID})
	require.NoError(t, err)
	assert.Equal(t, int64(350), balResp.Balance)
	assert.Equal(t, int64(0), balResp.Frozen)
}
