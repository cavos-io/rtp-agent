package livekit

import (
	"net/url"
	"strings"
)

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
