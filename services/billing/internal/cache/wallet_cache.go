package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type WalletCache struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewWalletCache(rdb *redis.Client, ttl time.Duration) *WalletCache {
	return &WalletCache{rdb: rdb, ttl: ttl}
}

// Invalidate deletes the cache — called after any wallet mutation.
func (c *WalletCache) Invalidate(ctx context.Context, userID string) {
	c.rdb.Del(ctx, fmt.Sprintf("wallet:balance:%s", userID))
}

// SetPreAuthIdempotent stores a pre_auth idempotency key.
func (c *WalletCache) SetPreAuthIdempotent(ctx context.Context, requestID, authID string, ttl time.Duration) {
	c.rdb.Set(ctx, fmt.Sprintf("preauth:idempotent:%s", requestID), authID, ttl)
}

// GetPreAuthIdempotent checks if a PreAuth was already created for this requestID.
func (c *WalletCache) GetPreAuthIdempotent(ctx context.Context, requestID string) (string, bool) {
	val, err := c.rdb.Get(ctx, fmt.Sprintf("preauth:idempotent:%s", requestID)).Result()
	if err != nil {
		return "", false
	}
	return val, true
}
