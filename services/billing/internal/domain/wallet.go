package domain

import (
	"time"

	"gorm.io/gorm"
)

type Wallet struct {
	ID        string         `gorm:"primaryKey;type:varchar(36)"`
	UserID    string         `gorm:"uniqueIndex;type:varchar(36);not null"`
	Balance   int64          `gorm:"not null;default:0"`
	Frozen    int64          `gorm:"not null;default:0"`
	TotalUsed int64          `gorm:"not null;default:0"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

func (w *Wallet) Available() int64 {
	return w.Balance - w.Frozen
}
