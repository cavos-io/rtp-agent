package aws

import (
	"encoding/json"
	"strings"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/google/uuid"
)

const (
	defaultAWSRealtimeInputSampleRate  = 16000
	defaultAWSRealtimeOutputSampleRate = 24000
	defaultAWSRealtimeSampleSizeBits   = 16
	defaultAWSRealtimeChannels         = 1
	defaultAWSRealtimeMaxTokens        = 1024
	defaultAWSRealtimeTopP             = 0.9
	defaultAWSRealtimeTemperature      = 0.7
)

type awsRealtimeEventBuilder struct {
	promptName       string
	audioContentName string
}

type awsRealtimePromptStartOptions struct {
	voiceID                string
	outputSampleRate       int
	systemContent          string
	chatCtx                *llm.ChatContext
	maxTokens              int
	topP                   float64
	temperature            float64
	endpointingSensitivity string
}

func newAWSRealtimeEventBuilder(promptName string, audioContentName string) *awsRealtimeEventBuilder {
	return &awsRealtimeEventBuilder{
		promptName:       promptName,
		audioContentName: audioContentName,
	}
}

func (b *awsRealtimeEventBuilder) createPromptStartBlock(options awsRealtimePromptStartOptions) ([]string, []string, error) {
	normalized := normalizeAWSRealtimePromptStartOptions(options)
	systemContentName := uuid.NewString()
	initEvents := make([]string, 0, 5)

	sessionStart, err := b.createSessionStartEvent(normalized.maxTokens, normalized.topP, normalized.temperature, normalized.endpointingSensitivity)
	if err != nil {
		return nil, nil, err
	}
	initEvents = append(initEvents, sessionStart)
	promptStart, err := b.createPromptStartEvent(normalized.voiceID, normalized.outputSampleRate)
	if err != nil {
		return nil, nil, err
	}
	initEvents = append(initEvents, promptStart)
	systemBlock, err := b.createTextContentBlock(systemContentName, "SYSTEM", normalized.systemContent)
	if err != nil {
		return nil, nil, err
	}
	initEvents = append(initEvents, systemBlock...)

	historyEvents, err := b.createHistoryEvents(normalized.chatCtx)
	if err != nil {
		return nil, nil, err
	}
	return initEvents, historyEvents, nil
}

func normalizeAWSRealtimePromptStartOptions(options awsRealtimePromptStartOptions) awsRealtimePromptStartOptions {
	if options.voiceID == "" {
		options.voiceID = defaultAWSRealtimeVoice
	}
	if options.outputSampleRate == 0 {
		options.outputSampleRate = defaultAWSRealtimeOutputSampleRate
	}
	if options.maxTokens == 0 {
		options.maxTokens = defaultAWSRealtimeMaxTokens
	}
	if options.topP == 0 {
		options.topP = defaultAWSRealtimeTopP
	}
	if options.temperature == 0 {
		options.temperature = defaultAWSRealtimeTemperature
	}
	if options.endpointingSensitivity == "" {
		options.endpointingSensitivity = defaultAWSRealtimeTurnDetection
	}
	return options
}

func (b *awsRealtimeEventBuilder) createSessionStartEvent(maxTokens int, topP float64, temperature float64, endpointingSensitivity string) (string, error) {
	return marshalAWSRealtimeEvent(map[string]any{
		"sessionStart": map[string]any{
			"inferenceConfiguration": map[string]any{
				"maxTokens":   maxTokens,
				"topP":        topP,
				"temperature": temperature,
			},
			"endpointingSensitivity": endpointingSensitivity,
		},
	})
}

func (b *awsRealtimeEventBuilder) createPromptStartEvent(voiceID string, sampleRate int) (string, error) {
	return marshalAWSRealtimeEvent(map[string]any{
		"promptStart": map[string]any{
			"promptName": b.promptName,
			"textOutputConfiguration": map[string]any{
				"mediaType": "text/plain",
			},
			"audioOutputConfiguration": map[string]any{
				"mediaType":       "audio/lpcm",
				"sampleRateHertz": sampleRate,
				"sampleSizeBits":  defaultAWSRealtimeSampleSizeBits,
				"channelCount":    defaultAWSRealtimeChannels,
				"voiceId":         voiceID,
				"encoding":        "base64",
				"audioType":       "SPEECH",
			},
			"toolUseOutputConfiguration": map[string]any{
				"mediaType": "application/json",
			},
			"toolConfiguration": map[string]any{
				"tools": []any{},
			},
		},
	})
}

func (b *awsRealtimeEventBuilder) createTextContentBlock(contentName string, role string, content string) ([]string, error) {
	start, err := b.createTextContentStartEvent(contentName, role, false)
	if err != nil {
		return nil, err
	}
	text, err := b.createTextContentEvent(contentName, content)
	if err != nil {
		return nil, err
	}
	end, err := b.createContentEndEvent(contentName)
	if err != nil {
		return nil, err
	}
	return []string{start, text, end}, nil
}

func (b *awsRealtimeEventBuilder) createTextContentStartEvent(contentName string, role string, interactive bool) (string, error) {
	return marshalAWSRealtimeEvent(map[string]any{
		"contentStart": map[string]any{
			"promptName":  b.promptName,
			"contentName": contentName,
			"type":        "TEXT",
			"interactive": interactive,
			"role":        role,
			"textInputConfiguration": map[string]any{
				"mediaType": "text/plain",
			},
		},
	})
}

func (b *awsRealtimeEventBuilder) createTextContentEvent(contentName string, content string) (string, error) {
	return marshalAWSRealtimeEvent(map[string]any{
		"textInput": map[string]any{
			"promptName":  b.promptName,
			"contentName": contentName,
			"content":     content,
		},
	})
}

func (b *awsRealtimeEventBuilder) createContentEndEvent(contentName string) (string, error) {
	return marshalAWSRealtimeEvent(map[string]any{
		"contentEnd": map[string]any{
			"promptName":  b.promptName,
			"contentName": contentName,
		},
	})
}

func (b *awsRealtimeEventBuilder) createHistoryEvents(chatCtx *llm.ChatContext) ([]string, error) {
	messages := awsRealtimeHistoryMessages(chatCtx)
	events := make([]string, 0, len(messages)*3)
	for _, message := range messages {
		block, err := b.createTextContentBlock(uuid.NewString(), message.role, message.text)
		if err != nil {
			return nil, err
		}
		events = append(events, block...)
	}
	return events, nil
}

type awsRealtimeHistoryMessage struct {
	role string
	text string
}

func awsRealtimeHistoryMessages(chatCtx *llm.ChatContext) []awsRealtimeHistoryMessage {
	if chatCtx == nil {
		return nil
	}
	merged := make([]awsRealtimeHistoryMessage, 0, len(chatCtx.Items))
	for _, item := range chatCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok {
			continue
		}
		role := strings.ToUpper(string(msg.Role))
		if role != "USER" && role != "ASSISTANT" && role != "SYSTEM" {
			continue
		}
		text := msg.TextContent()
		if strings.TrimSpace(text) == "" {
			continue
		}
		if len(merged) > 0 && merged[len(merged)-1].role == role {
			merged[len(merged)-1].text += "\n" + text
			continue
		}
		merged = append(merged, awsRealtimeHistoryMessage{role: role, text: text})
	}
	if len(merged) > 0 && merged[0].role == "ASSISTANT" {
		merged = merged[1:]
	}
	return merged
}

func marshalAWSRealtimeEvent(event map[string]any) (string, error) {
	data, err := json.Marshal(map[string]any{"event": event})
	if err != nil {
		return "", err
	}
	return string(data), nil
}
