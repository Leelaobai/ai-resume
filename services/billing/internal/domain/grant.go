package domain

import (
	"time"

	"gorm.io/gorm"
)

type CreditGrant struct {
	ID            string         `gorm:"primaryKey;type:varchar(36)"`
	UserID        string         `gorm:"index;type:varchar(36);not null"`
	Type          string         `gorm:"type:enum('registration','promotion','referral');not null"`
	Credits       int64          `gorm:"not null"`
	Remaining     int64          `gorm:"not null"`
	ExpiresAt     time.Time
	ExpiredAt     *time.Time
	TransactionID *string        `gorm:"type:varchar(36)"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     gorm.DeletedAt `gorm:"index"`
}

func (CreditGrant) TableName() string { return "credit_grants" }
