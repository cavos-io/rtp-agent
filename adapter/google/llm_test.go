package google

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"google.golang.org/genai"
)

type googleRequestTestTool struct{}

func (googleRequestTestTool) ID() string          { return "lookup" }
func (googleRequestTestTool) Name() string        { return "lookup" }
func (googleRequestTestTool) Description() string { return "look up information" }
func (googleRequestTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}
func (googleRequestTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

type googleNestedSchemaTestTool struct{}

func (googleNestedSchemaTestTool) ID() string          { return "schedule" }
func (googleNestedSchemaTestTool) Name() string        { return "schedule" }
func (googleNestedSchemaTestTool) Description() string { return "schedule a callback" }
func (googleNestedSchemaTestTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"priority": map[string]any{
				"type":        "string",
				"description": "callback priority",
				"enum":        []any{"low", "high"},
			},
			"window": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"start": map[string]any{"type": "string", "description": "start time"},
				},
				"required": []any{"start"},
			},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"required": []any{"priority", "window"},
	}
}
func (googleNestedSchemaTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

func TestNewGoogleLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "env-key")

	if got := resolveGoogleAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env key", got)
	}
	if got := resolveGoogleAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("resolved API key = %q, want explicit key", got)
	}
}

func TestNewGoogleLLMRequiresAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")

	_, err := NewGoogleLLM("", "")
	if err == nil {
		t.Fatal("NewGoogleLLM returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GOOGLE_API_KEY") {
		t.Fatalf("NewGoogleLLM error = %q, want GOOGLE_API_KEY guidance", err)
	}
}

func TestBuildGoogleContentsGroupsToolCallsWithResponses(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCall{ID: groupID + "/tool-2", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
		&llm.FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	contents, systemText := buildGoogleContents(ctx)

	if systemText != "" {
		t.Fatalf("systemText = %q, want empty", systemText)
	}
	if len(contents) != 2 {
		t.Fatalf("len(contents) = %d, want 2: %#v", len(contents), contents)
	}
	if contents[0].Role != genai.RoleModel {
		t.Fatalf("first role = %q, want model", contents[0].Role)
	}
	assertGoogleTextPart(t, contents[0].Parts, 0, "checking")
	assertGoogleFunctionCallPart(t, contents[0].Parts, 1, "call_lookup", "lookup")
	assertGoogleFunctionCallPart(t, contents[0].Parts, 2, "call_weather", "weather")
	if contents[1].Role != genai.RoleUser {
		t.Fatalf("second role = %q, want user", contents[1].Role)
	}
	assertGoogleFunctionResponsePart(t, contents[1].Parts, 0, "call_lookup", "lookup", "Paris")
	assertGoogleFunctionResponsePart(t, contents[1].Parts, 1, "call_weather", "weather", "sunny")
}

func TestBuildGoogleContentsPreservesMultipleMatchedToolOutputs(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "assistant-turn", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: "assistant-turn/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output-1", CallID: "call_lookup", Name: "lookup", Output: "first"},
		&llm.FunctionCallOutput{ID: "lookup-output-2", CallID: "call_lookup", Name: "lookup", Output: "second"},
	}

	contents, _ := buildGoogleContents(ctx)

	if len(contents) != 2 {
		t.Fatalf("len(contents) = %d, want 2: %#v", len(contents), contents)
	}
	if contents[1].Role != genai.RoleUser {
		t.Fatalf("tool output role = %q, want user", contents[1].Role)
	}
	if len(contents[1].Parts) != 2 {
		t.Fatalf("tool output parts = %d, want all matched outputs: %#v", len(contents[1].Parts), contents[1].Parts)
	}
	assertGoogleFunctionResponsePart(t, contents[1].Parts, 0, "call_lookup", "lookup", "first")
	assertGoogleFunctionResponsePart(t, contents[1].Parts, 1, "call_lookup", "lookup", "second")
}

func TestBuildGoogleContentsInjectsReferenceThoughtSignatures(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "assistant-turn", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: "assistant-turn/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
	}

	contents, _ := buildGoogleContentsWithThoughtSignatures(ctx, map[string][]byte{
		"call_lookup": []byte("signature"),
	})

	if len(contents) == 0 || len(contents[0].Parts) < 2 {
		t.Fatalf("contents = %#v, want model function_call part", contents)
	}
	call := contents[0].Parts[1].FunctionCall
	if call == nil || call.ID != "call_lookup" {
		t.Fatalf("function call part = %#v, want call_lookup", contents[0].Parts[1])
	}
	if got := contents[0].Parts[1].ThoughtSignature; string(got) != "signature" {
		t.Fatalf("thought signature = %q, want signature", got)
	}
}

func TestGoogleLLMReferenceModelsRequireThoughtSignatures(t *testing.T) {
	for _, model := range []string{"gemini-2.5-flash", "gemini-3-pro", "models/gemini-3-flash"} {
		if !googleModelRequiresThoughtSignatures(model) {
			t.Fatalf("googleModelRequiresThoughtSignatures(%q) = false, want true", model)
		}
	}
}

func TestBuildGoogleContentsFiltersUnmatchedToolItems(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.FunctionCall{ID: "orphan-call", CallID: "call_missing_output", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "orphan-output", CallID: "call_missing_call", Name: "lookup", Output: "ignored"},
	}

	contents, _ := buildGoogleContents(ctx)

	if len(contents) != 1 {
		t.Fatalf("len(contents) = %d, want 1: %#v", len(contents), contents)
	}
	if contents[0].Role != genai.RoleUser {
		t.Fatalf("role = %q, want user", contents[0].Role)
	}
	assertGoogleTextPart(t, contents[0].Parts, 0, "hello")
}

func TestBuildGoogleContentsIncludesImageParts(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{
			ID:   "user",
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: "describe"},
				{Image: &llm.ImageContent{Image: "data:image/png;base64," + imageData}},
				{Image: &llm.ImageContent{Image: "https://example.test/image.jpg", MimeType: "image/jpeg"}},
			},
		},
	}

	contents, _ := buildGoogleContents(ctx)

	parts := contents[0].Parts
	if len(parts) != 3 {
		t.Fatalf("len(parts) = %d, want 3: %#v", len(parts), parts)
	}
	if parts[0].Text != "describe" {
		t.Fatalf("text part = %#v", parts[0])
	}
	if parts[1].InlineData == nil || !reflect.DeepEqual(parts[1].InlineData.Data, []byte("png-bytes")) || parts[1].InlineData.MIMEType != "image/png" {
		t.Fatalf("inline image part = %#v", parts[1])
	}
	if parts[2].FileData == nil || parts[2].FileData.FileURI != "https://example.test/image.jpg" || parts[2].FileData.MIMEType != "image/jpeg" {
		t.Fatalf("file image part = %#v", parts[2])
	}
}

func TestBuildGoogleContentsCollectsSystemText(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "base"}}},
		&llm.ChatMessage{ID: "developer", Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "dev"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	contents, systemText := buildGoogleContents(ctx)

	if systemText != "base\ndev\n" {
		t.Fatalf("systemText = %q, want base/dev", systemText)
	}
	if len(contents) != 1 {
		t.Fatalf("len(contents) = %d, want 1", len(contents))
	}
	assertGoogleTextPart(t, contents[0].Parts, 0, "hello")
}

func TestBuildGoogleContentsInjectsDummyUserAfterModelTurn(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "assistant", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "done"}}},
	}

	contents, _ := buildGoogleContents(ctx)

	if len(contents) != 2 {
		t.Fatalf("len(contents) = %d, want 2: %#v", len(contents), contents)
	}
	if contents[0].Role != genai.RoleModel {
		t.Fatalf("first role = %q, want model", contents[0].Role)
	}
	assertGoogleTextPart(t, contents[0].Parts, 0, "done")
	if contents[1].Role != genai.RoleUser {
		t.Fatalf("second role = %q, want dummy user", contents[1].Role)
	}
	assertGoogleTextPart(t, contents[1].Parts, 0, ".")
}

func TestBuildGoogleFunctionDeclarationKeepsStringRequiredFields(t *testing.T) {
	declaration := buildGoogleFunctionDeclaration(googleRequestTestTool{})

	if declaration.Name != "lookup" {
		t.Fatalf("Name = %q, want lookup", declaration.Name)
	}
	if declaration.Parameters == nil {
		t.Fatalf("Parameters is nil")
	}
	if len(declaration.Parameters.Required) != 1 || declaration.Parameters.Required[0] != "query" {
		t.Fatalf("Required = %#v, want query", declaration.Parameters.Required)
	}
	if declaration.Parameters.Properties["query"] == nil {
		t.Fatalf("query property missing: %#v", declaration.Parameters.Properties)
	}
}

func TestBuildGoogleFunctionDeclarationPreservesNestedSchema(t *testing.T) {
	declaration := buildGoogleFunctionDeclaration(googleNestedSchemaTestTool{})

	params := declaration.Parameters
	if params.Type != genai.TypeObject {
		t.Fatalf("parameters type = %q, want OBJECT", params.Type)
	}
	if !reflect.DeepEqual(params.Required, []string{"priority", "window"}) {
		t.Fatalf("required = %#v, want priority/window", params.Required)
	}
	priority := params.Properties["priority"]
	if priority == nil {
		t.Fatalf("priority property missing: %#v", params.Properties)
	}
	if priority.Type != genai.TypeString || priority.Description != "callback priority" {
		t.Fatalf("priority schema = %#v, want string with description", priority)
	}
	if !reflect.DeepEqual(priority.Enum, []string{"low", "high"}) {
		t.Fatalf("priority enum = %#v, want low/high", priority.Enum)
	}
	window := params.Properties["window"]
	if window == nil || window.Type != genai.TypeObject {
		t.Fatalf("window schema = %#v, want object", window)
	}
	if !reflect.DeepEqual(window.Required, []string{"start"}) {
		t.Fatalf("window required = %#v, want start", window.Required)
	}
	if window.Properties["start"] == nil || window.Properties["start"].Type != genai.TypeString {
		t.Fatalf("window start schema = %#v, want string", window.Properties["start"])
	}
	tags := params.Properties["tags"]
	if tags == nil || tags.Type != genai.TypeArray || tags.Items == nil || tags.Items.Type != genai.TypeString {
		t.Fatalf("tags schema = %#v, want string array", tags)
	}
}

func TestBuildGoogleToolConfigMapsNamedToolChoice(t *testing.T) {
	config := buildGoogleToolConfig([]llm.Tool{googleRequestTestTool{}}, map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": "lookup",
		},
	})

	if config == nil || config.FunctionCallingConfig == nil {
		t.Fatalf("tool config = %#v, want function calling config", config)
	}
	if config.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeAny {
		t.Fatalf("mode = %q, want ANY", config.FunctionCallingConfig.Mode)
	}
	if len(config.FunctionCallingConfig.AllowedFunctionNames) != 1 || config.FunctionCallingConfig.AllowedFunctionNames[0] != "lookup" {
		t.Fatalf("allowed names = %#v, want lookup", config.FunctionCallingConfig.AllowedFunctionNames)
	}
}

func TestBuildGoogleToolConfigMapsNoneToolChoice(t *testing.T) {
	config := buildGoogleToolConfig([]llm.Tool{googleRequestTestTool{}}, "none")

	if config == nil || config.FunctionCallingConfig == nil {
		t.Fatalf("tool config = %#v, want function calling config", config)
	}
	if config.FunctionCallingConfig.Mode != genai.FunctionCallingConfigModeNone {
		t.Fatalf("mode = %q, want NONE", config.FunctionCallingConfig.Mode)
	}
}

func TestBuildGoogleGenerateContentConfigDropsToolsWithCachedContentLikeReference(t *testing.T) {
	options := &llm.ChatOptions{
		Tools:      []llm.Tool{googleRequestTestTool{}},
		ToolChoice: "required",
		ExtraParams: map[string]any{
			"cached_content":    "cachedContents/prefix",
			"temperature":       0.7,
			"max_output_tokens": 128,
		},
	}

	config := buildGoogleGenerateContentConfig(options, "system prompt\n")

	if config.CachedContent != "cachedContents/prefix" {
		t.Fatalf("CachedContent = %q, want cachedContents/prefix", config.CachedContent)
	}
	if config.SystemInstruction != nil {
		t.Fatalf("SystemInstruction = %#v, want nil with cached_content", config.SystemInstruction)
	}
	if config.Tools != nil {
		t.Fatalf("Tools = %#v, want nil with cached_content", config.Tools)
	}
	if config.ToolConfig != nil {
		t.Fatalf("ToolConfig = %#v, want nil with cached_content", config.ToolConfig)
	}
	if config.Temperature == nil || *config.Temperature != float32(0.7) {
		t.Fatalf("Temperature = %#v, want 0.7", config.Temperature)
	}
	if config.MaxOutputTokens != 128 {
		t.Fatalf("MaxOutputTokens = %d, want 128", config.MaxOutputTokens)
	}
}

func TestBuildGoogleGenerateContentConfigAppliesReferenceResponseSchemaExtra(t *testing.T) {
	options := &llm.ChatOptions{
		ExtraParams: map[string]any{
			"response_schema": map[string]any{
				"type":     "object",
				"required": []any{"answer"},
				"properties": map[string]any{
					"answer": map[string]any{"type": "string"},
				},
			},
			"response_mime_type": "application/json",
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	if config.ResponseSchema == nil {
		t.Fatal("ResponseSchema = nil, want schema from response_schema extra param")
	}
	if config.ResponseSchema.Type != genai.TypeObject {
		t.Fatalf("ResponseSchema type = %q, want OBJECT", config.ResponseSchema.Type)
	}
	if len(config.ResponseSchema.Required) != 1 || config.ResponseSchema.Required[0] != "answer" {
		t.Fatalf("ResponseSchema required = %#v, want answer", config.ResponseSchema.Required)
	}
	answer := config.ResponseSchema.Properties["answer"]
	if answer == nil || answer.Type != genai.TypeString {
		t.Fatalf("answer schema = %#v, want string", answer)
	}
	if config.ResponseMIMEType != "application/json" {
		t.Fatalf("ResponseMIMEType = %q, want application/json", config.ResponseMIMEType)
	}
}

func TestBuildGoogleGenerateContentConfigMapsResponseFormatToReferenceSchema(t *testing.T) {
	options := &llm.ChatOptions{
		ResponseFormat: map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "WeatherAnswer",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"summary": map[string]any{"type": "string"},
					},
					"required": []any{"summary"},
				},
			},
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	if config.ResponseMIMEType != "application/json" {
		t.Fatalf("ResponseMIMEType = %q, want application/json", config.ResponseMIMEType)
	}
	if config.ResponseJsonSchema != nil {
		t.Fatalf("ResponseJsonSchema = %#v, want nil because reference uses response_schema", config.ResponseJsonSchema)
	}
	if config.ResponseSchema == nil {
		t.Fatal("ResponseSchema = nil, want schema from response_format")
	}
	if config.ResponseSchema.Type != genai.TypeObject {
		t.Fatalf("ResponseSchema type = %q, want OBJECT", config.ResponseSchema.Type)
	}
	summary := config.ResponseSchema.Properties["summary"]
	if summary == nil || summary.Type != genai.TypeString {
		t.Fatalf("summary schema = %#v, want string", summary)
	}
	if len(config.ResponseSchema.Required) != 1 || config.ResponseSchema.Required[0] != "summary" {
		t.Fatalf("ResponseSchema required = %#v, want summary", config.ResponseSchema.Required)
	}
}

func TestBuildGoogleGenerateContentConfigAppliesReferenceServiceTierExtra(t *testing.T) {
	options := &llm.ChatOptions{
		ExtraParams: map[string]any{
			"service_tier": "priority",
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	if config.ServiceTier != genai.ServiceTierPriority {
		t.Fatalf("ServiceTier = %q, want priority", config.ServiceTier)
	}
}

func TestBuildGoogleGenerateContentConfigAppliesReferenceThinkingConfigExtra(t *testing.T) {
	options := &llm.ChatOptions{
		ExtraParams: map[string]any{
			"thinking_config": map[string]any{
				"thinking_budget":  0,
				"include_thoughts": true,
			},
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	if config.ThinkingConfig == nil {
		t.Fatal("ThinkingConfig = nil, want reference thinking_config extra")
	}
	if config.ThinkingConfig.ThinkingBudget == nil || *config.ThinkingConfig.ThinkingBudget != 0 {
		t.Fatalf("ThinkingBudget = %#v, want 0", config.ThinkingConfig.ThinkingBudget)
	}
	if !config.ThinkingConfig.IncludeThoughts {
		t.Fatal("IncludeThoughts = false, want true")
	}
}

func TestBuildGoogleGenerateContentConfigAppliesReferenceSafetySettingsExtra(t *testing.T) {
	safety := []*genai.SafetySetting{{
		Category:  genai.HarmCategoryHarassment,
		Threshold: genai.HarmBlockThresholdBlockOnlyHigh,
	}}
	options := &llm.ChatOptions{
		ExtraParams: map[string]any{
			"safety_settings": safety,
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	if !reflect.DeepEqual(config.SafetySettings, safety) {
		t.Fatalf("safety settings = %#v, want %#v", config.SafetySettings, safety)
	}
}

func TestBuildGoogleGenerateContentConfigAppliesReferenceMediaResolutionExtra(t *testing.T) {
	options := &llm.ChatOptions{
		ExtraParams: map[string]any{
			"media_resolution": "MEDIA_RESOLUTION_HIGH",
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	if config.MediaResolution != genai.MediaResolutionHigh {
		t.Fatalf("MediaResolution = %q, want high", config.MediaResolution)
	}
}

func TestBuildGoogleGenerateContentConfigAppliesReferenceRetrievalConfigExtra(t *testing.T) {
	retrieval := &genai.RetrievalConfig{LanguageCode: "id-ID"}
	options := &llm.ChatOptions{
		ExtraParams: map[string]any{
			"retrieval_config": retrieval,
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	if config.ToolConfig == nil || config.ToolConfig.RetrievalConfig != retrieval {
		t.Fatalf("tool config = %#v, want retrieval config", config.ToolConfig)
	}
}

func TestBuildGoogleGenerateContentConfigAppliesReferenceResponseModalitiesExtra(t *testing.T) {
	options := &llm.ChatOptions{
		ExtraParams: map[string]any{
			"response_modalities": []any{"AUDIO", "TEXT"},
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	want := []string{"AUDIO", "TEXT"}
	if !reflect.DeepEqual(config.ResponseModalities, want) {
		t.Fatalf("ResponseModalities = %#v, want %#v", config.ResponseModalities, want)
	}
}

func TestBuildGoogleGenerateContentConfigAppliesReferenceSpeechConfigExtra(t *testing.T) {
	speech := &genai.SpeechConfig{
		LanguageCode: "en-US",
		VoiceConfig: &genai.VoiceConfig{
			PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: "Puck"},
		},
	}
	options := &llm.ChatOptions{
		ExtraParams: map[string]any{
			"speech_config": speech,
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	if config.SpeechConfig != speech {
		t.Fatalf("SpeechConfig = %#v, want %#v", config.SpeechConfig, speech)
	}
}

func TestBuildGoogleGenerateContentConfigAppliesReferenceAudioTimestampExtra(t *testing.T) {
	options := &llm.ChatOptions{
		ExtraParams: map[string]any{
			"audio_timestamp": true,
		},
	}

	config := buildGoogleGenerateContentConfig(options, "")

	if !config.AudioTimestamp {
		t.Fatal("AudioTimestamp = false, want true")
	}
}

func TestGoogleLLMStreamNextAfterCloseReturnsEOFWithoutReading(t *testing.T) {
	readAfterClose := false
	stopped := false
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			readAfterClose = true
			return &genai.GenerateContentResponse{}, nil, true
		},
		stop: func() {
			stopped = true
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err := stream.Next()

	if err != io.EOF {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
	if readAfterClose {
		t.Fatal("Next() read provider iterator after Close")
	}
	if !stopped {
		t.Fatal("Close() did not stop provider iterator")
	}
}

func TestGoogleLLMStreamCloseUnblocksPendingNext(t *testing.T) {
	released := make(chan struct{})
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			<-released
			return nil, nil, false
		},
		stop: func() {
			close(released)
		},
	}

	errCh := make(chan error, 1)
	go func() {
		chunk, err := stream.Next()
		if chunk != nil {
			errCh <- errors.New("Next returned chunk after Close")
			return
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		t.Fatalf("Next returned before Close: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after Close error = %v, want EOF", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next did not unblock after Close")
	}
}

func TestGoogleLLMStreamPreservesProviderFunctionCallID(t *testing.T) {
	read := false
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if read {
				return nil, nil, false
			}
			read = true
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{
						Parts: []*genai.Part{{
							FunctionCall: &genai.FunctionCall{
								ID:   "provider-call-123",
								Name: "lookup",
								Args: map[string]any{"query": "weather"},
							},
						}},
					},
				}},
			}, nil, true
		},
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk == nil || chunk.Delta == nil || len(chunk.Delta.ToolCalls) != 1 {
		t.Fatalf("chunk = %#v, want one tool call", chunk)
	}
	call := chunk.Delta.ToolCalls[0]
	if call.CallID != "provider-call-123" {
		t.Fatalf("CallID = %q, want provider-call-123", call.CallID)
	}
	if call.Name != "lookup" || call.Type != "function" {
		t.Fatalf("tool call = %#v, want lookup function", call)
	}
	if call.Arguments != `{"query":"weather"}` {
		t.Fatalf("Arguments = %q, want compact JSON args", call.Arguments)
	}
}

func TestGoogleLLMStreamGeneratesDistinctReferenceFunctionCallIDs(t *testing.T) {
	responses := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{
					Parts: []*genai.Part{{
						FunctionCall: &genai.FunctionCall{
							Name: "lookup",
							Args: map[string]any{"query": "first"},
						},
					}},
				},
			}},
		},
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{
					Parts: []*genai.Part{{
						FunctionCall: &genai.FunctionCall{
							Name: "lookup",
							Args: map[string]any{"query": "second"},
						},
					}},
				},
			}},
		},
	}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}

	firstID := first.Delta.ToolCalls[0].CallID
	secondID := second.Delta.ToolCalls[0].CallID
	if !strings.HasPrefix(firstID, "function_call_") || !strings.HasPrefix(secondID, "function_call_") {
		t.Fatalf("generated call IDs = %q, %q, want function_call_ prefix", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("generated call IDs both %q, want distinct IDs", firstID)
	}
}

func TestGoogleLLMStreamStoresReferenceThoughtSignatures(t *testing.T) {
	read := false
	signatures := map[string][]byte{}
	stream := &googleLLMStream{
		thoughtSignatures: signatures,
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if read {
				return nil, nil, false
			}
			read = true
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{
						Parts: []*genai.Part{{
							FunctionCall: &genai.FunctionCall{
								ID:   "provider-call-123",
								Name: "lookup",
								Args: map[string]any{"query": "weather"},
							},
							ThoughtSignature: []byte("signature"),
						}},
					},
				}},
			}, nil, true
		},
	}

	chunk, err := stream.Next()

	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk == nil || chunk.Delta == nil || len(chunk.Delta.ToolCalls) != 1 {
		t.Fatalf("chunk = %#v, want one tool call", chunk)
	}
	if got := signatures["provider-call-123"]; string(got) != "signature" {
		t.Fatalf("stored signature = %q, want signature", got)
	}
}

func TestGoogleLLMStreamEmitsPartsAsOrderedDeltas(t *testing.T) {
	read := false
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if read {
				return nil, nil, false
			}
			read = true
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "checking"},
							{
								FunctionCall: &genai.FunctionCall{
									ID:   "call_lookup",
									Name: "lookup",
									Args: map[string]any{"query": "weather"},
								},
							},
						},
					},
				}},
			}, nil, true
		},
	}

	textChunk, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if textChunk == nil || textChunk.Delta == nil {
		t.Fatalf("first chunk = %#v, want text delta", textChunk)
	}
	if textChunk.Delta.Content != "checking" {
		t.Fatalf("first content = %q, want checking", textChunk.Delta.Content)
	}
	if len(textChunk.Delta.ToolCalls) != 0 {
		t.Fatalf("first tool calls = %#v, want none", textChunk.Delta.ToolCalls)
	}

	toolChunk, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if toolChunk == nil || toolChunk.Delta == nil || len(toolChunk.Delta.ToolCalls) != 1 {
		t.Fatalf("second chunk = %#v, want one tool-call delta", toolChunk)
	}
	if toolChunk.Delta.Content != "" {
		t.Fatalf("second content = %q, want empty", toolChunk.Delta.Content)
	}
	call := toolChunk.Delta.ToolCalls[0]
	if call.CallID != "call_lookup" || call.Name != "lookup" || call.Arguments != `{"query":"weather"}` {
		t.Fatalf("tool call = %#v, want lookup weather", call)
	}
}

func TestGoogleLLMStreamPrioritizesFunctionCallOverTextLikeReference(t *testing.T) {
	read := false
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if read {
				return nil, nil, false
			}
			read = true
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{
						Parts: []*genai.Part{{
							Text: "ignored when tool call exists",
							FunctionCall: &genai.FunctionCall{
								ID:   "call_lookup",
								Name: "lookup",
								Args: map[string]any{"query": "weather"},
							},
						}},
					},
				}},
			}, nil, true
		},
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk == nil || chunk.Delta == nil || len(chunk.Delta.ToolCalls) != 1 {
		t.Fatalf("chunk = %#v, want one tool-call delta", chunk)
	}
	if chunk.Delta.Content != "" {
		t.Fatalf("content = %q, want empty when function_call is present", chunk.Delta.Content)
	}
	call := chunk.Delta.ToolCalls[0]
	if call.CallID != "call_lookup" || call.Name != "lookup" || call.Arguments != `{"query":"weather"}` {
		t.Fatalf("tool call = %#v, want lookup weather", call)
	}
}

func TestGoogleLLMStreamTagsChunksWithReferenceRequestID(t *testing.T) {
	read := false
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if read {
				return nil, nil, false
			}
			read = true
			return &genai.GenerateContentResponse{
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
					PromptTokenCount:     3,
					CandidatesTokenCount: 2,
					TotalTokenCount:      5,
				},
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "checking"},
							{
								FunctionCall: &genai.FunctionCall{
									ID:   "call_lookup",
									Name: "lookup",
									Args: map[string]any{"query": "weather"},
								},
							},
						},
					},
				}},
			}, nil, true
		},
	}

	usageChunk, err := stream.Next()
	if err != nil {
		t.Fatalf("usage Next() error = %v", err)
	}
	textChunk, err := stream.Next()
	if err != nil {
		t.Fatalf("text Next() error = %v", err)
	}
	toolChunk, err := stream.Next()
	if err != nil {
		t.Fatalf("tool Next() error = %v", err)
	}

	if usageChunk.ID == "" {
		t.Fatal("usage chunk ID = empty, want reference request id")
	}
	if textChunk.ID != usageChunk.ID || toolChunk.ID != usageChunk.ID {
		t.Fatalf("chunk IDs = usage %q text %q tool %q, want same request id", usageChunk.ID, textChunk.ID, toolChunk.ID)
	}
}

func TestGoogleLLMStreamSkipsEmptyProviderDeltas(t *testing.T) {
	responses := []*genai.GenerateContentResponse{
		{},
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{
					Parts: []*genai.Part{{Text: "hello"}},
				},
			}},
		},
	}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk == nil || chunk.Delta == nil || chunk.Delta.Content != "hello" {
		t.Fatalf("chunk = %#v, want first non-empty delta", chunk)
	}
}

func TestGoogleLLMStreamReturnsAPIStatusErrorWhenNoResponseGenerated(t *testing.T) {
	responses := []*genai.GenerateContentResponse{{}}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Message != "no response generated" {
		t.Fatalf("APIStatusError message = %q, want no response generated", statusErr.Message)
	}
	if !statusErr.Retryable {
		t.Fatal("APIStatusError retryable = false, want true before any response output")
	}
	if statusErr.RequestID == "" {
		t.Fatal("APIStatusError request ID empty, want reference stream request ID")
	}
}

func TestGoogleLLMStreamReturnsAPIStatusErrorWhenPageDoneWithoutOutput(t *testing.T) {
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			return nil, genai.ErrPageDone, true
		},
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Message != "no response generated" {
		t.Fatalf("APIStatusError message = %q, want no response generated", statusErr.Message)
	}
	if statusErr.RequestID == "" {
		t.Fatal("APIStatusError request ID empty, want reference stream request ID")
	}
}

func TestGoogleLLMStreamReportsNoResponseFinishReasonLikeReference(t *testing.T) {
	responses := []*genai.GenerateContentResponse{{
		Candidates: []*genai.Candidate{{
			FinishReason: genai.FinishReasonStop,
		}},
	}}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Message != "no response generated" {
		t.Fatalf("APIStatusError message = %q, want no response generated", statusErr.Message)
	}
	if statusErr.Body != "finish reason: STOP" {
		t.Fatalf("APIStatusError body = %#v, want finish reason", statusErr.Body)
	}
}

func TestGoogleLLMStreamKeepsNoResponseFinishReasonAfterUsageChunk(t *testing.T) {
	responses := []*genai.GenerateContentResponse{{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: 1,
			TotalTokenCount:  1,
		},
		Candidates: []*genai.Candidate{{
			FinishReason: genai.FinishReasonStop,
		}},
	}}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	usage, err := stream.Next()
	if err != nil {
		t.Fatalf("usage Next error = %v", err)
	}
	if usage == nil || usage.Usage == nil {
		t.Fatalf("usage chunk = %#v, want usage before no-response error", usage)
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Body != "finish reason: STOP" {
		t.Fatalf("APIStatusError body = %#v, want persisted finish reason", statusErr.Body)
	}
}

func TestGoogleLLMStreamTreatsEmptyProviderPartAsGeneratedLikeReference(t *testing.T) {
	responses := []*genai.GenerateContentResponse{{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Parts: []*genai.Part{{}},
			},
		}},
	}}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF without false no-response error", err)
	}
}

func TestGoogleLLMStreamKeepsRetryableErrorAfterEmptyPartLikeReference(t *testing.T) {
	responses := []*genai.GenerateContentResponse{{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Parts: []*genai.Part{{}},
			},
		}},
	}}
	readError := false
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) > 0 {
				resp := responses[0]
				responses = responses[1:]
				return resp, nil, true
			}
			if !readError {
				readError = true
				return nil, genai.APIError{Code: 500, Message: "server broke", Status: "INTERNAL"}, true
			}
			return nil, nil, false
		},
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if !statusErr.Retryable {
		t.Fatal("APIStatusError retryable = false, want true before any emitted chat chunk")
	}
}

func TestGoogleLLMStreamReturnsNonRetryableStatusForBlockedFinishReason(t *testing.T) {
	responses := []*genai.GenerateContentResponse{{
		Candidates: []*genai.Candidate{{
			FinishReason: genai.FinishReasonSafety,
		}},
	}}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Message != "generation blocked by gemini: SAFETY" {
		t.Fatalf("APIStatusError message = %q, want blocked finish reason", statusErr.Message)
	}
	if statusErr.Retryable {
		t.Fatal("APIStatusError retryable = true, want false for blocked generation")
	}
	if statusErr.RequestID == "" {
		t.Fatal("APIStatusError request ID empty, want reference stream request ID")
	}
}

func TestGoogleLLMStreamReturnsNonRetryableStatusForPromptFeedback(t *testing.T) {
	responses := []*genai.GenerateContentResponse{{
		PromptFeedback: &genai.GenerateContentResponsePromptFeedback{
			BlockReason:        genai.BlockedReasonSafety,
			BlockReasonMessage: "blocked",
		},
	}}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Message != `{"blockReason":"SAFETY","blockReasonMessage":"blocked"}` {
		t.Fatalf("APIStatusError message = %q, want prompt feedback JSON", statusErr.Message)
	}
	if statusErr.Retryable {
		t.Fatal("APIStatusError retryable = true, want false for prompt feedback")
	}
	if statusErr.RequestID == "" {
		t.Fatal("APIStatusError request ID empty, want reference stream request ID")
	}
}

func TestGoogleLLMStreamMapsProviderAPIError(t *testing.T) {
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			return nil, genai.APIError{Code: 429, Message: "rate limited", Status: "RESOURCE_EXHAUSTED"}, true
		},
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Message != "gemini llm: client error" {
		t.Fatalf("APIStatusError message = %q, want client error", statusErr.Message)
	}
	if statusErr.StatusCode != 429 {
		t.Fatalf("APIStatusError status = %d, want 429", statusErr.StatusCode)
	}
	if statusErr.Body != "rate limited RESOURCE_EXHAUSTED" {
		t.Fatalf("APIStatusError body = %#v, want provider message and status", statusErr.Body)
	}
	if !statusErr.Retryable {
		t.Fatal("APIStatusError retryable = false, want true for 429 client error")
	}
	if statusErr.RequestID == "" {
		t.Fatal("APIStatusError request ID empty, want reference stream request ID")
	}
}

func TestGoogleLLMStreamMapsUnexpectedProviderError(t *testing.T) {
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			return nil, errors.New("dial failed"), true
		},
	}

	chunk, err := stream.Next()

	if chunk != nil {
		t.Fatalf("chunk = %#v, want nil", chunk)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != "gemini llm: error generating content dial failed" {
		t.Fatalf("APIConnectionError message = %q, want provider wrapper", connectionErr.Message)
	}
	if !connectionErr.Retryable {
		t.Fatal("APIConnectionError retryable = false, want true before response output")
	}
}

func TestGoogleLLMStreamReportsCachedPromptTokens(t *testing.T) {
	responses := []*genai.GenerateContentResponse{{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        8,
			CachedContentTokenCount: 3,
			CandidatesTokenCount:    5,
			TotalTokenCount:         13,
		},
	}}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk == nil || chunk.Usage == nil {
		t.Fatalf("chunk = %#v, want usage", chunk)
	}
	if chunk.Usage.PromptTokens != 8 || chunk.Usage.PromptCachedTokens != 3 || chunk.Usage.CompletionTokens != 5 || chunk.Usage.TotalTokens != 13 {
		t.Fatalf("usage = %#v, want cached prompt tokens preserved", chunk.Usage)
	}
}

func TestGoogleLLMStreamEmitsContinuingFunctionCallLikeReference(t *testing.T) {
	continuing := true
	responses := []*genai.GenerateContentResponse{
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{
					Parts: []*genai.Part{{
						FunctionCall: &genai.FunctionCall{
							ID:           "call_lookup",
							Name:         "lookup",
							Args:         map[string]any{"query": "wea"},
							WillContinue: &continuing,
						},
					}},
				},
			}},
		},
		{
			Candidates: []*genai.Candidate{{
				Content: &genai.Content{
					Parts: []*genai.Part{{
						FunctionCall: &genai.FunctionCall{
							ID:   "call_lookup",
							Name: "lookup",
							Args: map[string]any{"query": "weather"},
						},
					}},
				},
			}},
		},
	}
	stream := &googleLLMStream{
		next: func() (*genai.GenerateContentResponse, error, bool) {
			if len(responses) == 0 {
				return nil, nil, false
			}
			resp := responses[0]
			responses = responses[1:]
			return resp, nil, true
		},
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk == nil || chunk.Delta == nil || len(chunk.Delta.ToolCalls) != 1 {
		t.Fatalf("chunk = %#v, want continuing tool call chunk", chunk)
	}
	call := chunk.Delta.ToolCalls[0]
	if call.Arguments != `{"query":"wea"}` {
		t.Fatalf("Arguments = %q, want continuing arguments", call.Arguments)
	}

	chunk, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if chunk == nil || chunk.Delta == nil || len(chunk.Delta.ToolCalls) != 1 {
		t.Fatalf("second chunk = %#v, want final tool call chunk", chunk)
	}
	call = chunk.Delta.ToolCalls[0]
	if call.Arguments != `{"query":"weather"}` {
		t.Fatalf("second Arguments = %q, want final arguments", call.Arguments)
	}
	if len(responses) != 0 {
		t.Fatalf("remaining responses = %d, want all function-call parts emitted", len(responses))
	}
}

func assertGoogleTextPart(t *testing.T, parts []*genai.Part, index int, want string) {
	t.Helper()
	if len(parts) <= index {
		t.Fatalf("len(parts) = %d, want index %d", len(parts), index)
	}
	if parts[index].Text != want {
		t.Fatalf("text part[%d] = %q, want %q", index, parts[index].Text, want)
	}
}

func assertGoogleFunctionCallPart(t *testing.T, parts []*genai.Part, index int, wantID, wantName string) {
	t.Helper()
	if len(parts) <= index {
		t.Fatalf("len(parts) = %d, want index %d", len(parts), index)
	}
	call := parts[index].FunctionCall
	if call == nil {
		t.Fatalf("part[%d] has nil FunctionCall", index)
	}
	if call.ID != wantID {
		t.Fatalf("function call id = %q, want %q", call.ID, wantID)
	}
	if call.Name != wantName {
		t.Fatalf("function call name = %q, want %q", call.Name, wantName)
	}
}

func assertGoogleFunctionResponsePart(t *testing.T, parts []*genai.Part, index int, wantID, wantName, wantOutput string) {
	t.Helper()
	if len(parts) <= index {
		t.Fatalf("len(parts) = %d, want index %d", len(parts), index)
	}
	response := parts[index].FunctionResponse
	if response == nil {
		t.Fatalf("part[%d] has nil FunctionResponse", index)
	}
	if response.ID != wantID {
		t.Fatalf("function response id = %q, want %q", response.ID, wantID)
	}
	if response.Name != wantName {
		t.Fatalf("function response name = %q, want %q", response.Name, wantName)
	}
	if response.Response["output"] != wantOutput {
		t.Fatalf("function response output = %#v, want %q", response.Response["output"], wantOutput)
	}
}
