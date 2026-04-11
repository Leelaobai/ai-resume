package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Leelaobai/ai-resume/services/billing/internal/cache"
	"github.com/Leelaobai/ai-resume/services/billing/internal/config"
	billinggrpc "github.com/Leelaobai/ai-resume/services/billing/internal/grpc"
	"github.com/Leelaobai/ai-resume/services/billing/internal/handler"
	"github.com/Leelaobai/ai-resume/services/billing/internal/job"
	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"github.com/Leelaobai/ai-resume/services/billing/internal/service"
	pb "github.com/Leelaobai/ai-resume/services/billing/proto/billing/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

func main() {
	cfg := config.Load()

	// Database
	db, err := repo.NewDB(cfg.DBDSN)
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}

	// Redis
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("failed to connect redis: %v", err)
	}

	// Repos
	walletRepo := repo.NewWalletRepo(db)
	txnRepo := repo.NewTransactionRepo(db)
	paRepo := repo.NewPreAuthRepo(db)
	grantRepo := repo.NewGrantRepo(db)

	// Cache
	wc := cache.NewWalletCache(rdb, cfg.WalletCacheTTL)

	// Services
	billingSvc := service.NewBillingService(walletRepo, txnRepo, paRepo, grantRepo, wc, cfg.PreAuthTTL)
	walletSvc := service.NewWalletService(walletRepo, txnRepo, grantRepo, wc, cfg.RegistrationBonusCredits, cfg.RegistrationBonusDays)

	// Background jobs
	paCleanup := job.NewPreAuthCleanup(paRepo, walletRepo, wc, cfg.CleanupInterval, cfg.CleanupBatchSize)
	paCleanup.Start()
	grantCleanup := job.NewGrantCleanup(grantRepo, walletRepo, txnRepo, wc, cfg.GrantCleanupInterval)
	grantCleanup.Start()

	// gRPC server
	grpcLis, err := net.Listen("tcp", fmt.Sprintf(":%s", cfg.GRPCPort))
	if err != nil {
		log.Fatalf("failed to listen gRPC: %v", err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterBillingServiceServer(grpcServer, billinggrpc.NewBillingServer(billingSvc, walletSvc))
	go func() {
		log.Printf("gRPC server listening on :%s", cfg.GRPCPort)
		if err := grpcServer.Serve(grpcLis); err != nil {
			log.Printf("gRPC server error: %v", err)
		}
	}()

	// HTTP server
	wh := handler.NewWalletHandler(walletSvc)
	th := handler.NewTransactionHandler(txnRepo)
	router := handler.NewRouter(wh, th)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%s", cfg.ServerPort),
		Handler: router,
	}
	go func() {
		log.Printf("HTTP server listening on :%s", cfg.ServerPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit
	log.Println("Shutting down...")

	paCleanup.Stop()
	grantCleanup.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	grpcServer.GracefulStop()
	httpServer.Shutdown(ctx)

	sqlDB, _ := db.DB()
	sqlDB.Close()
	rdb.Close()

	log.Println("Billing Service stopped")
}
