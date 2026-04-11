package handler

import (
	"net/http"

	"github.com/Leelaobai/ai-resume/services/billing/internal/service"
	"github.com/gin-gonic/gin"
)

type WalletHandler struct {
	walletSvc *service.WalletService
}

func NewWalletHandler(walletSvc *service.WalletService) *WalletHandler {
	return &WalletHandler{walletSvc: walletSvc}
}

func (h *WalletHandler) GetWallet(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "ERR_UNAUTHORIZED", "message": "missing X-User-Id"})
		return
	}

	details, err := h.walletSvc.GetWalletDetails(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "ERR_WALLET_NOT_FOUND", "message": "wallet not found"})
		return
	}

	grants := make([]gin.H, 0, len(details.Grants))
	for _, g := range details.Grants {
		grants = append(grants, gin.H{
			"grant_id":   g.ID,
			"type":       g.Type,
			"credits":    g.Credits,
			"remaining":  g.Remaining,
			"expires_at": g.ExpiresAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"wallet_id": details.WalletID,
		"balance":   details.Balance,
		"frozen":    details.Frozen,
		"available": details.Available,
		"currency":  "credits",
		"grants":    grants,
		"stats": gin.H{
			"total_used": details.TotalUsed,
		},
	})
}
