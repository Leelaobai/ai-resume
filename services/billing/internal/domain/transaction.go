package domain

import "time"

type CreditTransaction struct {
	ID           string    `gorm:"primaryKey;type:varchar(36)"`
	UserID       string    `gorm:"index;type:varchar(36);not null"`
	Type         string    `gorm:"type:enum('topup','usage','refund','adjustment');not null"`
	ServiceName  *string   `gorm:"type:varchar(64)"`
	Amount       int64     `gorm:"not null"`
	BalanceAfter int64     `gorm:"not null"`
	Description  string    `gorm:"type:varchar(255);not null;default:''"`
	Status       string    `gorm:"type:enum('completed','cancelled');not null;default:'completed'"`
	AuthID       *string   `gorm:"type:varchar(36)"`
	RequestID    *string   `gorm:"index;type:varchar(128)"`
	CreatedAt    time.Time
}

func (CreditTransaction) TableName() string { return "credit_transactions" }
