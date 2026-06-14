package livekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type turnDetectorEncodeFunc func(string) ([]int, error)

type turnDetectorTokenizer struct {
	modelType ModelType
	encode    turnDetectorEncodeFunc
}

func newTurnDetectorTokenizer(modelType ModelType, encode turnDetectorEncodeFunc) *turnDetectorTokenizer {
	return &turnDetectorTokenizer{
		modelType: modelType,
		encode:    encode,
	}
}

func (t *turnDetectorTokenizer) TokenizeTurnDetectorPayload(ctx context.Context, payload []byte) ([]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t == nil || t.encode == nil {
		return nil, errors.New("livekit turn detector tokenizer encoder is not configured")
	}
	text, err := formatTurnDetectorPayload(payload)
	if err != nil {
		return nil, err
	}
	ids, err := t.encode(text)
	if err != nil {
		return nil, err
	}
	if len(ids) > MaxHistoryTokens {
		ids = ids[len(ids)-MaxHistoryTokens:]
	}
	inputIDs := make([]int64, len(ids))
	for i, id := range ids {
		inputIDs[i] = int64(id)
	}
	return inputIDs, nil
}

func formatTurnDetectorPayload(payload []byte) (string, error) {
	var parsed inferencePayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", fmt.Errorf("parse livekit turn detector payload: %w", err)
	}
	if len(parsed.ChatCtx) == 0 {
		return "", errors.New("livekit turn detector chat_ctx is empty")
	}

	messages := make([]inferenceMessage, 0, len(parsed.ChatCtx))
	for _, msg := range parsed.ChatCtx {
		if strings.TrimSpace(msg.Role) == "" || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		content := normalizeTurnDetectorText(msg.Content)
		if content == "" {
			continue
		}
		if len(messages) > 0 && messages[len(messages)-1].Role == msg.Role {
			messages[len(messages)-1].Content += " " + content
			continue
		}
		messages = append(messages, inferenceMessage{Role: msg.Role, Content: content})
	}
	if len(messages) == 0 {
		return "", errors.New("livekit turn detector chat_ctx is empty")
	}

	var builder strings.Builder
	for i, msg := range messages {
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString("<|im_start|>")
		builder.WriteString(msg.Role)
		builder.WriteByte('\n')
		builder.WriteString(msg.Content)
		if i < len(messages)-1 {
			builder.WriteString("<|im_end|>")
		}
	}
	return builder.String(), nil
}

var _ TurnDetectorTokenizer = (*turnDetectorTokenizer)(nil)
