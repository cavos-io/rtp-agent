package runway

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
	providerName                    = "runway"
	defaultRunwayAPIURL             = "https://api.dev.runwayml.com"
	runwayAPIVersion                = "2024-11-06"
	runwayModel                     = "gwm1_avatars"
	defaultRunwaySampleRate         = 16000
	defaultAvatarAgentIdentity      = "runway-avatar-agent"
	defaultAvatarAgentName          = "runway-avatar-agent"
	defaultInitialRunwayAvatarState = agent.AvatarStateIdle
	runwayAPISecretEnv              = "RUNWAYML_API_SECRET"
	runwayBaseURLEnv                = "RUNWAYML_BASE_URL"
)

type AvatarOption func(*Avatar)

type Avatar struct {
	apiKey            string
	apiURL            string
	avatar            map[string]string
	maxDuration       *int
	avatarIdentity    string
	avatarName        string
	realtimeSessionID string
	sampleRate        int
	state             agent.AvatarState
	started           bool
}

func NewAvatar(apiKey string, opts ...AvatarOption) (*Avatar, error) {
	if apiKey == "" {
		apiKey = os.Getenv(runwayAPISecretEnv)
	}
	apiURL := os.Getenv(runwayBaseURLEnv)
	if apiURL == "" {
		apiURL = defaultRunwayAPIURL
	}
	avatar := &Avatar{
		apiKey:         apiKey,
		apiURL:         apiURL,
		avatarIdentity: defaultAvatarAgentIdentity,
		avatarName:     defaultAvatarAgentName,
		sampleRate:     defaultRunwaySampleRate,
		state:          defaultInitialRunwayAvatarState,
	}
	for _, opt := range opts {
		opt(avatar)
	}
	if avatar.avatar != nil && avatar.avatar["duplicateSource"] == "true" {
		return nil, errors.New("provide avatar_id or preset_id, not both")
	}
	if avatar.avatar == nil {
		return nil, errors.New("either avatar_id or preset_id must be provided")
	}
	if avatar.apiKey == "" {
		return nil, errors.New("api_key must be set either by passing it to AvatarSession or by setting the RUNWAYML_API_SECRET environment variable")
	}
	return avatar, nil
}

func WithRunwayAvatarID(avatarID string) AvatarOption {
	return func(avatar *Avatar) {
		if avatarID == "" {
			return
		}
		if avatar.avatar != nil {
			avatar.avatar["duplicateSource"] = "true"
			return
		}
		avatar.avatar = map[string]string{"type": "custom", "avatarId": avatarID}
	}
}

func WithRunwayPresetID(presetID string) AvatarOption {
	return func(avatar *Avatar) {
		if presetID == "" {
			return
		}
		if avatar.avatar != nil {
			avatar.avatar["duplicateSource"] = "true"
			return
		}
		avatar.avatar = map[string]string{"type": "runway-preset", "presetId": presetID}
	}
}

func WithRunwayMaxDuration(maxDuration int) AvatarOption {
	return func(avatar *Avatar) {
		avatar.maxDuration = &maxDuration
	}
}

func WithRunwayAPIURL(apiURL string) AvatarOption {
	return func(avatar *Avatar) {
		if apiURL != "" {
			avatar.apiURL = apiURL
		}
	}
}

func (a *Avatar) Start(ctx context.Context) error {
	if a.apiKey == "" {
		return errors.New("RUNWAYML_API_SECRET must be set by arguments or environment variables")
	}
	if info, ok := agent.AvatarStartInfoFromContext(ctx); ok && info.LiveKitURL != "" && info.LiveKitToken != "" {
		if err := a.createSession(ctx, info); err != nil {
			return err
		}
	}
	a.started = true
	return nil
}

func (a *Avatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *Avatar) Provider() string {
	return providerName
}

func (a *Avatar) AvatarIdentity() string {
	return a.avatarIdentity
}

func (a *Avatar) createSession(ctx context.Context, info agent.AvatarStartInfo) error {
	endpoint, headers, body, err := buildRunwayCreateSessionRequest(a, info)
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
		return fmt.Errorf("runway API returned an error: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return err
	}
	a.realtimeSessionID = payload.ID
	return nil
}

func buildRunwayCreateSessionRequest(avatar *Avatar, info agent.AvatarStartInfo) (string, map[string]string, []byte, error) {
	payload := map[string]any{
		"model":  runwayModel,
		"avatar": avatar.avatar,
		"livekit": map[string]any{
			"url":           info.LiveKitURL,
			"token":         info.LiveKitToken,
			"roomName":      info.RoomName,
			"agentIdentity": info.AgentIdentity,
		},
	}
	if avatar.maxDuration != nil {
		payload["maxDuration"] = *avatar.maxDuration
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, nil, err
	}
	headers := map[string]string{
		"Authorization":    "Bearer " + avatar.apiKey,
		"X-Runway-Version": runwayAPIVersion,
		"Content-Type":     "application/json",
	}
	return "/v1/realtime_sessions", headers, body, nil
}

// Deprecated: use Avatar.
type RunwayAvatar = Avatar

// Deprecated: use AvatarOption.
type RunwayAvatarOption = AvatarOption

// Deprecated: use NewAvatar.
func NewRunwayAvatar(apiKey string, opts ...AvatarOption) (*Avatar, error) {
	return NewAvatar(apiKey, opts...)
}
