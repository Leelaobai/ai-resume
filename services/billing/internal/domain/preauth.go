package domain

import "time"

const (
	PreAuthStatusPending   = "pending"
	PreAuthStatusSettled   = "settled"
	PreAuthStatusCancelled = "cancelled"
)

type PreAuth struct {
	ID            string     `gorm:"primaryKey;type:varchar(36)"`
	UserID        string     `gorm:"index;type:varchar(36);not null"`
	ServiceName   string     `gorm:"type:varchar(64);not null"`
	RequestID     string     `gorm:"uniqueIndex;type:varchar(128);not null"`
	FrozenCredits int64      `gorm:"not null"`
	Status        string     `gorm:"type:enum('pending','settled','cancelled');not null;default:'pending'"`
	ExpiresAt     time.Time
	SettledAt     *time.Time
	TransactionID *string    `gorm:"type:varchar(36)"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (PreAuth) TableName() string { return "pre_auths" }
