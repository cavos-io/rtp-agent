package inferencecontext

import (
	"net/http"
	"sync"
)

const (
	HeaderRoomID            = "X-LiveKit-Room-ID"
	HeaderJobID             = "X-LiveKit-Job-ID"
	HeaderInferenceProvider = "X-LiveKit-Inference-Provider"
	HeaderInferencePriority = "X-LiveKit-Inference-Priority"
)

var headersProvider = struct {
	mu       sync.RWMutex
	provider func() map[string]string
}{}

func SetHeadersProvider(provider func() map[string]string) func() {
	headersProvider.mu.Lock()
	previous := headersProvider.provider
	headersProvider.provider = provider
	headersProvider.mu.Unlock()
	return func() {
		headersProvider.mu.Lock()
		headersProvider.provider = previous
		headersProvider.mu.Unlock()
	}
}

func AddHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	headersProvider.mu.RLock()
	provider := headersProvider.provider
	headersProvider.mu.RUnlock()
	if provider == nil {
		return
	}
	for key, value := range provider() {
		if key != "" && value != "" {
			headers.Set(key, value)
		}
	}
}
