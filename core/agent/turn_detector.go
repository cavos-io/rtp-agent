package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
)

// LLMTurnDetector uses an LLM to predict if the user has finished speaking.
// It sends the recent conversation history to the LLM and asks for a probability score.
type LLMTurnDetector struct {
	llmInstance llm.LLM
}

func NewLLMTurnDetector(llmInstance llm.LLM) *LLMTurnDetector {
	return &LLMTurnDetector{
		llmInstance: llmInstance,
	}
}

type turnDetectionResult struct {
	Probability float64 `json:"probability"`
}

func (m *LLMTurnDetector) PredictEndOfTurn(ctx context.Context, chatCtx *llm.ChatContext) (float64, error) {
	if len(chatCtx.Items) == 0 {
		return 0.0, nil
	}

	evalCtx := llm.NewChatContext()
	
	systemPrompt := `You are an End-of-Utterance (EOU) detection model.
Analyze the provided conversation history and determine the probability (0.0 to 1.0) that the user has finished their turn and expects a response.
A complete thought, question, or sentence usually indicates a high probability (> 0.8).
A trailing thought, conjunction, or incomplete sentence indicates a low probability (< 0.4).

Respond ONLY with a JSON object in this format:
{"probability": 0.9}`

	evalCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleSystem,
		Content: []llm.ChatContent{{Text: systemPrompt}},
	})

	// Build a text representation of the last few turns
	var history strings.Builder
	startIndex := 0
	if len(chatCtx.Items) > 6 {
		startIndex = len(chatCtx.Items) - 6 // Keep last 6 items for context
	}

	for i := startIndex; i < len(chatCtx.Items); i++ {
		item := chatCtx.Items[i]
		if msg, ok := item.(*llm.ChatMessage); ok {
			role := string(msg.Role)
			text := msg.TextContent()
			if text != "" {
				history.WriteString(fmt.Sprintf("%s: %s\n", role, text))
			}
		}
	}

	evalCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: history.String()}},
	})

	stream, err := m.llmInstance.Chat(ctx, evalCtx)
	if err != nil {
		return 0.0, fmt.Errorf("LLM turn detection failed: %w", err)
	}
	defer stream.Close()

	var responseText string
	for {
		chunk, err := stream.Next()
		if err != nil {
			break
		}
		if chunk.Delta != nil && chunk.Delta.Content != "" {
			responseText += chunk.Delta.Content
		}
	}

	// Clean up markdown block if present
	responseText = strings.TrimSpace(responseText)
	responseText = strings.TrimPrefix(responseText, "```json")
	responseText = strings.TrimPrefix(responseText, "```")
	responseText = strings.TrimSuffix(responseText, "```")
	responseText = strings.TrimSpace(responseText)

	var result turnDetectionResult
	if err := json.Unmarshal([]byte(responseText), &result); err != nil {
		// Fallback to punctuation heuristic if JSON parsing fails
		return m.fallbackHeuristic(chatCtx), fmt.Errorf("failed to parse turn detection JSON: %w", err)
	}

	return result.Probability, nil
}

func (m *LLMTurnDetector) fallbackHeuristic(chatCtx *llm.ChatContext) float64 {
	lastItem := chatCtx.Items[len(chatCtx.Items)-1]
	if msg, ok := lastItem.(*llm.ChatMessage); ok && msg.Role == llm.ChatRoleUser {
		text := strings.TrimSpace(msg.TextContent())
		if len(text) > 0 {
			lastChar := text[len(text)-1]
			if lastChar == '.' || lastChar == '?' || lastChar == '!' {
				return 0.8
			}
		}
	}
	return 0.2
}

var _ TurnDetector = (*LLMTurnDetector)(nil)
