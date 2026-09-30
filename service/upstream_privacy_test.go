package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const busyMessage = "服务暂时繁忙，请稍后重试 / Service temporarily unavailable, please retry later"

func setUpstreamPrivacy(t *testing.T, enabled bool) {
	t.Helper()
	previous := constant.UpstreamPrivacyEnabled
	constant.UpstreamPrivacyEnabled = enabled
	t.Cleanup(func() { constant.UpstreamPrivacyEnabled = previous })
}

func TestShouldCopyUpstreamHeaderUnderUpstreamPrivacy(t *testing.T) {
	setUpstreamPrivacy(t, true)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	cases := map[string]bool{
		"Content-Type":          true,
		"content-disposition":   true,
		"Retry-After":           true,
		"x-codex-turn-state":    true,
		"X-Reasoning-Included":  true,
		"X-New-Api-Version":     false,
		"Server":                false,
		"Via":                   false,
		"X-Cloud-Trace-Context": false,
		"X-Request-Id":          false,
		"Content-Length":        false,
	}
	for name, want := range cases {
		assert.Equal(t, want, ShouldCopyUpstreamHeader(c, name, []string{"value"}), name)
	}

	assert.False(t, ShouldCopyUpstreamHeader(c, common.RequestIdKey, []string{"upstream-request-id"}))
	assert.Equal(t, "upstream-request-id", c.GetString(common.UpstreamRequestIdKey))
}

func TestShouldCopyUpstreamHeaderWithoutUpstreamPrivacy(t *testing.T) {
	setUpstreamPrivacy(t, false)

	assert.True(t, ShouldCopyUpstreamHeader(nil, "X-New-Api-Version", []string{"v1.0.0"}))
	assert.True(t, ShouldCopyUpstreamHeader(nil, "Server", []string{"cloudflare"}))
}

func TestPrivatizeUpstreamError(t *testing.T) {
	setUpstreamPrivacy(t, true)

	tests := []struct {
		name        string
		err         *types.NewAPIError
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "upstream credential rejection becomes service unavailable",
			err:         types.WithOpenAIError(types.OpenAIError{Message: "Invalid token (request id: 202609300318350541)", Type: "new_api_error"}, http.StatusUnauthorized),
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: busyMessage,
		},
		{
			name:        "upstream balance problem becomes service unavailable",
			err:         types.WithClaudeError(types.ClaudeError{Message: "insufficient balance", Type: "billing_error"}, http.StatusPaymentRequired),
			wantStatus:  http.StatusServiceUnavailable,
			wantMessage: busyMessage,
		},
		{
			name:        "upstream rate limit keeps status with generic text",
			err:         types.WithOpenAIError(types.OpenAIError{Message: "当前分组 codex-team 上游负载已饱和"}, http.StatusTooManyRequests),
			wantStatus:  http.StatusTooManyRequests,
			wantMessage: "请求过于频繁，请稍后重试 / Too many requests, please retry later",
		},
		{
			name:        "upstream bad request keeps text without relay request id",
			err:         types.WithOpenAIError(types.OpenAIError{Message: "maximum context length exceeded (request id: 20260930abcDEF)", Type: "invalid_request_error"}, http.StatusBadRequest),
			wantStatus:  http.StatusBadRequest,
			wantMessage: "maximum context length exceeded",
		},
		{
			name:        "upstream server error body is dropped",
			err:         types.NewOpenAIError(errors.New("bad response status code 502, body: <html>cloudflare</html>"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway),
			wantStatus:  http.StatusBadGateway,
			wantMessage: busyMessage,
		},
		{
			name:        "transport error hides the upstream address",
			err:         types.NewError(errors.New(`Post "https://api.upstream.example/v1/chat/completions": dial tcp: i/o timeout`), types.ErrorCodeDoRequestFailed),
			wantStatus:  http.StatusInternalServerError,
			wantMessage: busyMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PrivatizeUpstreamError(tt.err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantStatus, got.StatusCode)
			assert.Equal(t, tt.wantMessage, got.ToOpenAIError().Message)
			assert.Equal(t, tt.wantMessage, got.ToClaudeError().Message)
		})
	}
}

func TestPrivatizeUpstreamErrorKeepsLocalAndDisabledErrors(t *testing.T) {
	setUpstreamPrivacy(t, true)
	local := types.NewErrorWithStatusCode(errors.New("user quota is not enough"), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden)
	assert.Same(t, local, PrivatizeUpstreamError(local))
	assert.Nil(t, PrivatizeUpstreamError(nil))

	setUpstreamPrivacy(t, false)
	upstream := types.WithOpenAIError(types.OpenAIError{Message: "Invalid token"}, http.StatusUnauthorized)
	assert.Same(t, upstream, PrivatizeUpstreamError(upstream))
}
