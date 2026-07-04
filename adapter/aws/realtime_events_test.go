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
	ctx.Append(&llm.ChatMessage{Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "memory rule"}}})
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
	if len(historyEvents) != 9 {
		t.Fatalf("history event count = %d, want 9", len(historyEvents))
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
	if got := awsRealtimeNestedString(firstHistoryStart, "event", "contentStart", "role"); got != "SYSTEM" {
		t.Fatalf("first history role = %q, want SYSTEM after stripping leading assistant", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, historyEvents[1]), "event", "textInput", "content"); got != "memory rule" {
		t.Fatalf("system history = %q, want memory rule", got)
	}
	userHistoryStart := mustAWSRealtimeJSONEvent(t, historyEvents[3])
	if got := awsRealtimeNestedString(userHistoryStart, "event", "contentStart", "role"); got != "USER" {
		t.Fatalf("second history role = %q, want USER", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, historyEvents[4]), "event", "textInput", "content"); got != "hi\nagain" {
		t.Fatalf("merged user history = %q, want hi\\nagain", got)
	}
	secondHistoryStart := mustAWSRealtimeJSONEvent(t, historyEvents[6])
	if got := awsRealtimeNestedString(secondHistoryStart, "event", "contentStart", "role"); got != "ASSISTANT" {
		t.Fatalf("third history role = %q, want ASSISTANT", got)
	}
}

func TestAWSRealtimeEventBuilderEmitsReferenceInteractiveFalse(t *testing.T) {
	builder := newAWSRealtimeEventBuilder("prompt-1", "audio-1")

	nonInteractive, err := builder.createTextContentStartEvent("content-1", "USER", false)
	if err != nil {
		t.Fatalf("createTextContentStartEvent error = %v", err)
	}
	start := nestedMap(t, mustAWSRealtimeJSONEvent(t, nonInteractive), "event", "contentStart")
	if got := start["interactive"]; got != false {
		t.Fatalf("non-interactive contentStart interactive = %#v, want false", got)
	}
	if got := awsRealtimeNestedString(map[string]any{"root": start}, "root", "textInputConfiguration", "mediaType"); got != "text/plain" {
		t.Fatalf("non-interactive text media type = %q, want text/plain", got)
	}

	interactive, err := builder.createTextContentStartEvent("content-2", "USER", true)
	if err != nil {
		t.Fatalf("createTextContentStartEvent interactive error = %v", err)
	}
	start = nestedMap(t, mustAWSRealtimeJSONEvent(t, interactive), "event", "contentStart")
	if got := start["interactive"]; got != true {
		t.Fatalf("interactive contentStart interactive = %#v, want true", got)
	}
}

func TestAWSRealtimeToolChoiceMatchesReferenceAdapter(t *testing.T) {
	tests := []struct {
		name string
		in   llm.ToolChoice
		key  string
	}{
		{name: "auto", in: "auto", key: "auto"},
		{name: "required", in: "required", key: "any"},
		{
			name: "named function",
			in: map[string]any{
				"type":     "function",
				"function": map[string]any{"name": "lookup"},
			},
			key: "tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			choice := awsRealtimeToolChoice(tt.in)
			mapped, ok := choice.(map[string]any)
			if !ok {
				t.Fatalf("tool choice = %#v, want map", choice)
			}
			if _, ok := mapped[tt.key]; !ok {
				t.Fatalf("tool choice = %#v, want %q key", mapped, tt.key)
			}
			if tt.key == "tool" {
				tool := nestedMap(t, map[string]any{"choice": choice}, "choice", "tool")
				if tool["name"] != "lookup" {
					t.Fatalf("tool choice name = %#v, want lookup", tool["name"])
				}
			}
		})
	}
}

func TestAWSRealtimeEventBuilderOmitsReferenceToolChoiceWithoutTools(t *testing.T) {
	builder := newAWSRealtimeEventBuilder("prompt-1", "audio-1")

	raw, err := builder.createPromptStartEvent("tiffany", 24000, nil, "required")
	if err != nil {
		t.Fatalf("createPromptStartEvent error = %v", err)
	}

	toolConfig := nestedMap(t, mustAWSRealtimeJSONEvent(t, raw), "event", "promptStart", "toolConfiguration")
	if _, ok := toolConfig["toolChoice"]; ok {
		t.Fatalf("toolChoice = %#v, want omitted without tools like reference", toolConfig["toolChoice"])
	}
	tools, ok := toolConfig["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("tools = %#v, want empty reference tool list", toolConfig["tools"])
	}
}

func TestAWSRealtimeEventBuilderCreatesReferenceAudioAndCloseEvents(t *testing.T) {
	builder := newAWSRealtimeEventBuilder("prompt-1", "audio-1")

	audioStart, err := builder.createAudioContentStartEvent(16000)
	if err != nil {
		t.Fatalf("createAudioContentStartEvent error = %v", err)
	}
	start := mustAWSRealtimeJSONEvent(t, audioStart)
	if got := awsRealtimeNestedString(start, "event", "contentStart", "promptName"); got != "prompt-1" {
		t.Fatalf("audio start promptName = %q, want prompt-1", got)
	}
	if got := awsRealtimeNestedString(start, "event", "contentStart", "contentName"); got != "audio-1" {
		t.Fatalf("audio start contentName = %q, want audio-1", got)
	}
	if got := awsRealtimeNestedString(start, "event", "contentStart", "type"); got != "AUDIO" {
		t.Fatalf("audio start type = %q, want AUDIO", got)
	}
	if got := awsRealtimeNestedString(start, "event", "contentStart", "role"); got != "USER" {
		t.Fatalf("audio start role = %q, want USER", got)
	}
	audioConfig := nestedMap(t, start, "event", "contentStart", "audioInputConfiguration")
	assertAWSRealtimeJSONNumber(t, audioConfig["sampleRateHertz"], 16000)
	if got := audioConfig["encoding"]; got != "base64" {
		t.Fatalf("audio encoding = %v, want base64", got)
	}

	audioInput, err := builder.createAudioInputEvent("YWJj")
	if err != nil {
		t.Fatalf("createAudioInputEvent error = %v", err)
	}
	input := mustAWSRealtimeJSONEvent(t, audioInput)
	if got := awsRealtimeNestedString(input, "event", "audioInput", "content"); got != "YWJj" {
		t.Fatalf("audio content = %q, want YWJj", got)
	}

	closeEvents, err := builder.createPromptEndBlock()
	if err != nil {
		t.Fatalf("createPromptEndBlock error = %v", err)
	}
	if len(closeEvents) != 3 {
		t.Fatalf("close event count = %d, want 3", len(closeEvents))
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[0]), "event", "contentEnd", "contentName"); got != "audio-1" {
		t.Fatalf("close contentEnd = %q, want audio-1", got)
	}
	if got := awsRealtimeNestedString(mustAWSRealtimeJSONEvent(t, closeEvents[1]), "event", "promptEnd", "promptName"); got != "prompt-1" {
		t.Fatalf("promptEnd promptName = %q, want prompt-1", got)
	}
	sessionEnd := mustAWSRealtimeJSONEvent(t, closeEvents[2])
	if _, ok := nestedMap(t, sessionEnd, "event")["sessionEnd"].(map[string]any); !ok {
		t.Fatalf("sessionEnd = %#v, want object", sessionEnd)
	}
}

func TestAWSRealtimeEventBuilderCreatesReferenceToolContentStart(t *testing.T) {
	builder := newAWSRealtimeEventBuilder("prompt-1", "audio-1")

	raw, err := builder.createToolContentStartEvent("tool-content-1", "tool-use-1")
	if err != nil {
		t.Fatalf("createToolContentStartEvent error = %v", err)
	}

	start := nestedMap(t, mustAWSRealtimeJSONEvent(t, raw), "event", "contentStart")
	if got := start["promptName"]; got != "prompt-1" {
		t.Fatalf("promptName = %v, want prompt-1", got)
	}
	if got := start["contentName"]; got != "tool-content-1" {
		t.Fatalf("contentName = %v, want tool-content-1", got)
	}
	if got := start["type"]; got != "TOOL" {
		t.Fatalf("tool contentStart type = %#v, want TOOL", got)
	}
	if got := start["role"]; got != "TOOL" {
		t.Fatalf("tool contentStart role = %#v, want TOOL", got)
	}
	if got := start["interactive"]; got != false {
		t.Fatalf("tool contentStart interactive = %#v, want false", got)
	}
	toolConfig := nestedMap(t, map[string]any{"root": start}, "root", "toolResultInputConfiguration")
	if got := toolConfig["toolUseId"]; got != "tool-use-1" {
		t.Fatalf("toolUseId = %v, want tool-use-1", got)
	}
	if got := toolConfig["type"]; got != "TEXT" {
		t.Fatalf("tool input type = %v, want TEXT", got)
	}
	if got := awsRealtimeNestedString(map[string]any{"root": toolConfig}, "root", "textInputConfiguration", "mediaType"); got != "text/plain" {
		t.Fatalf("tool text media type = %q, want text/plain", got)
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
