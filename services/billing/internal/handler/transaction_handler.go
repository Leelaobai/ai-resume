package handler

import (
	"net/http"
	"strconv"

	"github.com/Leelaobai/ai-resume/services/billing/internal/repo"
	"github.com/gin-gonic/gin"
)

type TransactionHandler struct {
	txnRepo *repo.TransactionRepo
}

func NewTransactionHandler(txnRepo *repo.TransactionRepo) *TransactionHandler {
	return &TransactionHandler{txnRepo: txnRepo}
}

func (h *TransactionHandler) ListTransactions(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "ERR_UNAUTHORIZED", "message": "missing X-User-Id"})
		return
	}

	txType := c.Query("type")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	items, total, err := h.txnRepo.ListByUserID(c.Request.Context(), userID, txType, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "ERR_INTERNAL", "message": err.Error()})
		return
	}

	result := make([]gin.H, 0, len(items))
	for _, item := range items {
		r := gin.H{
			"id":            item.ID,
			"type":          item.Type,
			"amount":        item.Amount,
			"balance_after": item.BalanceAfter,
			"description":   item.Description,
			"created_at":    item.CreatedAt,
		}
		if item.ServiceName != nil {
			r["service_name"] = *item.ServiceName
		}
		result = append(result, r)
	}

	c.JSON(http.StatusOK, gin.H{
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"items":     result,
	})
}
