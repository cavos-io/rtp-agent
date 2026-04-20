package hamming

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
)

type HammingMonitor struct {
	apiKey    string
	agentID   string
	baseURL   string
}

func NewHammingMonitor(apiKey string, agentID string) *HammingMonitor {
	return &HammingMonitor{
		apiKey:  apiKey,
		agentID: agentID,
		baseURL: "https://app.hamming.ai",
	}
}

type HammingPayload struct {
	Provider             string                 `json:"provider"`
	ExternalAgentID      string                 `json:"external_agent_id"`
	PayloadSchemaVersion string                 `json:"payload_schema_version"`
	Payload              map[string]interface{} `json:"payload"`
}

func (m *HammingMonitor) SendSessionReport(ctx context.Context, sessionID string, roomName string, events []interface{}) error {
	endpoint := fmt.Sprintf("%s/api/rest/v2/livekit-monitoring", m.baseURL)
	
	payload := HammingPayload{
		Provider:             "custom",
		ExternalAgentID:      m.agentID,
		PayloadSchemaVersion: "2026-03-02",
		Payload: map[string]interface{}{
			"call_id":           sessionID,
			"call_type":         "web",
			"livekit_room_name": roomName,
			"start_timestamp":   time.Now().Unix(), // In real case, use actual start
			"status":            "completed",
			"livekit_capture": map[string]interface{}{
				"events": events,
			},
		},
	}

	jsonBody, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("hamming error: status %d", resp.StatusCode)
	}

	logger.Logger.Infow("Hamming session report sent", "session_id", sessionID)
	return nil
}
