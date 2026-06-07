package did

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName                 = "d-id"
	defaultDIDAPIURL             = "https://api.d-id.com"
	defaultAvatarAgentIdentity   = "d-id-avatar-agent"
	defaultAvatarAgentName       = "d-id-avatar-agent"
	defaultAudioSampleRate       = 24000
	defaultInitialDIDAvatarState = agent.AvatarStateIdle
	didAPIKeyEnv                 = "DID_API_KEY"
	didAPIURLEnv                 = "DID_API_URL"
	didAgentIDEnv                = "DID_AGENT_ID"
)

type AudioConfig struct {
	SampleRate int
}

type DIDAvatar struct {
	agentID        string
	apiKey         string
	apiURL         string
	audioConfig    AudioConfig
	avatarIdentity string
	avatarName     string
	sessionID      string
	state          agent.AvatarState
	started        bool
}

func NewDIDAvatar(agentID, apiKey string) *DIDAvatar {
	if agentID == "" {
		agentID = os.Getenv(didAgentIDEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv(didAPIKeyEnv)
	}
	apiURL := os.Getenv(didAPIURLEnv)
	if apiURL == "" {
		apiURL = defaultDIDAPIURL
	}
	return &DIDAvatar{
		agentID:        agentID,
		apiKey:         apiKey,
		apiURL:         apiURL,
		audioConfig:    AudioConfig{SampleRate: defaultAudioSampleRate},
		avatarIdentity: defaultAvatarAgentIdentity,
		avatarName:     defaultAvatarAgentName,
		state:          defaultInitialDIDAvatarState,
	}
}

func (a *DIDAvatar) Start(ctx context.Context) error {
	if a.apiKey == "" {
		return errors.New("DID_API_KEY must be set by arguments or environment variables")
	}
	if a.agentID == "" {
		return errors.New("DID_AGENT_ID must be set by arguments or environment variables")
	}
	if info, ok := agent.AvatarStartInfoFromContext(ctx); ok && info.LiveKitURL != "" && info.LiveKitToken != "" {
		if err := a.joinSession(ctx, info); err != nil {
			return err
		}
	}
	a.started = true
	return nil
}

func (a *DIDAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *DIDAvatar) Provider() string {
	return providerName
}

func (a *DIDAvatar) AvatarIdentity() string {
	return a.avatarIdentity
}

func (a *DIDAvatar) joinSession(ctx context.Context, info agent.AvatarStartInfo) error {
	endpoint, headers, body, err := buildDIDJoinSessionRequest(a, info)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.apiURL, "/")+endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("D-ID join session failed: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return err
	}
	a.sessionID = payload.ID
	return nil
}

func buildDIDJoinSessionRequest(avatar *DIDAvatar, info agent.AvatarStartInfo) (string, map[string]string, []byte, error) {
	payload := map[string]any{
		"transport": map[string]any{
			"provider":   "livekit",
			"server_url": info.LiveKitURL,
			"token":      info.LiveKitToken,
			"room_name":  info.RoomName,
		},
		"audio_config": map[string]any{
			"sample_rate": avatar.audioConfig.SampleRate,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, nil, err
	}
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Basic " + avatar.apiKey,
	}
	return "/v2/agents/" + avatar.agentID + "/sessions/join", headers, body, nil
}
