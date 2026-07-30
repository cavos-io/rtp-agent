package slng

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type STTConnectionConfig struct {
	Endpoint string
	Model    string
	Headers  http.Header
	Init     map[string]any
}

type TTSConnectionConfig struct {
	Endpoint string
	Model    string
	Voice    string
	Headers  http.Header
	Init     map[string]any
}

type sttConnectionCandidate struct {
	endpoint       string
	model          string
	headers        http.Header
	init           map[string]any
	legacyEndpoint bool
	waitReady      bool
}

type ttsConnectionCandidate struct {
	endpoint string
	model    string
	voice    string
	headers  http.Header
	init     map[string]any
}

func cloneSTTConnectionConfigs(configs []STTConnectionConfig) []STTConnectionConfig {
	cloned := make([]STTConnectionConfig, len(configs))
	for i, config := range configs {
		cloned[i] = STTConnectionConfig{
			Endpoint: config.Endpoint,
			Model:    config.Model,
			Headers:  config.Headers.Clone(),
			Init:     cloneSLNGMap(config.Init),
		}
	}
	return cloned
}

func cloneTTSConnectionConfigs(configs []TTSConnectionConfig) []TTSConnectionConfig {
	cloned := make([]TTSConnectionConfig, len(configs))
	for i, config := range configs {
		cloned[i] = TTSConnectionConfig{
			Endpoint: config.Endpoint,
			Model:    config.Model,
			Voice:    config.Voice,
			Headers:  config.Headers.Clone(),
			Init:     cloneSLNGMap(config.Init),
		}
	}
	return cloned
}

func bridgeEndpoint(baseURL, service, model string) (string, error) {
	if !bridgeService(service) {
		return "", fmt.Errorf("unsupported bridge service %q", service)
	}
	if _, err := parseModelRef(model); err != nil {
		return "", err
	}

	endpoint, err := bridgeBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	endpoint.Path = "/v1/bridges/unmute/" + service + "/" + strings.TrimSpace(model)
	return endpoint.String(), nil
}

func bridgeModel(endpoint, service string) (string, error) {
	if !bridgeService(service) {
		return "", fmt.Errorf("unsupported bridge service %q", service)
	}

	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" {
		return "", bridgeModelEndpointError(service)
	}
	prefix := "/v1/bridges/unmute/" + service + "/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return "", bridgeModelEndpointError(service)
	}
	model := strings.TrimRight(strings.TrimPrefix(parsed.Path, prefix), "/")
	if _, err := parseModelRef(model); err != nil {
		return "", err
	}
	return model, nil
}

func bridgeModelEndpointError(service string) error {
	return fmt.Errorf("%s endpoint must target the Unmute Bridge path /v1/bridges/unmute/%s/", strings.ToUpper(service), service)
}

func bridgeBaseURL(baseURL string) (*url.URL, error) {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return nil, fmt.Errorf("bridge base URL must not be empty")
	}
	explicitScheme := strings.Contains(raw, "://")
	if !explicitScheme {
		raw = "wss://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid bridge base URL %q", baseURL)
	}
	switch parsed.Scheme {
	case "http", "https":
		parsed.Scheme = "wss"
		if host := parsed.Hostname(); host == "localhost" || host == "127.0.0.1" || host == "::1" {
			parsed.Scheme = "ws"
		}
	case "ws", "wss":
		if !explicitScheme {
			if host := parsed.Hostname(); host == "localhost" || host == "127.0.0.1" || host == "::1" {
				parsed.Scheme = "ws"
			}
		}
	default:
		return nil, fmt.Errorf("invalid bridge base URL %q", baseURL)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func bridgeService(service string) bool {
	return service == "stt" || service == "tts"
}

type candidateState struct {
	count           int
	active          int
	cooldown        time.Duration
	primaryFailedAt time.Time
}

func newCandidateState(count int, cooldown time.Duration) *candidateState {
	if count < 0 {
		count = 0
	}
	return &candidateState{
		count:    count,
		active:   0,
		cooldown: cooldown,
	}
}

func (s *candidateState) start(now time.Time) int {
	if s.count == 0 {
		return -1
	}
	if !s.primaryFailedAt.IsZero() && !now.Before(s.primaryFailedAt.Add(s.cooldown)) {
		s.active = 0
		s.primaryFailedAt = time.Time{}
		return 0
	}
	return s.active
}

func (s *candidateState) advance(index int, now time.Time) (int, bool) {
	if index < 0 || index >= s.count {
		return -1, false
	}
	if index == 0 {
		s.primaryFailedAt = now
	}
	next := index + 1
	if next >= s.count {
		return -1, false
	}
	s.active = next
	return next, true
}

func (s *candidateState) selectCandidate(index int) {
	if index >= 0 && index < s.count {
		s.active = index
	}
}
