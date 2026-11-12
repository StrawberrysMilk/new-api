package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrTokenDailyQuotaLimit = errors.New("API key daily spending limit reached")

// TokenDailyQuota is shared by all instances using the main database. Keeping
// one row per key/day makes reservations survive restarts and midnight refunds.
// Quota includes in-flight reservations as well as settled consumption.
type TokenDailyQuota struct {
	TokenID int    `gorm:"primaryKey;autoIncrement:false"`
	Day     string `gorm:"primaryKey;size:8"`
	Quota   int    `gorm:"type:bigint;not null;default:0"`
}

func ReserveTokenDailyQuota(tokenID int, day string, limit, amount int) error {
	if tokenID <= 0 || limit <= 0 {
		return nil
	}
	if amount < 0 || amount > limit {
		return ErrTokenDailyQuotaLimit
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		row := TokenDailyQuota{TokenID: tokenID, Day: day}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		query := tx.Model(&TokenDailyQuota{}).Where("token_id = ? AND day = ? AND quota < ? AND quota <= ?", tokenID, day, limit, limit-amount)
		if amount == 0 {
			// MySQL reports zero changed rows for a no-op UPDATE.
			var count int64
			if err := query.Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return ErrTokenDailyQuotaLimit
			}
			return nil
		}
		result := query.UpdateColumn("quota", gorm.Expr("quota + ?", amount))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrTokenDailyQuotaLimit
		}
		return nil
	})
}

// AdjustTokenDailyQuota records actual costs even when they exceed an estimate.
// Rejecting settlement must never turn already delivered usage into free usage.
func AdjustTokenDailyQuota(tokenID int, day string, delta int) error {
	if tokenID <= 0 || day == "" || delta == 0 {
		return nil
	}
	query := DB.Model(&TokenDailyQuota{}).Where("token_id = ? AND day = ?", tokenID, day)
	if delta < 0 {
		return query.UpdateColumn("quota", gorm.Expr("CASE WHEN quota >= ? THEN quota + ? ELSE 0 END", -delta, delta)).Error
	}
	if delta > common.MaxWalletQuota {
		return errors.New("daily quota adjustment exceeds valid range")
	}
	// Legacy callers can settle without a reservation. Never discard their
	// already delivered consumption merely because the daily limit was reached.
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&TokenDailyQuota{TokenID: tokenID, Day: day}).Error; err != nil {
		return err
	}
	result := query.Where("quota <= ?", common.MaxWalletQuota-delta).UpdateColumn("quota", gorm.Expr("quota + ?", delta))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("daily quota accounting overflow or missing reservation for token %d", tokenID)
	}
	return nil
}
