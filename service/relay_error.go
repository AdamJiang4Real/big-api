package service

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// localClientErrorCodes are produced by the gateway itself and tell the caller
// how to fix the request, so upstream privacy returns them unchanged.
var localClientErrorCodes = []types.ErrorCode{
	types.ErrorCodeInvalidRequest,
	types.ErrorCodeSensitiveWordsDetected,
	types.ErrorCodeViolationFeeGrokCSAM,
	types.ErrorCodeCountTokenFailed,
	types.ErrorCodeModelPriceError,
	types.ErrorCodeGetChannelFailed,
	types.ErrorCodeReadRequestBodyFailed,
	types.ErrorCodeConvertRequestFailed,
	types.ErrorCodeAccessDenied,
	types.ErrorCodeBadRequestBody,
	types.ErrorCodeInsufficientUserQuota,
	types.ErrorCodePreConsumeTokenQuotaFailed,
}

// PrivatizeUpstreamError returns the error shown to the API caller. With
// upstream privacy enabled, upstream failures are replaced by a gateway error
// that carries no upstream message, type, code or request id; the original
// error must be logged before calling this.
func PrivatizeUpstreamError(err *types.NewAPIError) *types.NewAPIError {
	if !constant.UpstreamPrivacyEnabled || err == nil || slices.Contains(localClientErrorCodes, err.GetErrorCode()) {
		return err
	}
	statusCode, message := common.PrivateUpstreamFailure(err.StatusCode, err.ToOpenAIError().Message)
	code := types.ErrorCode("service_unavailable")
	switch {
	case statusCode == http.StatusTooManyRequests:
		code = "rate_limit_exceeded"
	case statusCode == http.StatusForbidden:
		code = "request_rejected"
	case statusCode < http.StatusInternalServerError:
		code = types.ErrorCodeInvalidRequest
	}
	return types.NewErrorWithStatusCode(errors.New(message), code, statusCode, types.ErrOptionWithSkipRetry())
}

// DecideRelayRetry is the single retry decision for relay attempts. The reason
// is recorded in the request policy decision events of the log details.
func DecideRelayRetry(c *gin.Context, err *types.NewAPIError, retryTimes int) PolicyDecision {
	if err == nil {
		return PolicyDecision{Action: "stop", Reason: "request_completed", Source: "system"}
	}
	if ShouldSkipRetryAfterChannelAffinityFailure(c) {
		source := RequestPolicy(c).SessionModeSource
		if source == "" {
			source = "session_rule"
		}
		return PolicyDecision{Action: "stop", Reason: "strict_session", Source: source}
	}
	if GetChannelConstraints(c).SuppressesRetry() {
		return PolicyDecision{Action: "stop", Reason: "pinned_channel", Source: "channel_constraint"}
	}
	if types.IsChannelError(err) {
		return PolicyDecision{Action: "retry", Reason: "channel_error", Source: "system"}
	}
	if types.IsSkipRetryError(err) {
		return PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}
	}
	if retryTimes <= 0 {
		return PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted", Source: "global"}
	}
	code := err.StatusCode
	if code >= 200 && code < 300 {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if code < 100 || code > 599 {
		return PolicyDecision{Action: "retry", Reason: "unrecognized_status", Source: "system"}
	}
	if operation_setting.IsAlwaysSkipRetryCode(err.GetErrorCode()) || operation_setting.IsAlwaysSkipRetryStatusCode(code) {
		return PolicyDecision{Action: "stop", Reason: "system_retry_exclusion", Source: "system"}
	}
	if operation_setting.ShouldRetryByStatusCode(code) {
		return PolicyDecision{Action: "retry", Reason: "retry_status_matched", Source: "global"}
	}
	return PolicyDecision{Action: "stop", Reason: "status_not_retryable", Source: "global"}
}

func ShouldRetryRelayError(c *gin.Context, openaiErr *types.NewAPIError, retryTimes int) bool {
	return DecideRelayRetry(c, openaiErr, retryTimes).Action == "retry"
}

func ProcessChannelError(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError, relayInfo *relaycommon.RelayInfo) {
	if err == nil {
		return
	}
	logger.LogError(c, fmt.Sprintf("channel error (channel #%d, status code: %d): %s", channelError.ChannelId, err.StatusCode, common.LocalLogPreview(err.MaskSensitiveErrorWithStatusCode())))
	if ShouldDisableChannel(err) && channelError.AutoBan {
		reason := err.MaskSensitiveErrorWithStatusCode()
		gopool.Go(func() {
			DisableChannel(channelError, reason)
		})
	}

	if constant.ErrorLogEnabled && types.IsRecordErrorLog(err) {
		userId := c.GetInt("id")
		tokenName := c.GetString("token_name")
		modelName := c.GetString("original_model")
		tokenId := c.GetInt("token_id")
		userGroup := c.GetString("group")
		other := model.NewLogOther()
		if c.Request != nil && c.Request.URL != nil {
			other.SetPublic("request_path", c.Request.URL.Path)
		}
		other.SetPublic("error_type", err.GetErrorType())
		other.SetPublic("error_code", err.GetErrorCode())
		other.SetPublic("status_code", err.StatusCode)
		AppendRelayLogAdminInfo(c, relayInfo, other)
		AppendResponseModelLogInfo(relayInfo, other)
		AppendTaskPluginContextAuditInfo(c, other)
		startTime := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if startTime.IsZero() {
			startTime = time.Now()
		}
		useTimeSeconds := int(time.Since(startTime).Seconds())
		model.RecordErrorLog(c, userId, channelError.ChannelId, modelName, tokenName, err.MaskSensitiveErrorWithStatusCode(), tokenId, useTimeSeconds, common.GetContextKeyBool(c, constant.ContextKeyIsStream), userGroup, other)
	}
}
