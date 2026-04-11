package service

import (
	"context"
	"errors"
	"time"

	"github.com/Leelaobai/ai-resume/services/billing/internal/cache"
	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"gorm.io/gorm"
)

var (
	ErrInsufficientCredits = errors.New("insufficient credits")
	ErrWalletNotFound      = errors.New("wallet not found")
	ErrPreAuthNotFound     = errors.New("pre_auth not found")
	ErrInvalidArgument     = errors.New("invalid argument")
)

type BillingService struct {
	walletRepo *repo.WalletRepo
	txnRepo    *repo.TransactionRepo
	paRepo     *repo.PreAuthRepo
	grantRepo  *repo.GrantRepo
	cache      *cache.WalletCache
	preAuthTTL time.Duration
}

func NewBillingService(
	walletRepo *repo.WalletRepo,
	txnRepo *repo.TransactionRepo,
	paRepo *repo.PreAuthRepo,
	grantRepo *repo.GrantRepo,
	cache *cache.WalletCache,
	preAuthTTL time.Duration,
) *BillingService {
	return &BillingService{
		walletRepo: walletRepo,
		txnRepo:    txnRepo,
		paRepo:     paRepo,
		grantRepo:  grantRepo,
		cache:      cache,
		preAuthTTL: preAuthTTL,
	}
}

// PreAuth freezes estimated credits. Idempotent on request_id.
func (s *BillingService) PreAuth(ctx context.Context, userID, serviceName, requestID string, estimatedCredits int64) (authID string, frozenCredits int64, err error) {
	if estimatedCredits <= 0 {
		return "", 0, ErrInvalidArgument
	}

	// Check Redis idempotent key first
	if existingAuthID, ok := s.cache.GetPreAuthIdempotent(ctx, requestID); ok {
		pa, err := s.paRepo.GetByRequestID(ctx, requestID)
		if err == nil {
			return existingAuthID, pa.FrozenCredits, nil
		}
	}

	authID = domain.NewID()
	err = s.walletRepo.DB().Transaction(func(tx *gorm.DB) error {
		wallet, err := s.walletRepo.GetByUserIDForUpdate(ctx, tx, userID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWalletNotFound
			}
			return err
		}

		if wallet.Available() < estimatedCredits {
			return ErrInsufficientCredits
		}

		pa := &domain.PreAuth{
			ID:            authID,
			UserID:        userID,
			ServiceName:   serviceName,
			RequestID:     requestID,
			FrozenCredits: estimatedCredits,
			Status:        domain.PreAuthStatusPending,
			ExpiresAt:     time.Now().Add(s.preAuthTTL),
		}
		if err := s.paRepo.Create(ctx, tx, pa); err != nil {
			// Duplicate request_id — idempotent fallback
			existing, dbErr := s.paRepo.GetByRequestID(ctx, requestID)
			if dbErr == nil {
				authID = existing.ID
				frozenCredits = existing.FrozenCredits
				return nil
			}
			return err
		}

		wallet.Frozen += estimatedCredits
		if err := s.walletRepo.UpdateBalanceAndFrozen(ctx, tx, wallet); err != nil {
			return err
		}

		frozenCredits = estimatedCredits
		return nil
	})
	if err != nil {
		return "", 0, err
	}

	s.cache.SetPreAuthIdempotent(ctx, requestID, authID, s.preAuthTTL)
	s.cache.Invalidate(ctx, userID)
	return authID, frozenCredits, nil
}

// Settle deducts actual credits, releases excess frozen amount.
// Prioritizes expiring grants (FIFO by expires_at).
func (s *BillingService) Settle(ctx context.Context, authID string, actualCredits int64, serviceName, description, requestID string) (transactionID string, err error) {
	transactionID = domain.NewID()
	var userID string

	err = s.walletRepo.DB().Transaction(func(tx *gorm.DB) error {
		pa, err := s.paRepo.GetByIDForUpdate(ctx, tx, authID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPreAuthNotFound
			}
			return err
		}
		if actualCredits > pa.FrozenCredits {
			return ErrInvalidArgument
		}
		userID = pa.UserID

		wallet, err := s.walletRepo.GetByUserIDForUpdate(ctx, tx, userID)
		if err != nil {
			return err
		}

		// Consume grants first (FIFO by expiry)
		grants, err := s.grantRepo.GetActiveByUserIDForUpdate(ctx, tx, userID)
		if err != nil {
			return err
		}
		toDeduct := actualCredits
		for i := range grants {
			if toDeduct <= 0 {
				break
			}
			used := min(grants[i].Remaining, toDeduct)
			grants[i].Remaining -= used
			if err := s.grantRepo.UpdateRemaining(ctx, tx, &grants[i]); err != nil {
				return err
			}
			toDeduct -= used
		}

		balanceAfter := wallet.Balance - actualCredits
		wallet.Balance = balanceAfter
		wallet.Frozen -= pa.FrozenCredits
		wallet.TotalUsed += actualCredits
		if err := s.walletRepo.UpdateBalanceAndFrozen(ctx, tx, wallet); err != nil {
			return err
		}

		txn := &domain.CreditTransaction{
			ID:           transactionID,
			UserID:       userID,
			Type:         "usage",
			ServiceName:  &serviceName,
			Amount:       -actualCredits,
			BalanceAfter: balanceAfter,
			Description:  description,
			AuthID:       &authID,
			RequestID:    &requestID,
		}
		if err := s.txnRepo.Create(ctx, tx, txn); err != nil {
			return err
		}

		now := time.Now()
		pa.Status = domain.PreAuthStatusSettled
		pa.SettledAt = &now
		pa.TransactionID = &transactionID
		return s.paRepo.UpdateStatus(ctx, tx, pa)
	})
	if err != nil {
		return "", err
	}

	s.cache.Invalidate(ctx, userID)
	return transactionID, nil
}

// Rollback releases frozen credits. Idempotent — already settled/cancelled returns success.
func (s *BillingService) Rollback(ctx context.Context, authID string) error {
	var userID string
	err := s.walletRepo.DB().Transaction(func(tx *gorm.DB) error {
		pa, err := s.paRepo.GetByIDForUpdate(ctx, tx, authID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // already settled or cancelled, idempotent success
			}
			return err
		}
		userID = pa.UserID

		wallet, err := s.walletRepo.GetByUserIDForUpdate(ctx, tx, userID)
		if err != nil {
			return err
		}

		wallet.Frozen -= pa.FrozenCredits
		if err := s.walletRepo.UpdateBalanceAndFrozen(ctx, tx, wallet); err != nil {
			return err
		}

		pa.Status = domain.PreAuthStatusCancelled
		return s.paRepo.UpdateStatus(ctx, tx, pa)
	})
	if err != nil {
		return err
	}
	if userID != "" {
		s.cache.Invalidate(ctx, userID)
	}
	return nil
}
