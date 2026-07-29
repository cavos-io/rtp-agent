package slng

import (
	"fmt"
	"net"
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
		return "", fmt.Errorf("invalid bridge endpoint %q", endpoint)
	}
	prefix := "/v1/bridges/unmute/" + service + "/"
	if !strings.HasPrefix(parsed.Path, prefix) {
		return "", fmt.Errorf("invalid bridge endpoint %q", endpoint)
	}
	model := strings.TrimPrefix(parsed.Path, prefix)
	if _, err := parseModelRef(model); err != nil {
		return "", err
	}
	return model, nil
}

func bridgeBaseURL(baseURL string) (*url.URL, error) {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return nil, fmt.Errorf("bridge base URL must not be empty")
	}
	if !strings.Contains(raw, "://") {
		scheme := "wss"
		host, _, err := net.SplitHostPort(raw)
		if err != nil {
			host = strings.Trim(raw, "[]")
		}
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			scheme = "ws"
		}
		raw = scheme + "://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" {
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
	cooldown time.Duration
	retryAt  []time.Time
}

func newCandidateState(count int, cooldown time.Duration) *candidateState {
	return &candidateState{
		cooldown: cooldown,
		retryAt:  make([]time.Time, count),
	}
}

func (s *candidateState) start(now time.Time) int {
	for index, retryAt := range s.retryAt {
		if !now.Before(retryAt) {
			return index
		}
	}
	return -1
}

func (s *candidateState) advance(index int, now time.Time) (int, bool) {
	if index < 0 || index >= len(s.retryAt) {
		return -1, false
	}
	s.retryAt[index] = now.Add(s.cooldown)
	for offset := 1; offset < len(s.retryAt); offset++ {
		next := (index + offset) % len(s.retryAt)
		if !now.Before(s.retryAt[next]) {
			return next, true
		}
	}
	return -1, false
}

func (s *candidateState) selectCandidate(index int) {
	if index >= 0 && index < len(s.retryAt) {
		s.retryAt[index] = time.Time{}
	}
}
