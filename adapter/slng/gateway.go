package slng

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

const slngErrorMessageMaxLen = 500

var slngBridgeErrorStatus = map[string]int{
	"auth_error":                       http.StatusUnauthorized,
	"config_error":                     http.StatusBadRequest,
	"invalid_request":                  http.StatusBadRequest,
	"payload_too_large":                http.StatusRequestEntityTooLarge,
	"rate_limit":                       http.StatusTooManyRequests,
	"rate_limit_exceeded":              http.StatusTooManyRequests,
	"idle_timeout_exceeded":            http.StatusRequestTimeout,
	"max_connection_duration_exceeded": http.StatusRequestTimeout,
	"not_ready":                        http.StatusServiceUnavailable,
	"backpressure":                     http.StatusServiceUnavailable,
	"stt_metering_unavailable":         http.StatusServiceUnavailable,
	"translation_error":                http.StatusInternalServerError,
	"provider_error":                   http.StatusBadGateway,
	"backend_error":                    http.StatusBadGateway,
	"backend_connection_failed":        http.StatusBadGateway,
}

type gatewayHeaders struct {
	APIKey            string
	ProviderAPIKey    string
	RegionOverride    string
	WorldPartOverride string
	ExternalAgentID   string
	ExternalSessionID string
	Extra             http.Header
}

func (h gatewayHeaders) build(candidate http.Header) (http.Header, error) {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+h.APIKey)
	headers.Set("X-API-Key", h.APIKey)
	mergeSLNGHeaders(headers, h.Extra)

	if region := normalizeRegionOverride(h.RegionOverride); region != "" && headers.Get("X-Region-Override") == "" {
		headers.Set("X-Region-Override", region)
	}
	if worldPart := strings.ToLower(strings.TrimSpace(h.WorldPartOverride)); worldPart != "" && headers.Get("X-World-Part-Override") == "" {
		headers.Set("X-World-Part-Override", worldPart)
	}
	if h.ProviderAPIKey != "" {
		providerAPIKey := strings.TrimSpace(h.ProviderAPIKey)
		if providerAPIKey == "" {
			return nil, fmt.Errorf("provider API key must not be empty")
		}
		headers.Set("X-Slng-Provider-Key", providerAPIKey)
	}
	if agentID, err := validateSLNGTrackingID(h.ExternalAgentID, "external agent ID"); err != nil {
		return nil, err
	} else if agentID != "" {
		headers.Set("X-SLNG-Agent-Id", agentID)
	}
	if sessionID, err := validateSLNGTrackingID(h.ExternalSessionID, "external session ID"); err != nil {
		return nil, err
	} else if sessionID != "" {
		headers.Set("X-SLNG-Session-Id", sessionID)
	}
	mergeSLNGHeaders(headers, candidate)
	return headers, nil
}

func mergeSLNGHeaders(dst, src http.Header) {
	for key, values := range src.Clone() {
		dst[http.CanonicalHeaderKey(key)] = values
	}
}

func validateSLNGTrackingID(value, name string) (string, error) {
	if value == "" {
		return "", nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	if len(value) > 128 {
		return "", fmt.Errorf("%s must be 128 characters or fewer", name)
	}
	for i := 0; i < len(value); i++ {
		if value[i] == ',' || value[i] <= 0x1f || value[i] == 0x7f {
			return "", fmt.Errorf("%s must not contain commas or control characters", name)
		}
	}
	return value, nil
}

func extractSLNGErrorStatus(message map[string]any) int {
	for _, frame := range []map[string]any{slngMap(message["data"]), message} {
		for _, key := range []string{"status_code", "code", "err_code"} {
			if status, ok := slngErrorStatus(frame[key]); ok {
				return status
			}
		}
	}
	return -1
}

func slngErrorStatus(value any) (int, bool) {
	switch value := value.(type) {
	case int:
		return value, true
	case float64:
		if value == float64(int(value)) {
			return int(value), true
		}
	case string:
		value = strings.TrimSpace(value)
		if status, err := strconv.Atoi(value); err == nil {
			return status, true
		}
		if status, ok := slngBridgeErrorStatus[strings.ToLower(value)]; ok {
			return status, true
		}
	}
	return 0, false
}

func slngStatusError(message map[string]any) *llm.APIStatusError {
	return llm.NewAPIStatusError(slngGatewayErrorMessage(message), extractSLNGErrorStatus(message), "", message)
}

func slngGatewayErrorMessage(message map[string]any) string {
	for _, frame := range []map[string]any{slngMap(message["data"]), message} {
		if text := extractSLNGError(frame); text != "Unknown error" {
			return text[:min(len(text), slngErrorMessageMaxLen)]
		}
	}
	return "Unknown error"
}

func isSLNGFallbackEligible(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		return true
	}
	status := statusErr.StatusCode
	return status < 400 || status >= 500 ||
		status == http.StatusRequestTimeout ||
		status == http.StatusConflict ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests
}
