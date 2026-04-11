package domain

import (
	"time"

	"gorm.io/gorm"
)

type TopupOrder struct {
	ID             string         `gorm:"primaryKey;type:varchar(36)"`
	UserID         string         `gorm:"index;type:varchar(36);not null"`
	Credits        int64          `gorm:"not null"`
	AmountFen      int64          `gorm:"not null"`
	PayCurrency    string         `gorm:"type:varchar(8);not null;default:'CNY'"`
	PayAmountFen   int64          `gorm:"not null"`
	PaymentChannel string         `gorm:"type:enum('wechat','alipay','stripe');not null"`
	PaymentOrderID *string        `gorm:"type:varchar(128)"`
	Status         string         `gorm:"type:enum('pending','paid','failed','refunded');not null;default:'pending'"`
	PaidAt         *time.Time
	TransactionID  *string        `gorm:"type:varchar(36)"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      gorm.DeletedAt `gorm:"index"`
}

func (TopupOrder) TableName() string { return "topup_orders" }
