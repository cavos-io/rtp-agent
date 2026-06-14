package livekit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type remoteTurnDetectorResponse struct {
	Probability float64 `json:"probability"`
}

func (m *Model) PredictEndOfTurn(ctx context.Context, chatCtx *llm.ChatContext) (float64, error) {
	payload, err := m.InferencePayload(chatCtx)
	if err != nil {
		return 0, fmt.Errorf("build livekit turn detector request: %w", err)
	}

	url := m.RemoteInferenceURL()
	if url == "" {
		if m.runner == nil {
			return 0, errors.New("livekit turn detector local inference is not implemented")
		}
		return m.runner.RunTurnDetector(ctx, payload)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, RemoteInferenceTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("create livekit turn detector request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("livekit turn detector remote request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read livekit turn detector response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("livekit turn detector remote status %d: %s", resp.StatusCode, string(body))
	}

	var data remoteTurnDetectorResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, fmt.Errorf("parse livekit turn detector response: %w", err)
	}
	if data.Probability < 0 {
		return 1, nil
	}
	return data.Probability, nil
}

var _ agent.TurnDetector = (*Model)(nil)
