package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const inviteTestBotSecret = "invite-bot-secret-for-tests"

type inviteTestResponse struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Reason  string         `json:"reason"`
	Data    map[string]any `json:"data"`
}

func setupInviteCodeTest(t *testing.T, inviteRequired bool) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)

	originalMainType, originalLogType := common.MainDatabaseType(), common.LogDatabaseType()
	originalRedis := common.RedisEnabled
	originalRegister, originalPasswordRegister := common.RegisterEnabled, common.PasswordRegisterEnabled
	originalEmailVerification := common.EmailVerificationEnabled
	originalServerAddress := system_setting.ServerAddress
	settings := system_setting.GetInviteCodeSettings()
	originalSettings := *settings
	t.Cleanup(func() {
		common.SetDatabaseTypes(originalMainType, originalLogType)
		common.RedisEnabled = originalRedis
		common.RegisterEnabled, common.PasswordRegisterEnabled = originalRegister, originalPasswordRegister
		common.EmailVerificationEnabled = originalEmailVerification
		system_setting.ServerAddress = originalServerAddress
		*settings = originalSettings
	})

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.RegisterEnabled, common.PasswordRegisterEnabled = true, true
	common.EmailVerificationEnabled = false
	system_setting.ServerAddress = "https://bigapi.example"
	*settings = system_setting.InviteCodeSettings{RegisterEnabled: inviteRequired, ExpireMinutes: 30, BotSecret: inviteTestBotSecret}

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.InviteCode{}, &model.Log{}))
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func callInviteEndpoint(t *testing.T, handler gin.HandlerFunc, path string, authorization string, body map[string]any) (int, inviteTestResponse) {
	t.Helper()
	payload, err := common.Marshal(body)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		c.Request.Header.Set("Authorization", authorization)
	}
	handler(c)
	var response inviteTestResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder.Code, response
}

func registerWithInvite(t *testing.T, username string, inviteCode string) inviteTestResponse {
	t.Helper()
	body := map[string]any{"username": username, "password": "password-123"}
	if inviteCode != "" {
		body["invite_code"] = inviteCode
	}
	_, response := callInviteEndpoint(t, Register, "/api/user/register", "", body)
	return response
}

func userExists(t *testing.T, db *gorm.DB, username string) bool {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&model.User{}).Where("username = ?", username).Count(&count).Error)
	return count > 0
}

func TestRegisterWithInviteCodeRequired(t *testing.T) {
	db := setupInviteCodeTest(t, true)
	valid, err := model.IssueInviteCode("qq", "10001", "593143573", 30*time.Minute)
	require.NoError(t, err)
	lowercase, err := model.IssueInviteCode("qq", "10002", "593143573", 30*time.Minute)
	require.NoError(t, err)
	expired := &model.InviteCode{Code: "EXPIREDCODE1", Source: "qq", IssuedTo: "10003", CreatedAt: 1, ExpiresAt: time.Now().Add(-time.Minute).Unix()}
	require.NoError(t, db.Create(expired).Error)

	rejected := []struct {
		name       string
		username   string
		inviteCode string
	}{
		{name: "missing code", username: "no-code", inviteCode: ""},
		{name: "unknown code", username: "unknown-code", inviteCode: "NOTAREALCODE"},
		{name: "expired code", username: "expired-code", inviteCode: expired.Code},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			response := registerWithInvite(t, tt.username, tt.inviteCode)
			assert.False(t, response.Success)
			assert.False(t, userExists(t, db, tt.username), "rejected registration must not leave an account")
		})
	}

	response := registerWithInvite(t, "first-user", "  "+valid.Code+"  ")
	require.True(t, response.Success, response.Message)
	var created model.User
	require.NoError(t, db.Where("username = ?", "first-user").First(&created).Error)
	var consumed model.InviteCode
	require.NoError(t, db.Where("code = ?", valid.Code).First(&consumed).Error)
	assert.Positive(t, consumed.UsedAt)
	assert.Equal(t, created.Id, consumed.UsedUserId)

	replay := registerWithInvite(t, "second-user", valid.Code)
	assert.False(t, replay.Success, "a used invite code must not create a second account")
	assert.False(t, userExists(t, db, "second-user"))
	assert.Equal(t, registerWithInvite(t, "unknown-again", "NOTAREALCODE").Message, replay.Message,
		"used and unknown codes must get the same response")

	caseInsensitive := registerWithInvite(t, "lowercase-user", strings.ToLower(lowercase.Code))
	assert.True(t, caseInsensitive.Success, caseInsensitive.Message)
}

func TestRegisterWithoutInviteRequirementIgnoresInviteCodes(t *testing.T) {
	db := setupInviteCodeTest(t, false)

	response := registerWithInvite(t, "open-user", "")
	require.True(t, response.Success, response.Message)
	assert.True(t, userExists(t, db, "open-user"))
}

func TestOAuthCannotCreateUsersWhenInviteRequired(t *testing.T) {
	db := setupInviteCodeTest(t, true)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/oauth/test", nil)

	user, _, err := findOrCreateOAuthUser(c, &authFlowTestOAuthProvider{}, &oauth.OAuthUser{ProviderUserID: "external-user", Username: "oauth-newcomer"}, nil, "")

	assert.Nil(t, user)
	var disabled *OAuthRegistrationDisabledError
	assert.ErrorAs(t, err, &disabled)
	assert.False(t, userExists(t, db, "oauth-newcomer"))
}

func TestIssueInviteCodeByBot(t *testing.T) {
	db := setupInviteCodeTest(t, true)
	bearer := "Bearer " + inviteTestBotSecret
	issue := func(authorization string, qqUserId string) (int, inviteTestResponse) {
		return callInviteEndpoint(t, IssueInviteCodeByBot, "/api/invite/bot/issue", authorization,
			map[string]any{"qq_user_id": qqUserId, "qq_group_id": "593143573"})
	}

	status, _ := issue("", "20001")
	assert.Equal(t, http.StatusUnauthorized, status)
	status, _ = issue("Bearer wrong-secret", "20001")
	assert.Equal(t, http.StatusUnauthorized, status)
	status, _ = issue(bearer, "not-a-qq-number")
	assert.Equal(t, http.StatusBadRequest, status)

	status, first := issue(bearer, "20001")
	require.Equal(t, http.StatusOK, status)
	require.True(t, first.Success, first.Message)
	code, _ := first.Data["code"].(string)
	require.Len(t, code, 8)
	assert.Equal(t, strings.ToUpper(code), code)
	assert.Equal(t, "https://bigapi.example/sign-up?invite_code="+code, first.Data["invite_link"])

	_, again := issue(bearer, "20001")
	assert.Equal(t, code, again.Data["code"], "a pending code is reused for the same QQ user")
	_, other := issue(bearer, "20002")
	assert.NotEqual(t, code, other.Data["code"])

	require.NoError(t, db.Model(&model.InviteCode{}).Where("code = ?", code).Update("expires_at", time.Now().Add(-time.Minute).Unix()).Error)
	_, renewed := issue(bearer, "20001")
	renewedCode, _ := renewed.Data["code"].(string)
	assert.NotEqual(t, code, renewedCode, "an expired code is replaced")

	require.True(t, registerWithInvite(t, "bot-user", renewedCode).Success)
	_, afterRegistration := issue(bearer, "20001")
	assert.False(t, afterRegistration.Success)
	assert.Equal(t, "already_registered", afterRegistration.Reason)

	settings := system_setting.GetInviteCodeSettings()
	settings.BotSecret = ""
	status, _ = issue("Bearer ", "20003")
	assert.Equal(t, http.StatusUnauthorized, status, "an empty bot secret disables the endpoint")
}
