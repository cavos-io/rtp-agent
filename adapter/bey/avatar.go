package bey

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	defaultBeyAvatarID            = "b9be11b8-89fb-4227-8f86-4a881393cbdb"
	defaultBeyAPIURL              = "https://api.bey.dev"
	defaultBeyAvatarAgentIdentity = "bey-avatar-agent"
	defaultBeyAvatarAgentName     = "bey-avatar-agent"

	beyAPIKeyEnv = "BEY_API_KEY"
	beyAPIURLEnv = "BEY_API_URL"
)

type BeyAvatar struct {
	apiKey         string
	apiURL         string
	avatarID       string
	avatarIdentity string
	avatarName     string
	state          agent.AvatarState
	started        bool
}

func NewBeyAvatar(apiKey string) (*BeyAvatar, error) {
	if apiKey == "" {
		apiKey = os.Getenv(beyAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("BEY_API_KEY is required, either as argument or set BEY_API_KEY environment variable")
	}
	apiURL := os.Getenv(beyAPIURLEnv)
	if apiURL == "" {
		apiURL = defaultBeyAPIURL
	}
	return &BeyAvatar{
		apiKey:         apiKey,
		apiURL:         apiURL,
		avatarID:       defaultBeyAvatarID,
		avatarIdentity: defaultBeyAvatarAgentIdentity,
		avatarName:     defaultBeyAvatarAgentName,
		state:          agent.AvatarStateIdle,
	}, nil
}

func (a *BeyAvatar) Start(ctx context.Context) error {
	a.started = true
	return nil
}

func (a *BeyAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *BeyAvatar) Provider() string {
	return "bey"
}

func (a *BeyAvatar) AvatarIdentity() string {
	return a.avatarIdentity
}

func buildBeySessionRequest(a *BeyAvatar, livekitURL, livekitToken string) (string, http.Header, []byte, error) {
	body, err := json.Marshal(map[string]any{
		"avatar_id":     a.avatarID,
		"livekit_url":   livekitURL,
		"livekit_token": livekitToken,
	})
	if err != nil {
		return "", nil, nil, err
	}
	headers := make(http.Header)
	headers.Set("x-api-key", a.apiKey)
	return a.apiURL + "/v1/session", headers, body, nil
}
