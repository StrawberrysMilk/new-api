package controller

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestMediaWorkbenchAuthorizationBoundary(t *testing.T) {
	oldDB, oldRedis, oldSecret := model.DB, common.RedisEnabled, common.SessionSecret
	oldDialect := common.MainDatabaseType()
	t.Cleanup(func() { common.SetMainDatabaseType(oldDialect) })
	var dialect gorm.Dialector = sqlite.Open(":memory:")
	if dsn := os.Getenv("MEDIA_WORKBENCH_TEST_MYSQL_DSN"); dsn != "" {
		dialect = mysql.Open(dsn)
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	}
	if dsn := os.Getenv("MEDIA_WORKBENCH_TEST_POSTGRES_DSN"); dsn != "" {
		dialect = postgres.Open(dsn)
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	}
	db, err := gorm.Open(dialect, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}, &model.Token{}))
	model.DB, common.RedisEnabled, common.SessionSecret = db, false, "media-test-session-secret"
	t.Cleanup(func() { model.DB, common.RedisEnabled, common.SessionSecret = oldDB, oldRedis, oldSecret })
	t.Setenv("MEDIA_WORKBENCH_ENABLED", "true")
	t.Setenv("MEDIA_WORKBENCH_CALLBACK_URL", "http://127.0.0.1:5182/api/auth/resnewapi/callback")
	secret := strings.Repeat("s", 40)
	t.Setenv("MEDIA_WORKBENCH_CLIENT_SECRET", secret)
	userName := "media-" + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 10)
	user := model.User{Username: userName, Password: "unused", Role: 1, Status: common.UserStatusEnabled, Group: "default", AffCode: userName + "-aff", AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "workbench-test")
	require.NoError(t, err)
	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	request := func(handler gin.HandlerFunc, payload any, grant string, clientSecret string, authenticated bool, id string) *httptest.ResponseRecorder {
		body, marshalErr := common.Marshal(payload)
		require.NoError(t, marshalErr)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		c.Request.Header.Set("X-Media-Client-Secret", clientSecret)
		c.Request.Header.Set("Origin", "http://127.0.0.1:5182")
		c.Request.Header.Set("Authorization", "Bearer "+grant)
		if authenticated {
			c.Set("id", identity.UserID)
			c.Set("session_id", identity.SessionID)
			c.Set("auth_version", identity.UserAuthVersion)
			c.Set("session_version", identity.SessionVersion)
		}
		c.Params = gin.Params{{Key: "id", Value: id}}
		handler(c)
		return recorder
	}
	getCode := func() string {
		r := request(MediaWorkbenchAuthorize, gin.H{"challenge": challenge, "state": strings.Repeat("a", 43)}, "", "", true, "")
		require.Equal(t, 200, r.Code)
		var response struct {
			Data struct {
				RedirectURL string `json:"redirect_url"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(r.Body.Bytes(), &response))
		u, err := url.Parse(response.Data.RedirectURL)
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1:5182", u.Host)
		return u.Query().Get("code")
	}
	t.Run("disabled integration and unauthenticated callers cannot authorize", func(t *testing.T) {
		t.Setenv("MEDIA_WORKBENCH_ENABLED", "false")
		require.Equal(t, 404, request(MediaWorkbenchAuthorize, gin.H{}, "", "", true, "").Code)
		t.Setenv("MEDIA_WORKBENCH_ENABLED", "true")
		require.Equal(t, 403, request(MediaWorkbenchAuthorize, gin.H{}, "", "", false, "").Code)
		t.Setenv("MEDIA_WORKBENCH_CALLBACK_URL", "http://evil.example/api/auth/resnewapi/callback")
		_, _, enabled := mediaWorkbenchConfig()
		require.False(t, enabled)
	})
	require.JSONEq(t, `{"success":true,"data":{"enabled":true,"start_url":"http://127.0.0.1:5182/login"}}`, request(MediaWorkbenchConfig, gin.H{}, "", "", false, "").Body.String())
	code := getCode()
	require.Equal(t, 401, request(MediaWorkbenchExchange, gin.H{"code": code, "verifier": verifier}, "", "wrong", false, "").Code)
	require.Equal(t, 401, request(MediaWorkbenchExchange, gin.H{"code": code, "verifier": strings.Repeat("x", 43)}, "", secret, false, "").Code)
	r := request(MediaWorkbenchExchange, gin.H{"code": code, "verifier": verifier}, "", secret, false, "")
	require.Equal(t, 200, r.Code)
	var exchange struct {
		Data struct {
			Grant string `json:"grant"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(r.Body.Bytes(), &exchange))
	grant := exchange.Data.Grant
	require.NotEmpty(t, grant)
	require.Equal(t, 401, request(MediaWorkbenchExchange, gin.H{"code": code, "verifier": verifier}, "", secret, false, "").Code)
	r = request(MediaWorkbenchContext, gin.H{}, grant, secret, false, "")
	require.Equal(t, 200, r.Code)
	require.NotContains(t, r.Body.String(), "quota")
	require.NotContains(t, r.Body.String(), "balance")
	require.Equal(t, 401, request(MediaWorkbenchContext, gin.H{}, grant+"x", secret, false, "").Code)
	owned := model.Token{UserId: user.Id, Key: "owned-key-" + strconv.FormatInt(time.Now().UnixNano(), 10), Name: "owned", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	foreign := model.Token{UserId: user.Id + 1, Key: "other-key-" + strconv.FormatInt(time.Now().UnixNano(), 10), Name: "other", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
	require.NoError(t, db.Create(&owned).Error)
	require.NoError(t, db.Create(&foreign).Error)
	require.Equal(t, 200, request(MediaWorkbenchKey, gin.H{}, grant, secret, false, strconv.Itoa(owned.Id)).Code)
	require.Equal(t, 404, request(MediaWorkbenchKey, gin.H{}, grant, secret, false, strconv.Itoa(foreign.Id)).Code)
	require.Equal(t, 200, request(MediaWorkbenchModels, gin.H{}, grant, secret, false, strconv.Itoa(owned.Id)).Code)
	require.Equal(t, 404, request(MediaWorkbenchModels, gin.H{}, grant, secret, false, strconv.Itoa(foreign.Id)).Code)
	require.Equal(t, 401, request(MediaWorkbenchModels, gin.H{}, grant, "wrong", false, strconv.Itoa(owned.Id)).Code)
	expired := getCode()
	flow, err := model.GetAuthFlow(expired, model.AuthFlowMatch{Purpose: mediaCodePurpose})
	require.NoError(t, err)
	require.NoError(t, db.Model(flow).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	require.Equal(t, 401, request(MediaWorkbenchExchange, gin.H{"code": expired, "verifier": verifier}, "", secret, false, "").Code)
	pending := getCode()
	_, err = model.RevokeUserSession(user.Id, identity.SessionID, "test")
	require.NoError(t, err)
	require.Equal(t, 401, request(MediaWorkbenchExchange, gin.H{"code": pending, "verifier": verifier}, "", secret, false, "").Code)
	require.Equal(t, 401, request(MediaWorkbenchContext, gin.H{}, grant, secret, false, "").Code)
}
