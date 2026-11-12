package service

import (
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDailyQuotaDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect == "mysql" {
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			}
			if dialect == "postgres" {
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			if dialect == "sqlite" {
				sqlDB.SetMaxOpenConns(1)
			}
			previousDB := model.DB
			model.DB = db
			t.Cleanup(func() { model.DB = previousDB; require.NoError(t, sqlDB.Close()) })
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)
			// Reproduce an upgrade by creating the released Token schema without
			// the new column; the other fields/indexes use the exact current tags.
			require.NoError(t, db.AutoMigrate(&model.Token{}))
			require.NoError(t, db.AutoMigrate(&model.TokenDailyQuota{}))
			require.NoError(t, db.AutoMigrate(&model.TokenDailyQuota{}))
			t.Cleanup(func() { _ = db.Migrator().DropTable(&model.TokenDailyQuota{}, &model.Token{}) })
			require.NoError(t, db.Migrator().DropColumn(&model.Token{}, "DailyQuotaLimit"))
			require.NoError(t, db.Omit("DailyQuotaLimit").Create(&model.Token{Id: 901, Key: "preserve-key", Name: "preserved", RemainQuota: 123, Group: "vip"}).Error)
			for range 2 {
				require.NoError(t, db.AutoMigrate(&model.Token{}, &model.TokenDailyQuota{}))
			}
			var token model.Token
			require.NoError(t, db.First(&token, 901).Error)
			assert.Equal(t, "preserved", token.Name)
			assert.Equal(t, "vip", token.Group)
			assert.Equal(t, 123, token.RemainQuota)
			assert.Zero(t, token.DailyQuotaLimit)
			assert.Error(t, db.Create(&model.Token{Key: "preserve-key"}).Error)
			t.Run("atomic_reservation_refund_and_new_day", func(t *testing.T) {
				var accepted atomic.Int32
				var wg sync.WaitGroup
				for range 2 {
					wg.Go(func() {
						err := model.ReserveTokenDailyQuota(901, "20261112", 100, 60)
						if err == nil {
							accepted.Add(1)
						} else {
							assert.ErrorIs(t, err, model.ErrTokenDailyQuotaLimit)
						}
					})
				}
				wg.Wait()
				assert.EqualValues(t, 1, accepted.Load())
				require.ErrorIs(t, model.ReserveTokenDailyQuota(901, "20261112", 100, 50), model.ErrTokenDailyQuotaLimit)
				require.NoError(t, model.ReserveTokenDailyQuota(901, "20261112", 100, 40), "a rejected estimate must not consume the remaining allowance")
				require.NoError(t, model.ReserveTokenDailyQuota(901, "20261113", 100, 50))
				require.NoError(t, model.AdjustTokenDailyQuota(901, "20261112", -60))
				var rows []model.TokenDailyQuota
				require.NoError(t, db.Order("day").Find(&rows).Error)
				require.Len(t, rows, 2)
				assert.Equal(t, 40, rows[0].Quota)
				assert.Equal(t, 50, rows[1].Quota, "cross-midnight refund must not credit today's requests")
				require.NoError(t, model.ReserveTokenDailyQuota(902, "20261112", 100, 100), "other API keys have independent limits")
				require.NoError(t, model.ReserveTokenDailyQuota(903, "20261112", 0, 200), "disabled limits retain existing behavior")
			})
			t.Run("unlimited_token_still_blocks_and_streams_error", func(t *testing.T) {
				info := &relaycommon.RelayInfo{TokenId: 904, TokenUnlimited: true, TokenDailyQuotaLimit: 100, IsStream: true}
				session := &BillingSession{relayInfo: info}
				assert.False(t, session.shouldTrust(nil))
				require.NoError(t, reserveTokenDailyQuota(info, 80))
				require.NoError(t, adjustTokenDailyQuota(info, 50))
				err := reserveTokenDailyQuota(info, 1)
				require.ErrorIs(t, err, model.ErrTokenDailyQuotaLimit)
				writer := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(writer)
				emitDailyQuotaLimitStreamError(ctx, err)
				assert.Contains(t, writer.Body.String(), `"code":"daily_quota_limit"`)
				assert.Contains(t, writer.Body.String(), "data: ")
			})
		})
	}
}

func TestDailyQuotaSettlementKeepsFullCharge(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.TokenDailyQuota{}))
	t.Cleanup(func() { model.DB.Where("token_id = ?", 950).Delete(&model.TokenDailyQuota{}) })
	seedUser(t, 950, 1000)
	seedToken(t, 950, 950, "daily-settle", 1000)
	info := &relaycommon.RelayInfo{UserId: 950, TokenId: 950, TokenKey: "daily-settle", TokenUnlimited: true, TokenDailyQuotaLimit: 100}
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	session := &BillingSession{relayInfo: info, funding: &WalletFunding{userId: 950}}
	require.Nil(t, session.preConsume(ctx, 60))
	info.Billing = session
	info.IsStream = true
	common.SetContextKey(ctx, constant.ContextKeyDailyQuotaPendingDone, true)
	require.ErrorIs(t, SettleBilling(ctx, info, 130), model.ErrTokenDailyQuotaLimit)
	assert.Regexp(t, `(?s)"code":"daily_quota_limit".*data: \[DONE\]`, writer.Body.String())
	require.NoError(t, session.Settle(130), "settlement must be idempotent")
	var user model.User
	require.NoError(t, model.DB.First(&user, 950).Error)
	assert.Equal(t, 870, user.Quota, "over-limit completion is still charged in full")
	var day model.TokenDailyQuota
	require.NoError(t, model.DB.Where("token_id = ?", 950).First(&day).Error)
	assert.Equal(t, 130, day.Quota)
	require.ErrorIs(t, reserveTokenDailyQuota(info, 1), model.ErrTokenDailyQuotaLimit)
}

func TestDailyQuotaFailedReservationPreservesBalances(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.TokenDailyQuota{}))
	t.Cleanup(func() { model.DB.Where("token_id = ?", 951).Delete(&model.TokenDailyQuota{}) })
	seedUser(t, 951, 10)
	seedToken(t, 951, 951, "daily-rollback", 1000)
	info := &relaycommon.RelayInfo{UserId: 951, TokenId: 951, TokenKey: "daily-rollback", TokenDailyQuotaLimit: 100}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	session := &BillingSession{relayInfo: info, funding: &WalletFunding{userId: 951}}
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	require.NotNil(t, session.preConsume(ctx, 60), "wallet failure must release daily reservations")
	var day model.TokenDailyQuota
	require.NoError(t, model.DB.Where("token_id = ?", 951).First(&day).Error)
	assert.Zero(t, day.Quota)
	require.Nil(t, session.preConsume(ctx, 10))
	require.Error(t, session.ReserveBeforeSubmit(101))
	var user model.User
	require.NoError(t, model.DB.First(&user, 951).Error)
	assert.Zero(t, user.Quota, "rejected daily reservation must not take more wallet funds")
	require.NoError(t, model.DB.Where("token_id = ?", 951).First(&day).Error)
	assert.Equal(t, 10, day.Quota)
}

func TestDailyQuotaTaskRefundUsesOriginalDay(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.TokenDailyQuota{}))
	t.Cleanup(func() { model.DB.Where("token_id = ?", 952).Delete(&model.TokenDailyQuota{}) })
	seedUser(t, 952, 900)
	seedToken(t, 952, 952, "daily-task-refund", 900)
	require.NoError(t, model.ReserveTokenDailyQuota(952, "20261111", 100, 100))
	require.NoError(t, model.ReserveTokenDailyQuota(952, "20261112", 100, 50))
	task := makeTask(952, 0, 100, 952, BillingSourceWallet, 0)
	task.PrivateData.TokenDailyQuotaDay = "20261111"
	require.NoError(t, task.Insert())
	require.True(t, RefundTaskQuota(t.Context(), task, "upstream failed"))
	var rows []model.TokenDailyQuota
	require.NoError(t, model.DB.Where("token_id = ?", 952).Order("day").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Zero(t, rows[0].Quota)
	assert.Equal(t, 50, rows[1].Quota)
	var user model.User
	require.NoError(t, model.DB.First(&user, 952).Error)
	assert.Equal(t, 1000, user.Quota)
}
