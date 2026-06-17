package livekit

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type WorkerConnectOptions struct {
	WSURL       string
	WorkerToken string
	APIKey      string
	APISecret   string
	TTL         time.Duration
}

type WorkerConnect struct {
	URL    string
	Header http.Header
}

func WorkerConnectInfo(opts WorkerConnectOptions) (WorkerConnect, error) {
	agentURL, err := AgentWebSocketURL(opts.WSURL, opts.WorkerToken)
	if err != nil {
		return WorkerConnect{}, err
	}
	token, err := WorkerAuthToken(opts.APIKey, opts.APISecret, opts.TTL)
	if err != nil {
		return WorkerConnect{}, err
	}
	return WorkerConnect{
		URL:    agentURL,
		Header: WorkerAuthHeader(token),
	}, nil
}

func WorkerWebSocketDialer(httpProxy string) (*websocket.Dialer, error) {
	dialer := *websocket.DefaultDialer
	if httpProxy == "" {
		return &dialer, nil
	}
	proxyURL, err := url.Parse(httpProxy)
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP proxy URL: %w", err)
	}
	dialer.Proxy = http.ProxyURL(proxyURL)
	return &dialer, nil
}

func AgentWebSocketURL(rawURL string, workerToken string) (string, error) {
	wsURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	if wsURL.Scheme == "http" {
		wsURL.Scheme = "ws"
	} else if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	}

	basePath := strings.TrimRight(wsURL.Path, "/")
	wsURL.Path = basePath + "/agent"
	if basePath == "" {
		wsURL.Path = "/agent"
	}

	values := url.Values{}
	if workerToken != "" {
		values.Set("worker_token", workerToken)
	}
	wsURL.RawQuery = values.Encode()

	return wsURL.String(), nil
}
