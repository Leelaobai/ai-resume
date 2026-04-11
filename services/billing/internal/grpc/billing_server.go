package grpc

import (
	"context"
	"errors"

	"github.com/Leelaobai/ai-resume/services/billing/internal/service"
	pb "github.com/Leelaobai/ai-resume/services/billing/proto/billing/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type BillingServer struct {
	pb.UnimplementedBillingServiceServer
	billing *service.BillingService
	wallet  *service.WalletService
}

func NewBillingServer(billing *service.BillingService, wallet *service.WalletService) *BillingServer {
	return &BillingServer{billing: billing, wallet: wallet}
}

func (s *BillingServer) CreateWallet(ctx context.Context, req *pb.CreateWalletRequest) (*pb.CreateWalletResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	walletID, err := s.wallet.CreateWallet(ctx, req.UserId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.CreateWalletResponse{WalletId: walletID}, nil
}

func (s *BillingServer) GetBalance(ctx context.Context, req *pb.GetBalanceRequest) (*pb.GetBalanceResponse, error) {
	details, err := s.wallet.GetWalletDetails(ctx, req.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetBalanceResponse{
		Balance:   details.Balance,
		Frozen:    details.Frozen,
		Available: details.Available,
	}, nil
}

func (s *BillingServer) PreAuth(ctx context.Context, req *pb.PreAuthRequest) (*pb.PreAuthResponse, error) {
	if req.RequestId == "" || req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and request_id are required")
	}
	authID, frozen, err := s.billing.PreAuth(ctx, req.UserId, req.ServiceName, req.RequestId, req.EstimatedCredits)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.PreAuthResponse{AuthId: authID, FrozenCredits: frozen}, nil
}

func (s *BillingServer) Settle(ctx context.Context, req *pb.SettleRequest) (*pb.SettleResponse, error) {
	if req.AuthId == "" {
		return nil, status.Error(codes.InvalidArgument, "auth_id is required")
	}
	txnID, err := s.billing.Settle(ctx, req.AuthId, req.ActualCredits, req.ServiceName, req.Description, req.RequestId)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.SettleResponse{TransactionId: txnID}, nil
}

func (s *BillingServer) Rollback(ctx context.Context, req *pb.RollbackRequest) (*pb.RollbackResponse, error) {
	err := s.billing.Rollback(ctx, req.AuthId)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.RollbackResponse{Success: true}, nil
}

func (s *BillingServer) IssueGrant(ctx context.Context, req *pb.IssueGrantRequest) (*pb.IssueGrantResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func mapError(err error) error {
	switch {
	case errors.Is(err, service.ErrInsufficientCredits):
		return status.Error(codes.FailedPrecondition, "insufficient credits")
	case errors.Is(err, service.ErrWalletNotFound):
		return status.Error(codes.NotFound, "wallet not found")
	case errors.Is(err, service.ErrPreAuthNotFound):
		return status.Error(codes.NotFound, "pre_auth not found")
	case errors.Is(err, service.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, "invalid argument")
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
