package common

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Upstream privacy (UPSTREAM_PRIVACY_ENABLED) keeps API callers from learning
// which provider served a request. Administrators still see the original
// upstream details in system and admin logs.

const upstreamServiceBusyMessage = "服务暂时繁忙，请稍后重试 / Service temporarily unavailable, please retry later"

var (
	upstreamRequestIdPattern = regexp.MustCompile(`(?i)\s*[(（]?\s*request[ _-]?id\s*[:：=]\s*[\w-]+\s*[)）]?`)
	statusCodeLogPattern     = regexp.MustCompile(`(?s)^status_code=(\d+), (.*)$`)
)

// PrivateUpstreamFailure maps an upstream failure to the status code and
// message shown to API callers. Client-correctable 4xx errors keep their text,
// minus relay request ids such as another gateway's "(request id: ...)"
// suffix, so callers can fix the request; upstream credential, balance and
// availability problems become a generic message.
func PrivateUpstreamFailure(statusCode int, message string) (int, string) {
	switch statusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		if scrubbed := strings.TrimSpace(upstreamRequestIdPattern.ReplaceAllString(message, "")); scrubbed != "" {
			return statusCode, scrubbed
		}
		return statusCode, "请求无效，请检查参数后重试 / Invalid request, please check the parameters"
	case http.StatusTooManyRequests:
		return statusCode, "请求过于频繁，请稍后重试 / Too many requests, please retry later"
	case http.StatusForbidden:
		return statusCode, "请求被拒绝，可能触发了内容安全策略 / Request rejected, possibly by content policy"
	case http.StatusUnauthorized, http.StatusPaymentRequired:
		return http.StatusServiceUnavailable, upstreamServiceBusyMessage
	}
	if statusCode < http.StatusInternalServerError {
		statusCode = http.StatusBadGateway
	}
	return statusCode, upstreamServiceBusyMessage
}

// PrivateUpstreamLogContent applies PrivateUpstreamFailure to an error log
// entry stored as "status_code=<code>, <message>".
func PrivateUpstreamLogContent(content string) string {
	statusCode := 0
	message := content
	if match := statusCodeLogPattern.FindStringSubmatch(content); match != nil {
		statusCode, _ = strconv.Atoi(match[1])
		message = match[2]
	}
	statusCode, message = PrivateUpstreamFailure(statusCode, message)
	return fmt.Sprintf("status_code=%d, %s", statusCode, message)
}
