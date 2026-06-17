package livekit

import (
	"net/http"
	"net/url"
	"strings"
	"time"
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
