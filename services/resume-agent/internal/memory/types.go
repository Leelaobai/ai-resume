package memory

import (
	"time"

	"github.com/google/uuid"
)

type Fact struct {
	ID         uuid.UUID `json:"id"`
	SessionID  uuid.UUID `json:"session_id"`
	Category   string    `json:"category"`
	Key        string    `json:"key"`
	Value      string    `json:"value"`
	Confidence float32   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}
