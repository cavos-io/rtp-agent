package aws

import (
	"encoding/json"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestAWSRealtimeEventBuilderCreatesReferencePromptStartBlock(t *testing.T) {
	builder := newAWSRealtimeEventBuilder("prompt-1", "audio-1")
	ctx := llm.NewChatContext()
	ctx.Append(&llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "orphan"}}})
	ctx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hi"}}})
	ctx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "again"}}})
	ctx.Append(&llm.ChatMessage{Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "ok"}}})

	initEvents, historyEvents, err := builder.createPromptStartBlock(awsRealtimePromptStartOptions{
		voiceID:                "tiffany",
		outputSampleRate:       24000,
		systemContent:          "system prompt",
		chatCtx:                ctx,
		maxTokens:              2048,
		topP:                   0.8,
		temperature:            0.6,
		endpointingSensitivity: "HIGH",
	})
	if err != nil {
		t.Fatalf("createPromptStartBlock error = %v", err)
	}
	if len(initEvents) != 5 {
		t.Fatalf("init event count = %d, want 5", len(initEvents))
	}
	if len(historyEvents) != 6 {
		t.Fatalf("history event count = %d, want 6", len(historyEvents))
	}

	sessionStart := mustAWSRealtimeJSONEvent(t, initEvents[0])
	inference := nestedMap(t, sessionStart, "event", "sessionStart", "inferenceConfiguration")
	assertAWSRealtimeJSONNumber(t, inference["maxTokens"], 2048)
	assertAWSRealtimeJSONNumber(t, inference["topP"], 0.8)
	assertAWSRealtimeJSONNumber(t, inference["temperature"], 0.6)
	if got := awsRealtimeNestedString(sessionStart, "event", "sessionStart", "endpointingSensitivity"); got != "HIGH" {
		t.Fatalf("endpointingSensitivity = %q, want HIGH", got)
	}

	promptStart := mustAWSRealtimeJSONEvent(t, initEvents[1])
	audioConfig := nestedMap(t, promptStart, "event", "promptStart", "audioOutputConfiguration")
	if got := audioConfig["voiceId"]; got != "tiffany" {
		t.Fatalf("voiceId = %v, want tiffany", got)
	}
	assertAWSRealtimeJSONNumber(t, audioConfig["sampleRateHertz"], 24000)
	if got := awsRealtimeNestedString(promptStart, "event", "promptStart", "toolUseOutputConfiguration", "mediaType"); got != "application/json" {
		t.Fatalf("tool use media type = %q, want application/json", got)
	}
	toolConfig := nestedMap(t, promptStart, "event", "promptStart", "toolConfiguration")
	tools, ok := toolConfig["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("tools = %#v, want empty array", toolConfig["tools"])
	}

	systemStart := mustAWSRealtimeJSONEvent(t, initEvents[2])
	systemContentName := awsRealtimeNestedString(systemStart, "event", "contentStart", "contentName")
	if systemContentName == "" {
		t.Fatal("system contentName is empty")
	}
	if got := awsRealtimeNestedString(systemStart, "event", "contentStart", "role"); got != "SYSTEM" {
		t.Fatalf("system role = %q, want SYSTEM", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, initEvents[3]), "event", "textInput", "content"); got != "system prompt" {
		t.Fatalf("system content = %q, want system prompt", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, initEvents[4]), "event", "contentEnd", "contentName"); got != systemContentName {
		t.Fatalf("system content end name = %q, want %q", got, systemContentName)
	}

	firstHistoryStart := mustAWSRealtimeJSONEvent(t, historyEvents[0])
	if got := awsRealtimeNestedString(firstHistoryStart, "event", "contentStart", "role"); got != "USER" {
		t.Fatalf("first history role = %q, want USER after stripping leading assistant", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, historyEvents[1]), "event", "textInput", "content"); got != "hi\nagain" {
		t.Fatalf("merged user history = %q, want hi\\nagain", got)
	}
	secondHistoryStart := mustAWSRealtimeJSONEvent(t, historyEvents[3])
	if got := awsRealtimeNestedString(secondHistoryStart, "event", "contentStart", "role"); got != "ASSISTANT" {
		t.Fatalf("second history role = %q, want ASSISTANT", got)
	}
}

func mustAWSRealtimeJSONEvent(t *testing.T, raw string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", raw, err)
	}
	return decoded
}

func nestedMap(t *testing.T, root map[string]any, path ...string) map[string]any {
	t.Helper()
	var current any = root
	for _, key := range path {
		asMap, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("path %v hit %T, want map", path, current)
		}
		current = asMap[key]
	}
	asMap, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("path %v = %T, want map", path, current)
	}
	return asMap
}

func assertAWSRealtimeJSONNumber(t *testing.T, got any, want float64) {
	t.Helper()
	asFloat, ok := got.(float64)
	if !ok {
		t.Fatalf("number = %T(%v), want %v", got, got, want)
	}
	if asFloat != want {
		t.Fatalf("number = %v, want %v", asFloat, want)
	}
}
