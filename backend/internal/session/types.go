package session

import (
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID         uuid.UUID `json:"id"`
	SessionID  uuid.UUID `json:"session_id"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	ToolCalls  *string   `json:"tool_calls,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
