package tools

import (
	"context"

	"github.com/google/uuid"
)

type contextKey string

const sessionIDKey contextKey = "session_id"

func WithSessionID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, sessionIDKey, id)
}

func SessionIDFromContext(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(sessionIDKey).(uuid.UUID)
	return id
}
