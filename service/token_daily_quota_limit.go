package service

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

type tokenDailyQuotaLimitError struct{ message string }

func (e *tokenDailyQuotaLimitError) Error() string { return e.message }
func (e *tokenDailyQuotaLimitError) Unwrap() error { return model.ErrTokenDailyQuotaLimit }

func reserveTokenDailyQuota(info *relaycommon.RelayInfo, amount int) error {
	if info == nil || info.IsPlayground || info.TokenDailyQuotaLimit <= 0 {
		return nil
	}
	if info.TokenDailyQuotaDay == "" {
		day := info.StartTime
		if day.IsZero() {
			day = time.Now()
		}
		info.TokenDailyQuotaDay = day.In(time.Local).Format("20060102")
	}
	err := model.ReserveTokenDailyQuota(info.TokenId, info.TokenDailyQuotaDay, info.TokenDailyQuotaLimit, amount)
	if errors.Is(err, model.ErrTokenDailyQuotaLimit) {
		return dailyQuotaLimitError(info)
	}
	return err
}

func dailyQuotaLimitError(info *relaycommon.RelayInfo) error {
	return &tokenDailyQuotaLimitError{message: i18n.Translate(info.UserSetting.Language, "quota.token_daily_limit", map[string]any{"Limit": logger.FormatQuota(info.TokenDailyQuotaLimit)})}
}

func adjustTokenDailyQuota(info *relaycommon.RelayInfo, delta int) error {
	if info == nil || info.IsPlayground || info.TokenDailyQuotaLimit <= 0 {
		return nil
	}
	if info.TokenDailyQuotaDay == "" {
		day := info.StartTime
		if day.IsZero() {
			day = time.Now()
		}
		info.TokenDailyQuotaDay = day.In(time.Local).Format("20060102")
	}
	return model.AdjustTokenDailyQuota(info.TokenId, info.TokenDailyQuotaDay, delta)
}

func emitDailyQuotaLimitStreamError(c *gin.Context, err error) {
	if c == nil || !errors.Is(err, model.ErrTokenDailyQuotaLimit) {
		return
	}
	payload, marshalErr := common.Marshal(gin.H{"error": gin.H{"message": err.Error(), "type": "daily_quota_limit", "code": "daily_quota_limit"}})
	if marshalErr != nil {
		return
	}
	_, _ = c.Writer.Write(append(append([]byte("data: "), payload...), '\n', '\n'))
	c.Writer.Flush()
}
