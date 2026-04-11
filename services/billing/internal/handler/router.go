package handler

import "github.com/gin-gonic/gin"

func NewRouter(wh *WalletHandler, th *TransactionHandler) *gin.Engine {
	r := gin.Default()
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	v1 := r.Group("/v1")
	v1.GET("/wallet", wh.GetWallet)
	v1.GET("/transactions", th.ListTransactions)

	return r
}
