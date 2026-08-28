package service

import (
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func resetTokenDailyQuotaState() {
	tokenDailyQuotaMemoryStore = sync.Map{}
}

func TestBillingSessionDailyQuotaLimitUsesTokenScope(t *testing.T) {
	truncate(t)
	resetTokenDailyQuotaState()

	const dailyLimit = 1000

	require.NoError(t, reserveTokenDailyQuota(201, dailyLimit, 600))
	err := reserveTokenDailyQuota(201, dailyLimit, 500)
	require.Error(t, err)
	require.Contains(t, err.Error(), "每日消费限额")

	require.NoError(t, reserveTokenDailyQuota(202, dailyLimit, 500))
}

func TestDailyQuotaSettlementOverflowExhaustsTokenForNextRequest(t *testing.T) {
	truncate(t)
	resetTokenDailyQuotaState()
	common.RedisEnabled = false

	const dailyLimit = 1000

	require.NoError(t, reserveTokenDailyQuota(204, dailyLimit, 600))
	require.Error(t, settleTokenDailyQuotaDelta(204, dailyLimit, 500))

	err := reserveTokenDailyQuota(204, dailyLimit, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "每日消费限额")
}

func TestEmitDailyQuotaLimitStreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	info := &relaycommon.RelayInfo{IsStream: true}

	emitDailyQuotaLimitStreamError(c, info, errors.New("该 API Key 已达到每日消费限额：今日最多可消费 ＄10.000000"))

	body := recorder.Body.String()
	require.Contains(t, body, "data: {\"error\":")
	require.Contains(t, body, "\"type\":\"daily_quota_limit\"")
	require.Contains(t, body, "每日消费限额")
	require.True(t, strings.HasSuffix(body, "\n\n"))
}

func TestEmitDailyQuotaLimitStreamErrorIgnoresOtherCases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		err  error
	}{
		{name: "non stream", info: &relaycommon.RelayInfo{}, err: errors.New("每日消费限额")},
		{name: "other error", info: &relaycommon.RelayInfo{IsStream: true}, err: errors.New("upstream failed")},
		{name: "nil error", info: &relaycommon.RelayInfo{IsStream: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)

			emitDailyQuotaLimitStreamError(c, tt.info, tt.err)

			require.Empty(t, recorder.Body.String())
		})
	}
}

func TestPostConsumeQuotaDailyLimitReleasesOnRefund(t *testing.T) {
	truncate(t)
	resetTokenDailyQuotaState()
	common.RedisEnabled = false

	seedUser(t, 102, 10000)
	seedToken(t, 203, 102, "sk-token-daily-3", 10000)

	info := &relaycommon.RelayInfo{
		UserId:               102,
		TokenId:              203,
		TokenKey:             "sk-token-daily-3",
		TokenDailyQuotaLimit: 1000,
	}

	require.NoError(t, PostConsumeQuota(info, 700, 0, false))

	err := PostConsumeQuota(info, 400, 0, false)
	require.Error(t, err)

	require.NoError(t, PostConsumeQuota(info, -700, 0, false))
	require.NoError(t, PostConsumeQuota(info, 400, 0, false))
}
