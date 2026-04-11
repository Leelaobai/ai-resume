package service

import (
	"context"
	"time"

	"github.com/Leelaobai/ai-resume/services/billing/internal/cache"
	"github.com/Leelaobai/ai-resume/services/billing/internal/domain"
	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"gorm.io/gorm"
)

type WalletService struct {
	walletRepo   *repo.WalletRepo
	txnRepo      *repo.TransactionRepo
	grantRepo    *repo.GrantRepo
	cache        *cache.WalletCache
	bonusCredits int64
	bonusDays    int
}

func NewWalletService(
	walletRepo *repo.WalletRepo,
	txnRepo *repo.TransactionRepo,
	grantRepo *repo.GrantRepo,
	cache *cache.WalletCache,
	bonusCredits int64,
	bonusDays int,
) *WalletService {
	return &WalletService{
		walletRepo:   walletRepo,
		txnRepo:      txnRepo,
		grantRepo:    grantRepo,
		cache:        cache,
		bonusCredits: bonusCredits,
		bonusDays:    bonusDays,
	}
}

// CreateWallet creates wallet + registration bonus in a single transaction.
func (s *WalletService) CreateWallet(ctx context.Context, userID string) (walletID string, err error) {
	walletID = domain.NewID()

	err = s.walletRepo.DB().Transaction(func(tx *gorm.DB) error {
		wallet := &domain.Wallet{
			ID:     walletID,
			UserID: userID,
		}
		if err := s.walletRepo.Create(ctx, tx, wallet); err != nil {
			return err
		}

		if s.bonusCredits <= 0 {
			return nil
		}

		wallet.Balance = s.bonusCredits
		if err := s.walletRepo.UpdateBalanceAndFrozen(ctx, tx, wallet); err != nil {
			return err
		}

		grantID := domain.NewID()
		grant := &domain.CreditGrant{
			ID:        grantID,
			UserID:    userID,
			Type:      "registration",
			Credits:   s.bonusCredits,
			Remaining: s.bonusCredits,
			ExpiresAt: time.Now().AddDate(0, 0, s.bonusDays),
		}
		if err := s.grantRepo.Create(ctx, tx, grant); err != nil {
			return err
		}

		txnID := domain.NewID()
		txn := &domain.CreditTransaction{
			ID:           txnID,
			UserID:       userID,
			Type:         "adjustment",
			Amount:       s.bonusCredits,
			BalanceAfter: s.bonusCredits,
			Description:  "注册赠送积分",
		}
		return s.txnRepo.Create(ctx, tx, txn)
	})
	return walletID, err
}

type WalletDetails struct {
	WalletID  string
	Balance   int64
	Frozen    int64
	Available int64
	Grants    []domain.CreditGrant
	TotalUsed int64
}

func (s *WalletService) GetWalletDetails(ctx context.Context, userID string) (*WalletDetails, error) {
	wallet, err := s.walletRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	grants, err := s.grantRepo.GetActiveByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &WalletDetails{
		WalletID:  wallet.ID,
		Balance:   wallet.Balance,
		Frozen:    wallet.Frozen,
		Available: wallet.Available(),
		Grants:    grants,
		TotalUsed: wallet.TotalUsed,
	}, nil
}
