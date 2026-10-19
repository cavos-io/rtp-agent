package inference

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type APIError struct {
	Message   string
	Body      any
	Retryable bool
}

func NewAPIError(message string, body any, retryable bool) *APIError {
	return &APIError{
		Message:   message,
		Body:      body,
		Retryable: retryable,
	}
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type APIStatusError struct {
	*APIError
	StatusCode int
	RequestID  string
}

func NewAPIStatusError(message string, statusCode int, requestID string, body any) *APIStatusError {
	return NewAPIStatusErrorWithRetryable(message, statusCode, requestID, body, apiStatusDefaultRetryable(statusCode))
}

func NewAPIStatusErrorWithRetryable(message string, statusCode int, requestID string, body any, retryable bool) *APIStatusError {
	retryable = applyAPIStatusRetryableOverride(statusCode, retryable)
	return &APIStatusError{
		APIError:   NewAPIError(message, body, retryable),
		StatusCode: statusCode,
		RequestID:  requestID,
	}
}

func CreateAPIErrorFromHTTP(message string, statusCode int, requestID string, body any) *APIStatusError {
	reason := http.StatusText(statusCode)
	if reason == "" {
		reason = fmt.Sprintf("HTTP %d", statusCode)
	}
	display := fmt.Sprintf("%s (%d)", reason, statusCode)
	if message != "" && message != reason {
		display = fmt.Sprintf("%s (%d %s)", message, statusCode, reason)
	}
	return NewAPIStatusError(display, statusCode, requestID, body)
}

func (e *APIStatusError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.APIError
}

func (e *APIStatusError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{
		fmt.Sprintf("message=%s", pyRepr(e.Message)),
		fmt.Sprintf("status_code=%d", e.StatusCode),
		fmt.Sprintf("retryable=%s", pyBool(e.Retryable)),
	}
	if e.RequestID != "" {
		parts = append(parts, fmt.Sprintf("request_id=%s", e.RequestID))
	}
	if pyTruthy(e.Body) {
		parts = append(parts, fmt.Sprintf("body=%s", pyStr(e.Body)))
	}
	return strings.Join(parts, ", ")
}

func apiStatusDefaultRetryable(statusCode int) bool {
	return applyAPIStatusRetryableOverride(statusCode, true)
}

func applyAPIStatusRetryableOverride(statusCode int, retryable bool) bool {
	if statusCode >= 400 && statusCode < 500 && statusCode != 408 && statusCode != 429 && statusCode != 499 {
		return false
	}
	return retryable
}

func pyBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

func pyTruthy(value any) bool {
	if value == nil {
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.String, reflect.Array, reflect.Chan, reflect.Map, reflect.Slice:
		return v.Len() > 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return v.Float() != 0
	default:
		return true
	}
}

func pyStr(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return pyRepr(value)
	}
}

func pyRepr(value any) string {
	switch v := value.(type) {
	case string:
		return pyStringRepr(v)
	case bool:
		return pyBool(v)
	case float32:
		return pyFloatRepr(float64(v), 32)
	case float64:
		return pyFloatRepr(v, 64)
	case nil:
		return "None"
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, fmt.Sprintf("%s: %s", pyRepr(key), pyRepr(v[key])))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			parts := make([]string, 0, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				parts = append(parts, pyRepr(rv.Index(i).Interface()))
			}
			return "[" + strings.Join(parts, ", ") + "]"
		case reflect.Map:
			if rv.Type().Key().Kind() == reflect.String {
				keys := make([]string, 0, rv.Len())
				iter := rv.MapRange()
				for iter.Next() {
					keys = append(keys, iter.Key().String())
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, key := range keys {
					parts = append(parts, fmt.Sprintf("%s: %s", pyRepr(key), pyRepr(rv.MapIndex(reflect.ValueOf(key)).Interface())))
				}
				return "{" + strings.Join(parts, ", ") + "}"
			}
		}
		return fmt.Sprintf("%v", value)
	}
}

func pyStringRepr(value string) string {
	if strings.Contains(value, "'") && !strings.Contains(value, `"`) {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(value) + `"`
	}
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(value) + "'"
}

func pyFloatRepr(value float64, bitSize int) string {
	formatted := strconv.FormatFloat(value, 'g', -1, bitSize)
	if !strings.ContainsAny(formatted, ".eE") {
		formatted += ".0"
	}
	return formatted
}

type APIConnectionError struct {
	*APIError
}

func NewAPIConnectionError(message string) *APIConnectionError {
	return NewAPIConnectionErrorWithRetryable(message, true)
}

func NewAPIConnectionErrorWithRetryable(message string, retryable bool) *APIConnectionError {
	if message == "" {
		message = "Connection error."
	}
	return &APIConnectionError{APIError: NewAPIError(message, nil, retryable)}
}

func (e *APIConnectionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.APIError
}

type APITimeoutError struct {
	*APIConnectionError
}

func NewAPITimeoutError(message string) *APITimeoutError {
	return NewAPITimeoutErrorWithRetryable(message, true)
}

func NewAPITimeoutErrorWithRetryable(message string, retryable bool) *APITimeoutError {
	if message == "" {
		message = "Request timed out."
	}
	return &APITimeoutError{APIConnectionError: NewAPIConnectionErrorWithRetryable(message, retryable)}
}

func (e *APITimeoutError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.APIConnectionError
}
