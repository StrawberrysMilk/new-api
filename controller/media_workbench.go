package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const mediaCodePurpose = "media_workbench"

var mediaPKCEPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type mediaAuthorization struct {
	Identity  service.AuthIdentity `json:"identity"`
	Challenge string               `json:"challenge"`
	ExpiresAt int64                `json:"expires_at"`
	Callback  string               `json:"callback"`
}

// Configuration is deliberately opt-in and restricted to one operator-owned client.
func mediaWorkbenchConfig() (string, string, bool) {
	callback := strings.TrimSpace(os.Getenv("MEDIA_WORKBENCH_CALLBACK_URL"))
	secret := os.Getenv("MEDIA_WORKBENCH_CLIENT_SECRET")
	u, err := url.Parse(callback)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/auth/resnewapi/callback" || len(secret) < 32 {
		return "", "", false
	}
	local := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", "", false
	}
	return callback, secret, os.Getenv("MEDIA_WORKBENCH_ENABLED") == "true"
}

func MediaWorkbenchConfig(c *gin.Context) {
	callback, _, enabled := mediaWorkbenchConfig()
	setAuthNoStore(c)
	start := ""
	if enabled {
		start = strings.TrimSuffix(callback, "/api/auth/resnewapi/callback") + "/login"
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"enabled": enabled, "start_url": start}})
}

func MediaWorkbenchAuthorize(c *gin.Context) {
	setAuthNoStore(c)
	callback, _, enabled := mediaWorkbenchConfig()
	if !enabled {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	var input struct {
		Challenge string `json:"challenge"`
		State     string `json:"state"`
	}
	if common.DecodeJson(c.Request.Body, &input) != nil || !mediaPKCEPattern.MatchString(input.Challenge) || !mediaPKCEPattern.MatchString(input.State) {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if _, _, err := service.ValidateLoginSession(identity); err != nil {
		writeAuthSessionError(c, err)
		return
	}
	payload, err := common.Marshal(mediaAuthorization{Identity: identity, Challenge: input.Challenge, Callback: callback, ExpiresAt: time.Now().Add(14 * 24 * time.Hour).Unix()})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	code, _, err := model.CreateAuthFlow(model.AuthFlowCreate{Purpose: mediaCodePurpose, UserId: identity.UserID, SessionId: identity.SessionID, Payload: string(payload), ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	u, _ := url.Parse(callback)
	q := u.Query()
	q.Set("code", code)
	q.Set("state", input.State)
	u.RawQuery = q.Encode()
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"redirect_url": u.String()}})
}

// Every server-to-server operation needs both the client secret and a user grant.
func mediaClient(c *gin.Context) (string, bool) {
	callback, secret, enabled := mediaWorkbenchConfig()
	setAuthNoStore(c)
	if !enabled {
		c.AbortWithStatus(http.StatusNotFound)
		return "", false
	}
	if !hmac.Equal([]byte(c.GetHeader("X-Media-Client-Secret")), []byte(secret)) {
		c.AbortWithStatus(http.StatusUnauthorized)
		return "", false
	}
	if origin := c.GetHeader("Origin"); origin != "" {
		u, err := url.Parse(origin)
		cb, callbackErr := url.Parse(callback)
		if err != nil || callbackErr != nil || u.Scheme != cb.Scheme || !strings.EqualFold(u.Host, cb.Host) {
			c.AbortWithStatus(http.StatusForbidden)
			return "", false
		}
	}
	return callback, true
}

func mediaGrantSignature(payload string, callback string) []byte {
	mac := hmac.New(sha256.New, []byte(mediaGrantSigningKey()))
	_, _ = mac.Write([]byte("media-workbench/v1\x00" + callback + "\x00" + os.Getenv("MEDIA_WORKBENCH_CLIENT_SECRET") + "\x00" + payload))
	return mac.Sum(nil)
}

func mediaGrantSigningKey() string {
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	_, _ = mac.Write([]byte("new-api/media-workbench/signing/v1/" + os.Getenv("MEDIA_WORKBENCH_CLIENT_SECRET")))
	return hex.EncodeToString(mac.Sum(nil))
}

func MediaWorkbenchExchange(c *gin.Context) {
	callback, ok := mediaClient(c)
	if !ok {
		return
	}
	var input struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
	}
	if common.DecodeJson(c.Request.Body, &input) != nil || !mediaPKCEPattern.MatchString(input.Verifier) {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	var authorization mediaAuthorization
	_, err := model.ConsumeAuthFlowWithAction(input.Code, model.AuthFlowMatch{Purpose: mediaCodePurpose}, func(tx *gorm.DB, flow *model.AuthFlow) error {
		if err := common.UnmarshalJsonStr(flow.Payload, &authorization); err != nil {
			return err
		}
		challenge := sha256.Sum256([]byte(input.Verifier))
		if authorization.Callback != callback || !hmac.Equal([]byte(authorization.Challenge), []byte(base64.RawURLEncoding.EncodeToString(challenge[:]))) {
			return model.ErrAuthFlowInvalid
		}
		return model.ValidateAuthSessionWithTx(tx, authorization.Identity)
	})
	if err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	payload, err := common.Marshal(authorization)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	grant := encoded + "." + base64.RawURLEncoding.EncodeToString(mediaGrantSignature(encoded, callback))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"grant": grant, "user_id": authorization.Identity.UserID, "expires_at": authorization.ExpiresAt}})
}

func mediaGrantIdentity(c *gin.Context) (service.AuthIdentity, bool) {
	callback, ok := mediaClient(c)
	if !ok {
		return service.AuthIdentity{}, false
	}
	raw := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	payload, signature, found := strings.Cut(raw, ".")
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if !found || err != nil || !hmac.Equal(sig, mediaGrantSignature(payload, callback)) {
		c.AbortWithStatus(http.StatusUnauthorized)
		return service.AuthIdentity{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	var authorization mediaAuthorization
	if err != nil || common.Unmarshal(decoded, &authorization) != nil || authorization.Callback != callback || authorization.ExpiresAt <= time.Now().Unix() {
		c.AbortWithStatus(http.StatusUnauthorized)
		return service.AuthIdentity{}, false
	}
	if _, _, err := service.ValidateLoginSession(authorization.Identity); err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return service.AuthIdentity{}, false
	}
	return authorization.Identity, true
}

func MediaWorkbenchContext(c *gin.Context) {
	identity, ok := mediaGrantIdentity(c)
	if !ok {
		return
	}
	user, err := model.GetSelfUserById(identity.UserID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	tokens, err := model.GetAllUserTokens(identity.UserID, 0, -1)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	keys := make([]gin.H, 0, len(tokens))
	for _, token := range tokens {
		status := token.Status
		if status == common.TokenStatusEnabled && token.ExpiredTime != -1 && token.ExpiredTime <= time.Now().Unix() {
			status = 3
		}
		keys = append(keys, gin.H{"id": token.Id, "name": token.Name, "group": token.Group, "status": status, "expired_time": token.ExpiredTime, "model_limits_enabled": token.ModelLimitsEnabled, "model_limits": token.ModelLimits})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"user": gin.H{"id": user.Id, "username": user.Username, "email": user.Email, "role": user.Role}, "keys": keys}})
}

func mediaWorkbenchToken(c *gin.Context) *model.Token {
	identity, ok := mediaGrantIdentity(c)
	if !ok {
		return nil
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return nil
	}
	token, err := model.GetTokenByIds(id, identity.UserID)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return nil
	}
	if token.Status != common.TokenStatusEnabled || (token.ExpiredTime != -1 && token.ExpiredTime <= time.Now().Unix()) {
		c.AbortWithStatus(http.StatusForbidden)
		return nil
	}
	return token
}

func MediaWorkbenchModels(c *gin.Context) {
	token := mediaWorkbenchToken(c)
	if token == nil {
		return
	}
	c.Set("id", token.UserId)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, token.Group)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, token.ModelLimitsEnabled)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimit, token.GetModelLimitsMap())
	if token.AutoGroups != "" {
		groups, err := token.GetAutoGroups()
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, groups)
	}
	ListModels(c, constant.ChannelTypeOpenAI)
}

func MediaWorkbenchKey(c *gin.Context) {
	token := mediaWorkbenchToken(c)
	if token == nil {
		return
	}
	key := token.GetFullKey()
	if !strings.HasPrefix(key, "sk-") {
		key = "sk-" + key
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"key": key}})
}
