package openai

import (
	"context"
	"testing"

	"github.com/cavos-io/conversation-worker/core/llm"
	openaisdk "github.com/sashabaranov/go-openai"
)

type requestTestTool struct{}

func (requestTestTool) ID() string          { return "lookup" }
func (requestTestTool) Name() string        { return "lookup" }
func (requestTestTool) Description() string { return "look up information" }
func (requestTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}
func (requestTestTool) Execute(context.Context, string) (string, error) { return "", nil }

func TestBuildOpenAIChatCompletionRequestAppliesExtraParams(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		ParallelToolCalls: true,
		ExtraParams: map[string]any{
			"temperature":           0.7,
			"top_p":                 0.8,
			"presence_penalty":      0.1,
			"frequency_penalty":     0.2,
			"n":                     2,
			"max_completion_tokens": 128,
			"logit_bias":            map[string]any{"42": 7.0},
			"reasoning_effort":      "low",
			"metadata":              map[string]any{"trace": "abc"},
			"seed":                  42,
			"stop":                  []string{"END"},
			"user":                  "caller-123",
			"store":                 true,
			"stream_options":        map[string]any{"include_usage": true},
			"service_tier":          "priority",
			"verbosity":             "low",
			"safety_identifier":     "hashed-user",
			"chat_template_kwargs":  map[string]any{"enable_thinking": false},
			"prediction":            map[string]any{"type": "content", "content": "known prefix"},
		},
	})

	if req.Temperature != 0.7 {
		t.Fatalf("Temperature = %v, want 0.7", req.Temperature)
	}
	if req.TopP != 0.8 {
		t.Fatalf("TopP = %v, want 0.8", req.TopP)
	}
	if req.PresencePenalty != 0.1 {
		t.Fatalf("PresencePenalty = %v, want 0.1", req.PresencePenalty)
	}
	if req.FrequencyPenalty != 0.2 {
		t.Fatalf("FrequencyPenalty = %v, want 0.2", req.FrequencyPenalty)
	}
	if req.N != 2 {
		t.Fatalf("N = %d, want 2", req.N)
	}
	if req.MaxCompletionTokens != 128 {
		t.Fatalf("MaxCompletionTokens = %d, want 128", req.MaxCompletionTokens)
	}
	if req.LogitBias["42"] != 7 {
		t.Fatalf("LogitBias = %#v, want token 42 bias 7", req.LogitBias)
	}
	if req.ReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %q, want low", req.ReasoningEffort)
	}
	if req.Metadata["trace"] != "abc" {
		t.Fatalf("Metadata[trace] = %q, want abc", req.Metadata["trace"])
	}
	if req.Seed == nil || *req.Seed != 42 {
		t.Fatalf("Seed = %#v, want 42", req.Seed)
	}
	if len(req.Stop) != 1 || req.Stop[0] != "END" {
		t.Fatalf("Stop = %#v, want END", req.Stop)
	}
	if req.User != "caller-123" {
		t.Fatalf("User = %q, want caller-123", req.User)
	}
	if !req.Store {
		t.Fatal("Store = false, want true")
	}
	if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
		t.Fatalf("StreamOptions = %#v, want include_usage", req.StreamOptions)
	}
	if req.ServiceTier != openaisdk.ServiceTierPriority {
		t.Fatalf("ServiceTier = %q, want priority", req.ServiceTier)
	}
	if req.Verbosity != "low" {
		t.Fatalf("Verbosity = %q, want low", req.Verbosity)
	}
	if req.SafetyIdentifier != "hashed-user" {
		t.Fatalf("SafetyIdentifier = %q, want hashed-user", req.SafetyIdentifier)
	}
	if enabled, ok := req.ChatTemplateKwargs["enable_thinking"].(bool); !ok || enabled {
		t.Fatalf("ChatTemplateKwargs = %#v, want enable_thinking false", req.ChatTemplateKwargs)
	}
	if req.Prediction == nil || req.Prediction.Type != "content" || req.Prediction.Content != "known prefix" {
		t.Fatalf("Prediction = %#v, want content prediction", req.Prediction)
	}
}

func TestBuildOpenAIChatCompletionRequestMarksToolsStrict(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		Tools: []llm.Tool{requestTestTool{}},
	})

	if len(req.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Function == nil {
		t.Fatalf("tool function is nil")
	}
	if !req.Tools[0].Function.Strict {
		t.Fatalf("tool strict = false, want true")
	}
}

func TestBuildOpenAIChatCompletionRequestMapsNamedToolChoice(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "lookup",
			},
		},
	})

	choice, ok := req.ToolChoice.(openaisdk.ToolChoice)
	if !ok {
		t.Fatalf("ToolChoice = %#v, want openai.ToolChoice", req.ToolChoice)
	}
	if choice.Type != openaisdk.ToolTypeFunction {
		t.Fatalf("ToolChoice.Type = %q, want function", choice.Type)
	}
	if choice.Function.Name != "lookup" {
		t.Fatalf("ToolChoice.Function.Name = %q, want lookup", choice.Function.Name)
	}
}

func TestBuildOpenAIChatCompletionRequestMapsResponseFormat(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
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
					"required":             []string{"summary"},
					"additionalProperties": false,
				},
			},
		},
	})

	if req.ResponseFormat == nil {
		t.Fatal("ResponseFormat = nil, want json_schema response format")
	}
	if req.ResponseFormat.Type != openaisdk.ChatCompletionResponseFormatTypeJSONSchema {
		t.Fatalf("ResponseFormat.Type = %q, want json_schema", req.ResponseFormat.Type)
	}
	if req.ResponseFormat.JSONSchema == nil {
		t.Fatal("ResponseFormat.JSONSchema = nil, want schema")
	}
	if req.ResponseFormat.JSONSchema.Name != "WeatherAnswer" || !req.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("ResponseFormat.JSONSchema = %#v, want strict WeatherAnswer", req.ResponseFormat.JSONSchema)
	}
}
