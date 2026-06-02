package anam

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName                  = "anam"
	defaultAnamAPIURL             = "https://api.anam.ai"
	defaultAvatarAgentIdentity    = "anam-avatar-agent"
	defaultAvatarAgentName        = "anam-avatar-agent"
	defaultInitialAnamAvatarState = agent.AvatarStateIdle

	anamAPIKeyEnv = "ANAM_API_KEY"
	anamAPIURLEnv = "ANAM_API_URL"
)

type PersonaConfig struct {
	Name        string
	AvatarID    string
	AvatarModel string
}

type AnamAvatar struct {
	apiKey         string
	apiURL         string
	personaConfig  PersonaConfig
	avatarIdentity string
	avatarName     string
	state          agent.AvatarState
	started        bool
}

func NewAnamAvatar(apiKey string, personaConfig ...PersonaConfig) *AnamAvatar {
	if apiKey == "" {
		apiKey = os.Getenv(anamAPIKeyEnv)
	}
	apiURL := os.Getenv(anamAPIURLEnv)
	if apiURL == "" {
		apiURL = defaultAnamAPIURL
	}
	var persona PersonaConfig
	if len(personaConfig) > 0 {
		persona = personaConfig[0]
	}
	return &AnamAvatar{
		apiKey:         apiKey,
		apiURL:         apiURL,
		personaConfig:  persona,
		avatarIdentity: defaultAvatarAgentIdentity,
		avatarName:     defaultAvatarAgentName,
		state:          defaultInitialAnamAvatarState,
	}
}

func (a *AnamAvatar) Start(ctx context.Context) error {
	if a.apiKey == "" {
		return errors.New("ANAM_API_KEY must be set by arguments or environment variables")
	}
	a.started = true
	return nil
}

func (a *AnamAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *AnamAvatar) Provider() string {
	return providerName
}

func (a *AnamAvatar) AvatarIdentity() string {
	return a.avatarIdentity
}

func buildAnamSessionTokenRequest(apiKey string, personaConfig PersonaConfig, livekitURL, livekitToken string) (string, map[string]string, []byte, error) {
	payload := map[string]any{
		"personaConfig": map[string]any{
			"type":     "ephemeral",
			"name":     personaConfig.Name,
			"avatarId": personaConfig.AvatarID,
			"llmId":    "CUSTOMER_CLIENT_V1",
		},
		"environment": map[string]any{
			"livekitUrl":   livekitURL,
			"livekitToken": livekitToken,
		},
	}
	if personaConfig.AvatarModel != "" {
		payload["personaConfig"].(map[string]any)["avatarModel"] = personaConfig.AvatarModel
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, nil, err
	}

	headers := map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Content-Type":  "application/json",
	}
	return "/v1/auth/session-token", headers, body, nil
}
