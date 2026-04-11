package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	ServerPort               string
	GRPCPort                 string
	DBDSN                    string
	RedisAddr                string
	PreAuthTTL               time.Duration
	WalletCacheTTL           time.Duration
	CleanupInterval          time.Duration
	CleanupBatchSize         int
	RegistrationBonusCredits int64
	RegistrationBonusDays    int
	GrantCleanupInterval     time.Duration
	ShutdownTimeout          time.Duration
	UserServiceGRPCAddr      string
}

func Load() *Config {
	return &Config{
		ServerPort:               envOrDefault("SERVER_PORT", "8084"),
		GRPCPort:                 envOrDefault("GRPC_PORT", "9094"),
		DBDSN:                    envOrDefault("DB_DSN", "root:root123@tcp(127.0.0.1:3306)/tadpoles_billing?charset=utf8mb4&parseTime=True&loc=UTC"),
		RedisAddr:                envOrDefault("REDIS_ADDR", "127.0.0.1:6380"),
		PreAuthTTL:               time.Duration(envOrDefaultInt("PREAUTH_TTL_MINUTES", 15)) * time.Minute,
		WalletCacheTTL:           time.Duration(envOrDefaultInt("WALLET_CACHE_TTL_SECONDS", 300)) * time.Second,
		CleanupInterval:          time.Duration(envOrDefaultInt("CLEANUP_INTERVAL_SECONDS", 60)) * time.Second,
		CleanupBatchSize:         envOrDefaultInt("CLEANUP_BATCH_SIZE", 100),
		RegistrationBonusCredits: int64(envOrDefaultInt("REGISTRATION_BONUS_CREDITS", 500)),
		RegistrationBonusDays:    envOrDefaultInt("REGISTRATION_BONUS_DAYS", 7),
		GrantCleanupInterval:     time.Duration(envOrDefaultInt("GRANT_CLEANUP_INTERVAL_SECONDS", 3600)) * time.Second,
		ShutdownTimeout:          time.Duration(envOrDefaultInt("SHUTDOWN_TIMEOUT_SECONDS", 15)) * time.Second,
		UserServiceGRPCAddr:      envOrDefault("USER_SERVICE_GRPC_ADDR", "127.0.0.1:9092"),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
