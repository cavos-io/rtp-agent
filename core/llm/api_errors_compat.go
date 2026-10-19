package llm

import "github.com/cavos-io/rtp-agent/core/inference"

// APIError is retained for source compatibility.
// Deprecated: use inference.APIError.
type APIError = inference.APIError

// APIStatusError is retained for source compatibility.
// Deprecated: use inference.APIStatusError.
type APIStatusError = inference.APIStatusError

// APIConnectionError is retained for source compatibility.
// Deprecated: use inference.APIConnectionError.
type APIConnectionError = inference.APIConnectionError

// APITimeoutError is retained for source compatibility.
// Deprecated: use inference.APITimeoutError.
type APITimeoutError = inference.APITimeoutError

// NewAPIError is retained for source compatibility.
// Deprecated: use inference.NewAPIError.
func NewAPIError(message string, body any, retryable bool) *APIError {
	return inference.NewAPIError(message, body, retryable)
}

// NewAPIStatusError is retained for source compatibility.
// Deprecated: use inference.NewAPIStatusError.
func NewAPIStatusError(message string, statusCode int, requestID string, body any) *APIStatusError {
	return inference.NewAPIStatusError(message, statusCode, requestID, body)
}

// NewAPIStatusErrorWithRetryable is retained for source compatibility.
// Deprecated: use inference.NewAPIStatusErrorWithRetryable.
func NewAPIStatusErrorWithRetryable(message string, statusCode int, requestID string, body any, retryable bool) *APIStatusError {
	return inference.NewAPIStatusErrorWithRetryable(message, statusCode, requestID, body, retryable)
}

// CreateAPIErrorFromHTTP is retained for source compatibility.
// Deprecated: use inference.CreateAPIErrorFromHTTP.
func CreateAPIErrorFromHTTP(message string, statusCode int, requestID string, body any) *APIStatusError {
	return inference.CreateAPIErrorFromHTTP(message, statusCode, requestID, body)
}

// NewAPIConnectionError is retained for source compatibility.
// Deprecated: use inference.NewAPIConnectionError.
func NewAPIConnectionError(message string) *APIConnectionError {
	return inference.NewAPIConnectionError(message)
}

// NewAPIConnectionErrorWithRetryable is retained for source compatibility.
// Deprecated: use inference.NewAPIConnectionErrorWithRetryable.
func NewAPIConnectionErrorWithRetryable(message string, retryable bool) *APIConnectionError {
	return inference.NewAPIConnectionErrorWithRetryable(message, retryable)
}

// NewAPITimeoutError is retained for source compatibility.
// Deprecated: use inference.NewAPITimeoutError.
func NewAPITimeoutError(message string) *APITimeoutError {
	return inference.NewAPITimeoutError(message)
}

// NewAPITimeoutErrorWithRetryable is retained for source compatibility.
// Deprecated: use inference.NewAPITimeoutErrorWithRetryable.
func NewAPITimeoutErrorWithRetryable(message string, retryable bool) *APITimeoutError {
	return inference.NewAPITimeoutErrorWithRetryable(message, retryable)
}
