package main

import (
	"fmt"

	"github.com/Leelaobai/ai-resume/services/billing/internal/config"
)

func main() {
	cfg := config.Load()
	fmt.Printf("Billing Service starting on :%s (gRPC :%s)\n", cfg.ServerPort, cfg.GRPCPort)
}
