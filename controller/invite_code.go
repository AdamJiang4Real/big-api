package controller

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

type issueInviteCodeRequest struct {
	QQUserId  string `json:"qq_user_id"`
	QQGroupId string `json:"qq_group_id"`
}

// IssueInviteCodeByBot lets the trusted QQ group bot obtain a one-time
// registration code for a QQ user. The bot authenticates with the configured
// bot secret; an empty secret disables the endpoint.
func IssueInviteCodeByBot(c *gin.Context) {
	settings := system_setting.GetInviteCodeSettings()
	secret := strings.TrimSpace(settings.BotSecret)
	provided, _ := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	if secret == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(provided)), []byte(secret)) != 1 {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("invite bot request rejected: bad credentials, client_ip=%s", c.ClientIP()))
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "unauthorized"})
		return
	}

	var req issueInviteCodeRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request body"})
		return
	}
	qqUserId := strings.TrimSpace(req.QQUserId)
	if qqUserId == "" || len(qqUserId) > 20 || strings.Trim(qqUserId, "0123456789") != "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "qq_user_id must be a QQ number"})
		return
	}

	invite, err := model.IssueInviteCode("qq", qqUserId, strings.TrimSpace(req.QQGroupId), settings.InviteCodeTTL())
	if errors.Is(err, model.ErrInviteRecipientRegistered) {
		c.JSON(http.StatusOK, gin.H{"success": false, "reason": "already_registered", "message": "该 QQ 号已经用邀请码注册过账号"})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"code":        invite.Code,
		"expires_at":  invite.ExpiresAt,
		"invite_link": strings.TrimRight(system_setting.ServerAddress, "/") + "/sign-up?invite_code=" + url.QueryEscape(invite.Code),
	})
}
