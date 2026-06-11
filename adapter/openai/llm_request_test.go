package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	openaisdk "github.com/sashabaranov/go-openai"
)

func mustNewOpenAILLMWithConfig(t *testing.T, config openaisdk.ClientConfig, model string) *OpenAILLM {
	t.Helper()
	provider, err := newOpenAILLMWithConfigAndModel(config, model)
	if err != nil {
		t.Fatalf("newOpenAILLMWithConfigAndModel error = %v", err)
	}
	return provider
}

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

type requestOptionalSchemaTool struct{}

func (requestOptionalSchemaTool) ID() string          { return "end_call" }
func (requestOptionalSchemaTool) Name() string        { return "end_call" }
func (requestOptionalSchemaTool) Description() string { return "end call" }
func (requestOptionalSchemaTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"variant": map[string]any{
				"type":        "string",
				"description": "Optional variant name.",
			},
		},
		"required": []string{},
	}
}
func (requestOptionalSchemaTool) Execute(context.Context, string) (string, error) { return "", nil }

type requestUnionSchemaTool struct{}

func (requestUnionSchemaTool) ID() string          { return "lookup" }
func (requestUnionSchemaTool) Name() string        { return "lookup" }
func (requestUnionSchemaTool) Description() string { return "look up information" }
func (requestUnionSchemaTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{},
					map[string]any{"type": "integer"},
				},
			},
		},
		"required": []string{"query"},
	}
}
func (requestUnionSchemaTool) Execute(context.Context, string) (string, error) { return "", nil }

type requestNullableUnionSchemaTool struct{}

func (requestNullableUnionSchemaTool) ID() string          { return "lookup" }
func (requestNullableUnionSchemaTool) Name() string        { return "lookup" }
func (requestNullableUnionSchemaTool) Description() string { return "look up information" }
func (requestNullableUnionSchemaTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mode": map[string]any{
				"anyOf": []any{
					map[string]any{
						"type": "string",
						"enum": []any{"fast", "safe"},
					},
					map[string]any{"type": "null"},
				},
			},
		},
		"required": []string{"mode"},
	}
}
func (requestNullableUnionSchemaTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

type requestDefsSchemaTool struct{}

func (requestDefsSchemaTool) ID() string          { return "lookup" }
func (requestDefsSchemaTool) Name() string        { return "lookup" }
func (requestDefsSchemaTool) Description() string { return "look up information" }
func (requestDefsSchemaTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"payload": map[string]any{"$ref": "#/$defs/payload"},
		},
		"required": []string{"payload"},
		"$defs": map[string]any{
			"payload": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
			},
		},
	}
}
func (requestDefsSchemaTool) Execute(context.Context, string) (string, error) { return "", nil }

type requestRefSiblingSchemaTool struct{}

func (requestRefSiblingSchemaTool) ID() string          { return "lookup" }
func (requestRefSiblingSchemaTool) Name() string        { return "lookup" }
func (requestRefSiblingSchemaTool) Description() string { return "look up information" }
func (requestRefSiblingSchemaTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"payload": map[string]any{
				"$ref":        "#/$defs/payload",
				"description": "caller payload",
			},
		},
		"required": []string{"payload"},
		"$defs": map[string]any{
			"payload": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
			},
		},
	}
}
func (requestRefSiblingSchemaTool) Execute(context.Context, string) (string, error) { return "", nil }

func TestNewOpenAILLMUsesEnvironmentAPIKeyAndReferenceDefaultModel(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "env-key")

	model, err := NewOpenAILLM("", "")
	if err != nil {
		t.Fatalf("NewOpenAILLM error = %v, want env fallback", err)
	}

	if model.Model() != defaultOpenAILLMModel {
		t.Fatalf("Model = %q, want reference default", model.Model())
	}
}

func TestNewOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "")

	_, err := NewOpenAILLM("", "")
	if err == nil {
		t.Fatal("NewOpenAILLM error = nil, want missing API key error")
	}
	if got, want := err.Error(), openAIAPIKeyRequiredMessage; got != want {
		t.Fatalf("NewOpenAILLM error = %q, want %q", got, want)
	}
}

func TestNewAzureOpenAILLMRoutesDeploymentAndKeepsModelMetadata(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewAzureOpenAILLM(
		"gpt-4o",
		"https://resource.openai.azure.com",
		"chat-deployment",
		"2024-06-01",
		"azure-key",
		"",
		withOpenAILLMHTTPClient(capture),
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "gpt-4o" {
		t.Fatalf("Model = %q, want reference model metadata", model.Model())
	}
	if got := model.Provider(); got != "resource.openai.azure.com" {
		t.Fatalf("Provider() = %q, want Azure endpoint host", got)
	}
	if !strings.Contains(capture.requestURL, "/openai/deployments/chat-deployment/chat/completions") {
		t.Fatalf("request URL = %s, want Azure deployment route", capture.requestURL)
	}
	if !strings.Contains(capture.requestURL, "api-version=2024-06-01") {
		t.Fatalf("request URL = %s, want configured api-version", capture.requestURL)
	}
	if capture.apiKey != "azure-key" {
		t.Fatalf("api-key header = %q, want Azure API key", capture.apiKey)
	}
	if capture.authorization != "" {
		t.Fatalf("Authorization = %q, want no bearer token for API-key auth", capture.authorization)
	}
}

func TestNewAzureOpenAILLMFallsBackToReferenceEnvironment(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://env-resource.openai.azure.com")
	t.Setenv("AZURE_OPENAI_API_KEY", "env-azure-key")
	t.Setenv("OPENAI_API_VERSION", "2024-08-01-preview")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewAzureOpenAILLM(
		"",
		"",
		"",
		"",
		"",
		"",
		withOpenAILLMHTTPClient(capture),
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != defaultAzureOpenAILLMModel {
		t.Fatalf("Model = %q, want Azure reference default model", model.Model())
	}
	if !strings.Contains(capture.requestURL, "/openai/deployments/"+defaultAzureOpenAILLMModel+"/chat/completions") {
		t.Fatalf("request URL = %s, want Azure reference default model as deployment", capture.requestURL)
	}
	if strings.Contains(capture.requestURL, "/openai/deployments/"+defaultOpenAILLMModel+"/chat/completions") {
		t.Fatalf("request URL = %s, want Azure reference default not global OpenAI default", capture.requestURL)
	}
	if !strings.Contains(capture.requestURL, "api-version=2024-08-01-preview") {
		t.Fatalf("request URL = %s, want env api-version", capture.requestURL)
	}
	if capture.apiKey != "env-azure-key" {
		t.Fatalf("api-key header = %q, want env Azure API key", capture.apiKey)
	}
}

func TestNewAzureOpenAILLMRequiresEndpoint(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")
	t.Setenv("AZURE_OPENAI_API_KEY", "key")

	_, err := NewAzureOpenAILLM("gpt-4o", "", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "AZURE_OPENAI_ENDPOINT") {
		t.Fatalf("NewAzureOpenAILLM error = %v, want missing endpoint error", err)
	}
}

func TestNewAzureOpenAILLMUsesEntraTokenWhenAPIKeyEmpty(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewAzureOpenAILLM(
		"gpt-4o",
		"https://resource.openai.azure.com",
		"chat-deployment",
		"2024-06-01",
		"",
		"entra-token",
		withOpenAILLMHTTPClient(capture),
	)
	if err != nil {
		t.Fatalf("NewAzureOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if capture.apiKey != "" {
		t.Fatalf("api-key header = %q, want removed for Entra token auth", capture.apiKey)
	}
	if capture.authorization != "Bearer entra-token" {
		t.Fatalf("Authorization = %q, want Entra bearer token", capture.authorization)
	}
}

func TestNewOVHCloudOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(ovhcloudAPIKeyEnv, "env-ovh-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewOVHCloudOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewOVHCloudOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "gpt-oss-120b" {
		t.Fatalf("Model = %q, want gpt-oss-120b", model.Model())
	}
	if model.Provider() != "oai.endpoints.kepler.ai.cloud.ovh.net" {
		t.Fatalf("Provider() = %q, want OVHcloud endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-ovh-key" {
		t.Fatalf("Authorization = %q, want OVHcloud bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v1/chat/completions") {
		t.Fatalf("request URL = %s, want OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"gpt-oss-120b"`) {
		t.Fatalf("request body = %s, want default OVHcloud model", capture.requestBody)
	}
}

func TestNewOVHCloudOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(ovhcloudAPIKeyEnv, "")

	_, err := NewOVHCloudOpenAILLM("", "")
	if err == nil || err.Error() != "OVHcloud AI Endpoints API key is required, either as argument or set OVHCLOUD_API_KEY environmental variable" {
		t.Fatalf("NewOVHCloudOpenAILLM error = %v, want OVHcloud API key required", err)
	}
}

func TestNewDeepSeekOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(deepSeekAPIKeyEnv, "env-deepseek-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewDeepSeekOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewDeepSeekOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "deepseek-chat" {
		t.Fatalf("Model = %q, want deepseek-chat", model.Model())
	}
	if model.Provider() != "api.deepseek.com" {
		t.Fatalf("Provider() = %q, want DeepSeek endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-deepseek-key" {
		t.Fatalf("Authorization = %q, want DeepSeek bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v1/chat/completions") {
		t.Fatalf("request URL = %s, want OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"deepseek-chat"`) {
		t.Fatalf("request body = %s, want default DeepSeek model", capture.requestBody)
	}
}

func TestNewDeepSeekOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(deepSeekAPIKeyEnv, "")

	_, err := NewDeepSeekOpenAILLM("", "")
	if err == nil || err.Error() != "DeepSeek API key is required, either as argument or set DEEPSEEK_API_KEY environmental variable" {
		t.Fatalf("NewDeepSeekOpenAILLM error = %v, want DeepSeek API key required", err)
	}
}

func TestNewFireworksOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(fireworksAPIKeyEnv, "env-fireworks-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewFireworksOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewFireworksOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "accounts/fireworks/models/llama-v3p3-70b-instruct" {
		t.Fatalf("Model = %q, want Fireworks reference model", model.Model())
	}
	if model.Provider() != "api.fireworks.ai" {
		t.Fatalf("Provider() = %q, want Fireworks endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-fireworks-key" {
		t.Fatalf("Authorization = %q, want Fireworks bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/inference/v1/chat/completions") {
		t.Fatalf("request URL = %s, want Fireworks OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"accounts/fireworks/models/llama-v3p3-70b-instruct"`) {
		t.Fatalf("request body = %s, want default Fireworks model", capture.requestBody)
	}
}

func TestNewFireworksOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(fireworksAPIKeyEnv, "")

	_, err := NewFireworksOpenAILLM("", "")
	if err == nil || err.Error() != "fireworks API key is required, either as argument or set FIREWORKS_API_KEY environmental variable" {
		t.Fatalf("NewFireworksOpenAILLM error = %v, want Fireworks API key required", err)
	}
}

func TestNewPerplexityOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(perplexityAPIKeyEnv, "env-perplexity-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewPerplexityOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewPerplexityOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "llama-3.1-sonar-small-128k-chat" {
		t.Fatalf("Model = %q, want Perplexity reference model", model.Model())
	}
	if model.Provider() != "api.perplexity.ai" {
		t.Fatalf("Provider() = %q, want Perplexity endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-perplexity-key" {
		t.Fatalf("Authorization = %q, want Perplexity bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/chat/completions") {
		t.Fatalf("request URL = %s, want Perplexity OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"llama-3.1-sonar-small-128k-chat"`) {
		t.Fatalf("request body = %s, want default Perplexity model", capture.requestBody)
	}
}

func TestNewPerplexityOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(perplexityAPIKeyEnv, "")

	_, err := NewPerplexityOpenAILLM("", "")
	if err == nil || err.Error() != "perplexity AI API key is required, either as argument or set PERPLEXITY_API_KEY environmental variable" {
		t.Fatalf("NewPerplexityOpenAILLM error = %v, want Perplexity API key required", err)
	}
}

func TestNewTogetherOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(togetherAPIKeyEnv, "env-together-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewTogetherOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewTogetherOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo" {
		t.Fatalf("Model = %q, want Together reference model", model.Model())
	}
	if model.Provider() != "api.together.xyz" {
		t.Fatalf("Provider() = %q, want Together endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-together-key" {
		t.Fatalf("Authorization = %q, want Together bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v1/chat/completions") {
		t.Fatalf("request URL = %s, want Together OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo"`) {
		t.Fatalf("request body = %s, want default Together model", capture.requestBody)
	}
}

func TestNewTogetherOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(togetherAPIKeyEnv, "")

	_, err := NewTogetherOpenAILLM("", "")
	if err == nil || err.Error() != "together AI API key is required, either as argument or set TOGETHER_API_KEY environmental variable" {
		t.Fatalf("NewTogetherOpenAILLM error = %v, want Together API key required", err)
	}
}

func TestNewTelnyxOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(telnyxAPIKeyEnv, "env-telnyx-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewTelnyxOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewTelnyxOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "meta-llama/Meta-Llama-3.1-70B-Instruct" {
		t.Fatalf("Model = %q, want Telnyx reference model", model.Model())
	}
	if model.Provider() != "api.telnyx.com" {
		t.Fatalf("Provider() = %q, want Telnyx endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-telnyx-key" {
		t.Fatalf("Authorization = %q, want Telnyx bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v2/ai/chat/completions") {
		t.Fatalf("request URL = %s, want Telnyx OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"meta-llama/Meta-Llama-3.1-70B-Instruct"`) {
		t.Fatalf("request body = %s, want default Telnyx model", capture.requestBody)
	}
}

func TestNewTelnyxOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(telnyxAPIKeyEnv, "")

	_, err := NewTelnyxOpenAILLM("", "")
	if err == nil || err.Error() != "telnyx AI API key is required, either as argument or set TELNYX_API_KEY environmental variable" {
		t.Fatalf("NewTelnyxOpenAILLM error = %v, want Telnyx API key required", err)
	}
}

func TestNewNebiusOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(nebiusAPIKeyEnv, "env-nebius-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewNebiusOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewNebiusOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "meta-llama/Meta-Llama-3.1-70B-Instruct" {
		t.Fatalf("Model = %q, want Nebius reference model", model.Model())
	}
	if model.Provider() != "api.studio.nebius.com" {
		t.Fatalf("Provider() = %q, want Nebius endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-nebius-key" {
		t.Fatalf("Authorization = %q, want Nebius bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v1/chat/completions") {
		t.Fatalf("request URL = %s, want Nebius OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"meta-llama/Meta-Llama-3.1-70B-Instruct"`) {
		t.Fatalf("request body = %s, want default Nebius model", capture.requestBody)
	}
}

func TestNewNebiusOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(nebiusAPIKeyEnv, "")

	_, err := NewNebiusOpenAILLM("", "")
	if err == nil || err.Error() != "nebius API key is required, either as argument or set NEBIUS_API_KEY environmental variable" {
		t.Fatalf("NewNebiusOpenAILLM error = %v, want Nebius API key required", err)
	}
}

func TestNewLettaOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(lettaAPIKeyEnv, "env-letta-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewLettaOpenAILLM("agent-123", "", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewLettaOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "agent-123" {
		t.Fatalf("Model = %q, want Letta agent id", model.Model())
	}
	if model.Provider() != "api.letta.com" {
		t.Fatalf("Provider() = %q, want Letta endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-letta-key" {
		t.Fatalf("Authorization = %q, want Letta bearer key", capture.authorization)
	}
	if capture.requestURL != "https://api.letta.com/v1/chat/completions" {
		t.Fatalf("request URL = %s, want Letta chat completions endpoint without duplicated path", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"agent-123"`) {
		t.Fatalf("request body = %s, want Letta agent id as model", capture.requestBody)
	}
}

func TestNewLettaOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(lettaAPIKeyEnv, "")

	_, err := NewLettaOpenAILLM("agent-123", "", "")
	if err == nil || err.Error() != "letta API key is required, either as argument or set LETTA_API_KEY environmental variable" {
		t.Fatalf("NewLettaOpenAILLM error = %v, want Letta API key required", err)
	}
}

func TestNewLettaOpenAILLMValidatesBaseURL(t *testing.T) {
	t.Setenv(lettaAPIKeyEnv, "env-letta-key")

	_, err := NewLettaOpenAILLM("agent-123", "ftp://api.letta.com/v1/chat/completions", "")
	if err == nil || err.Error() != "invalid URL scheme: \"ftp\"; must be \"http\" or \"https\"" {
		t.Fatalf("NewLettaOpenAILLM invalid scheme error = %v", err)
	}

	_, err = NewLettaOpenAILLM("agent-123", "https:///v1/chat/completions", "")
	if err == nil || err.Error() != "URL \"https:///v1/chat/completions\" is missing a network location (e.g., domain name)" {
		t.Fatalf("NewLettaOpenAILLM missing host error = %v", err)
	}
}

func TestNewOllamaOpenAILLMDefaultsMatchReference(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model := NewOllamaOpenAILLM("", withOpenAILLMHTTPClient(capture))

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "llama3.1" {
		t.Fatalf("Model = %q, want llama3.1", model.Model())
	}
	if model.Provider() != "localhost:11434" {
		t.Fatalf("Provider() = %q, want local Ollama endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer ollama" {
		t.Fatalf("Authorization = %q, want reference ollama API key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "http://localhost:11434/v1/chat/completions") {
		t.Fatalf("request URL = %s, want local Ollama chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"llama3.1"`) {
		t.Fatalf("request body = %s, want default Ollama model", capture.requestBody)
	}
}

func TestNewCometAPIOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(cometAPIKeyEnv, "env-comet-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewCometAPIOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewCometAPIOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "gpt-5-chat-latest" {
		t.Fatalf("Model = %q, want gpt-5-chat-latest", model.Model())
	}
	if model.Provider() != "api.cometapi.com" {
		t.Fatalf("Provider() = %q, want CometAPI endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-comet-key" {
		t.Fatalf("Authorization = %q, want CometAPI bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v1/chat/completions") {
		t.Fatalf("request URL = %s, want OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"gpt-5-chat-latest"`) {
		t.Fatalf("request body = %s, want default CometAPI model", capture.requestBody)
	}
}

func TestNewCometAPIOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(cometAPIKeyEnv, "")

	_, err := NewCometAPIOpenAILLM("", "")
	if err == nil || err.Error() != "CometAPI API key is required, either as argument or set COMETAPI_API_KEY environmental variable" {
		t.Fatalf("NewCometAPIOpenAILLM error = %v, want CometAPI API key required", err)
	}
}

func TestNewOctoAIOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(octoAIAPIKeyEnv, "env-octo-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewOctoAIOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewOctoAIOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "llama-2-13b-chat" {
		t.Fatalf("Model = %q, want llama-2-13b-chat", model.Model())
	}
	if model.Provider() != "text.octoai.run" {
		t.Fatalf("Provider() = %q, want OctoAI endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-octo-key" {
		t.Fatalf("Authorization = %q, want OctoAI bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v1/chat/completions") {
		t.Fatalf("request URL = %s, want OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"llama-2-13b-chat"`) {
		t.Fatalf("request body = %s, want default OctoAI model", capture.requestBody)
	}
}

func TestNewOctoAIOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(octoAIAPIKeyEnv, "")

	_, err := NewOctoAIOpenAILLM("", "")
	if err == nil || err.Error() != "OctoAI API key is required, either as argument or set OCTOAI_TOKEN environmental variable" {
		t.Fatalf("NewOctoAIOpenAILLM error = %v, want OctoAI API key required", err)
	}
}

func TestNewSambaNovaOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(sambaNovaAPIKeyEnv, "env-sambanova-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewSambaNovaOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewSambaNovaOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "DeepSeek-R1-0528" {
		t.Fatalf("Model = %q, want DeepSeek-R1-0528", model.Model())
	}
	if model.Provider() != "api.sambanova.ai" {
		t.Fatalf("Provider() = %q, want SambaNova endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-sambanova-key" {
		t.Fatalf("Authorization = %q, want SambaNova bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v1/chat/completions") {
		t.Fatalf("request URL = %s, want OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"DeepSeek-R1-0528"`) {
		t.Fatalf("request body = %s, want default SambaNova model", capture.requestBody)
	}
}

func TestNewSambaNovaOpenAILLMOmitsStrictToolSchema(t *testing.T) {
	t.Setenv(sambaNovaAPIKeyEnv, "env-sambanova-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model, err := NewSambaNovaOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewSambaNovaOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithTools([]llm.Tool{requestTestTool{}}),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var body map[string]any
	if err := json.Unmarshal([]byte(capture.requestBody), &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	tools := body["tools"].([]any)
	function := tools[0].(map[string]any)["function"].(map[string]any)
	if _, ok := function["strict"]; ok {
		t.Fatalf("strict = %#v, want omitted for SambaNova legacy tool schema; body %s", function["strict"], capture.requestBody)
	}
}

func TestNewSambaNovaOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(sambaNovaAPIKeyEnv, "")

	_, err := NewSambaNovaOpenAILLM("", "")
	if err == nil || err.Error() != "SambaNova API key is required, either as argument or set SAMBANOVA_API_KEY environment variable" {
		t.Fatalf("NewSambaNovaOpenAILLM error = %v, want SambaNova API key required", err)
	}
}

func TestNewCerebrasOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(cerebrasAPIKeyEnv, "env-cerebras-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewCerebrasOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewCerebrasOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "llama-4-scout-17b-16e-instruct" {
		t.Fatalf("Model = %q, want llama-4-scout-17b-16e-instruct", model.Model())
	}
	if model.Provider() != "api.cerebras.ai" {
		t.Fatalf("Provider() = %q, want Cerebras endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-cerebras-key" {
		t.Fatalf("Authorization = %q, want Cerebras bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v1/chat/completions") {
		t.Fatalf("request URL = %s, want OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"llama-4-scout-17b-16e-instruct"`) {
		t.Fatalf("request body = %s, want default Cerebras model", capture.requestBody)
	}
}

func TestNewCerebrasOpenAILLMOmitsStrictToolSchema(t *testing.T) {
	t.Setenv(cerebrasAPIKeyEnv, "env-cerebras-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model, err := NewCerebrasOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewCerebrasOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithTools([]llm.Tool{requestTestTool{}}),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var body map[string]any
	if err := json.Unmarshal([]byte(capture.requestBody), &body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	tools := body["tools"].([]any)
	function := tools[0].(map[string]any)["function"].(map[string]any)
	if _, ok := function["strict"]; ok {
		t.Fatalf("strict = %#v, want omitted for Cerebras legacy tool schema; body %s", function["strict"], capture.requestBody)
	}
}

func TestNewCerebrasOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(cerebrasAPIKeyEnv, "")

	_, err := NewCerebrasOpenAILLM("", "")
	if err == nil || err.Error() != "cerebras API key is required, either as argument or set CEREBRAS_API_KEY environment variable" {
		t.Fatalf("NewCerebrasOpenAILLM error = %v, want Cerebras API key required", err)
	}
}

func TestNewXAIOpenAILLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(xAIAPIKeyEnv, "env-xai-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	model, err := NewXAIOpenAILLM("", "", withOpenAILLMHTTPClient(capture))
	if err != nil {
		t.Fatalf("NewXAIOpenAILLM error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if model.Model() != "grok-3-fast" {
		t.Fatalf("Model = %q, want grok-3-fast", model.Model())
	}
	if model.Provider() != "api.x.ai" {
		t.Fatalf("Provider() = %q, want xAI endpoint host", model.Provider())
	}
	if capture.authorization != "Bearer env-xai-key" {
		t.Fatalf("Authorization = %q, want xAI bearer key", capture.authorization)
	}
	if !strings.Contains(capture.requestURL, "/v1/chat/completions") {
		t.Fatalf("request URL = %s, want OpenAI-compatible chat completions route", capture.requestURL)
	}
	if !strings.Contains(capture.requestBody, `"model":"grok-3-fast"`) {
		t.Fatalf("request body = %s, want default xAI model", capture.requestBody)
	}
}

func TestNewXAIOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(xAIAPIKeyEnv, "")

	_, err := NewXAIOpenAILLM("", "")
	if err == nil || err.Error() != "XAI API key is required, either as argument or set XAI_API_KEY environmental variable" {
		t.Fatalf("NewXAIOpenAILLM error = %v, want xAI API key required", err)
	}
}

func TestNewOpenAILLMChatUsesConfiguredKeyAndDefaultModel(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "env-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	config := openaisdk.DefaultConfig("env-key")
	config.HTTPClient = capture

	model, err := newOpenAILLMWithConfigAndModel(config, "")
	if err != nil {
		t.Fatalf("newOpenAILLMWithConfigAndModel error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if capture.requestBody == "" || !strings.Contains(capture.requestBody, `"model":"gpt-4.1"`) {
		t.Fatalf("request body = %s, want default model", capture.requestBody)
	}
	if capture.authorization != "Bearer env-key" {
		t.Fatalf("Authorization = %q, want bearer env key", capture.authorization)
	}
}

func TestOpenAIChatOmitsDefaultParallelToolCalls(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if strings.Contains(capture.requestBody, `"parallel_tool_calls"`) {
		t.Fatalf("request body = %s, want default parallel_tool_calls omitted", capture.requestBody)
	}
}

func TestOpenAIChatSerializesExplicitParallelToolCalls(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, _ = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithParallelToolCalls(false),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	if !strings.Contains(capture.requestBody, `"parallel_tool_calls":false`) {
		t.Fatalf("request body = %s, want explicit parallel_tool_calls false", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderParallelToolCalls(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMParallelToolCalls(false),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"parallel_tool_calls":false`) {
		t.Fatalf("request body = %s, want provider parallel_tool_calls false", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderToolChoice(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMToolChoice("none"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"tool_choice":"none"`) {
		t.Fatalf("request body = %s, want provider tool_choice none", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderTemperature(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMTemperature(0.3),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"temperature":0.3`) {
		t.Fatalf("request body = %s, want provider temperature", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderTopP(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMTopP(0.4),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"top_p":0.4`) {
		t.Fatalf("request body = %s, want provider top_p", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderMaxCompletionTokens(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMMaxCompletionTokens(256),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"max_completion_tokens":256`) {
		t.Fatalf("request body = %s, want provider max_completion_tokens", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderStore(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMStore(true),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"store":true`) {
		t.Fatalf("request body = %s, want provider store true", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderServiceTier(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMServiceTier("priority"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"service_tier":"priority"`) {
		t.Fatalf("request body = %s, want provider service_tier", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderSafetyIdentifier(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMSafetyIdentifier("hashed-user"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"safety_identifier":"hashed-user"`) {
		t.Fatalf("request body = %s, want provider safety_identifier", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderExtraHeaders(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMExtraHeaders(map[string]string{
			"X-Request-Group": "gold",
		}),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if got := capture.header.Get("X-Request-Group"); got != "gold" {
		t.Fatalf("X-Request-Group = %q, want provider extra header", got)
	}
}

func TestOpenAIChatAppliesProviderExtraQuery(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMExtraQuery(map[string]string{
			"api-version": "preview",
		}),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	requestURL, err := url.Parse(capture.requestURL)
	if err != nil {
		t.Fatalf("request URL parse error = %v", err)
	}
	if got := requestURL.Query().Get("api-version"); got != "preview" {
		t.Fatalf("api-version query = %q, want provider extra query", got)
	}
}

func TestOpenAIChatAppliesProviderExtraBody(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMExtraBody(map[string]any{
			"prompt_cache_key": "room-123",
		}),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"prompt_cache_key":"room-123"`) {
		t.Fatalf("request body = %s, want provider extra body field", capture.requestBody)
	}
	if strings.Contains(capture.requestBody, "extra_body") {
		t.Fatalf("request body = %s, want extra body merged into request", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderPromptCacheOptions(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMPromptCacheKey("room-123"),
		WithOpenAILLMPromptCacheRetention("24h"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"prompt_cache_key":"room-123"`) {
		t.Fatalf("request body = %s, want provider prompt_cache_key", capture.requestBody)
	}
	if !strings.Contains(capture.requestBody, `"prompt_cache_retention":"24h"`) {
		t.Fatalf("request body = %s, want provider prompt_cache_retention", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderUser(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMUser("caller-123"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"user":"caller-123"`) {
		t.Fatalf("request body = %s, want provider user", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderMetadata(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMMetadata(map[string]string{"trace": "abc"}),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"metadata":{"trace":"abc"}`) {
		t.Fatalf("request body = %s, want provider metadata", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderVerbosity(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-5",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMVerbosity("low"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"verbosity":"low"`) {
		t.Fatalf("request body = %s, want provider verbosity", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderReasoningEffort(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-5",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMReasoningEffort("low"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"reasoning_effort":"low"`) {
		t.Fatalf("request body = %s, want provider reasoning_effort", capture.requestBody)
	}
}

func TestOpenAIChatAppliesConnectOptionsTimeoutToRequestContext(t *testing.T) {
	sentinelErr := errors.New("stop after context capture")
	capture := &captureDeadlineHTTPClient{err: sentinelErr}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: 75 * time.Millisecond}),
	)

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Chat error = %T %v, want APIConnectionError", err, err)
	}
	if !capture.hasDeadline {
		t.Fatal("request context has no deadline, want connect options timeout deadline")
	}
	if capture.remaining <= 0 || capture.remaining > 75*time.Millisecond {
		t.Fatalf("request context deadline remaining = %v, want bounded by connect timeout", capture.remaining)
	}
}

func TestOpenAIChatAppliesDefaultConnectOptionsTimeoutToRequestContext(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(context.Background(), llm.NewChatContext())

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if !capture.hasDeadline {
		t.Fatal("request context has no deadline, want default connect timeout deadline")
	}
	if capture.remaining <= 0 || capture.remaining > llm.DefaultAPIConnectOptions().Timeout {
		t.Fatalf("request context deadline remaining = %v, want bounded by default connect timeout", capture.remaining)
	}
}

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

func TestBuildOpenAIChatCompletionRequestAppliesExtraParamParallelToolCalls(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		ExtraParams: map[string]any{
			"parallel_tool_calls": false,
		},
	})

	if got, ok := req.ParallelToolCalls.(*bool); !ok || got == nil || *got {
		t.Fatalf("ParallelToolCalls = %#v, want pointer to false", req.ParallelToolCalls)
	}
}

func TestBuildOpenAIChatCompletionRequestAppliesExtraParamToolChoice(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		ExtraParams: map[string]any{
			"tool_choice": "none",
		},
	})

	if req.ToolChoice != "none" {
		t.Fatalf("ToolChoice = %#v, want none", req.ToolChoice)
	}
}

func TestNewOpenAILLMWithBaseURLAndHTTPClientUsesConfiguredClient(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient("test-key", "gpt-4o", "https://openai.test/v1", capture)

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if capture.authorization != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want Bearer test-key", capture.authorization)
	}
}

func TestNewOpenRouterLLMMatchesReferenceHeadersAndBody(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model, err := NewOpenRouterLLMWithHTTPClient(
		"test-key",
		"openai/gpt-4o-mini",
		capture,
		WithOpenRouterSiteURL("https://app.example"),
		WithOpenRouterAppName("Cavos Agent"),
		WithOpenRouterFallbackModels([]string{"anthropic/claude-3.5-sonnet"}),
		WithOpenRouterProvider(map[string]any{"order": []any{"OpenAI", "Anthropic"}}),
		WithOpenRouterPlugins([]map[string]any{{"id": "web", "max_results": 3}}),
	)
	if err != nil {
		t.Fatalf("NewOpenRouterLLMWithHTTPClient error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if got := capture.header.Get("HTTP-Referer"); got != "https://app.example" {
		t.Fatalf("HTTP-Referer = %q, want site URL", got)
	}
	if got := capture.header.Get("X-Title"); got != "Cavos Agent" {
		t.Fatalf("X-Title = %q, want app name", got)
	}
	if !strings.HasPrefix(capture.requestURL, "https://openrouter.ai/api/v1/chat/completions") {
		t.Fatalf("request URL = %s, want OpenRouter chat completions endpoint", capture.requestURL)
	}
	for _, want := range []string{
		`"tool_choice":"auto"`,
		`"models":["openai/gpt-4o-mini","anthropic/claude-3.5-sonnet"]`,
		`"provider":{"order":["OpenAI","Anthropic"]}`,
		`"plugins":[{"id":"web","max_results":3}]`,
	} {
		if !strings.Contains(capture.requestBody, want) {
			t.Fatalf("request body = %s, want %s", capture.requestBody, want)
		}
	}
}

func TestNewOpenRouterLLMUsesEnvironmentKeyAndForwardedOptions(t *testing.T) {
	t.Setenv(openRouterAPIKeyEnv, "env-key")
	model, err := NewOpenRouterLLM(
		"",
		"",
		WithOpenRouterLLMOptions(
			WithOpenAILLMTemperature(0.2),
			WithOpenAILLMUser("caller-123"),
		),
	)
	if err != nil {
		t.Fatalf("NewOpenRouterLLM error = %v", err)
	}

	if model.Model() != "auto" {
		t.Fatalf("Model = %q, want reference auto default", model.Model())
	}
	if model.Provider() != "openrouter.ai" {
		t.Fatalf("Provider = %q, want OpenRouter host", model.Provider())
	}

	req := buildOpenAIChatCompletionRequest("auto", llm.NewChatContext(), &llm.ChatOptions{
		ExtraParams: model.extraParams,
		ToolChoice:  model.toolChoice,
	})
	if req.ToolChoice != "auto" {
		t.Fatalf("ToolChoice = %#v, want reference auto default", req.ToolChoice)
	}
	if req.Temperature != 0.2 {
		t.Fatalf("Temperature = %v, want forwarded option", req.Temperature)
	}
	if req.User != "caller-123" {
		t.Fatalf("User = %q, want forwarded option", req.User)
	}
}

type captureDeadlineHTTPClient struct {
	err               error
	hasDeadline       bool
	remaining         time.Duration
	statusCode        int
	responseBody      string
	header            http.Header
	requestURL        string
	requestBody       string
	authorization     string
	apiKey            string
	userAgent         string
	roomID            string
	jobID             string
	inferenceProvider string
	inferencePriority string
}

func (c *captureDeadlineHTTPClient) Do(req *http.Request) (*http.Response, error) {
	deadline, ok := req.Context().Deadline()
	c.hasDeadline = ok
	if ok {
		c.remaining = time.Until(deadline)
	}
	c.header = req.Header.Clone()
	c.requestURL = req.URL.String()
	c.authorization = req.Header.Get("Authorization")
	c.apiKey = req.Header.Get(openaisdk.AzureAPIKeyHeader)
	c.userAgent = req.Header.Get("User-Agent")
	c.roomID = req.Header.Get("X-LiveKit-Room-ID")
	c.jobID = req.Header.Get("X-LiveKit-Job-ID")
	c.inferenceProvider = req.Header.Get("X-LiveKit-Inference-Provider")
	c.inferencePriority = req.Header.Get("X-LiveKit-Inference-Priority")
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		c.requestBody = string(body)
		req.Body = io.NopCloser(strings.NewReader(c.requestBody))
	}
	if c.err != nil {
		return nil, c.err
	}
	statusCode := c.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	header := c.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Body:       io.NopCloser(strings.NewReader(c.responseBody)),
		Header:     header,
		Request:    req,
	}, nil
}

func TestOpenAIChatReturnsAPIStatusErrorOnHTTPError(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusTooManyRequests,
		responseBody: `{"error":{"message":"rate limit","type":"rate_limit","code":"rate_limit"}}`,
	}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Message != "rate limit (429 Too Many Requests)" {
		t.Fatalf("Message = %q, want formatted rate limit message", statusErr.Message)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want 429", statusErr.StatusCode)
	}
	if !statusErr.Retryable {
		t.Fatal("Retryable = false, want 429 retryable")
	}
}

func TestOpenAIChatReturnsAPITimeoutErrorOnTransportDeadline(t *testing.T) {
	capture := &captureDeadlineHTTPClient{err: context.DeadlineExceeded}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Chat error = %T %v, want APITimeoutError", err, err)
	}
	if timeoutErr.Message != "Request timed out." {
		t.Fatalf("Message = %q, want default timeout message", timeoutErr.Message)
	}
	if !timeoutErr.Retryable {
		t.Fatal("Retryable = false, want timeout errors retryable")
	}
}

func TestOpenAIChatRetriesRetryableSetupAPIError(t *testing.T) {
	capture := &sequenceHTTPClient{responses: []*http.Response{
		openAITestResponse(http.StatusTooManyRequests, `{"error":{"message":"rate limit","type":"rate_limit","code":"rate_limit"}}`),
		openAITestResponse(http.StatusOK, "data: [DONE]\n\n"),
	}}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v, want retry success", err)
	}
	_ = stream.Close()
	if capture.calls != 2 {
		t.Fatalf("HTTP calls = %d, want initial failure plus retry", capture.calls)
	}
}

func TestOpenAIChatDoesNotRetryNonRetryableSetupAPIError(t *testing.T) {
	capture := &sequenceHTTPClient{responses: []*http.Response{
		openAITestResponse(http.StatusBadRequest, `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`),
		openAITestResponse(http.StatusOK, "data: [DONE]\n\n"),
	}}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if capture.calls != 1 {
		t.Fatalf("HTTP calls = %d, want no retry for non-retryable status", capture.calls)
	}
}

func TestOpenAIStreamReturnsAPIErrorOnErrorEvent(t *testing.T) {
	capture := &sequenceHTTPClient{responses: []*http.Response{
		openAITestResponse(http.StatusOK, `data: {"error":{"message":"stream failed","type":"server_error","code":"server_error"}}`+"\n\n"),
	}}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()

	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIError", err, err)
	}
	if apiErr.Message != "stream failed" {
		t.Fatalf("Message = %q, want OpenAI stream error message", apiErr.Message)
	}
	body, ok := apiErr.Body.(*openaisdk.APIError)
	if !ok {
		t.Fatalf("Body = %T %#v, want OpenAI APIError body", apiErr.Body, apiErr.Body)
	}
	if body.Type != "server_error" || body.Code != "server_error" {
		t.Fatalf("Body = %#v, want server_error metadata", body)
	}
	if !apiErr.Retryable {
		t.Fatal("Retryable = false, want stream API errors retryable")
	}
	var connectionErr *llm.APIConnectionError
	if errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIError not APIConnectionError", err, err)
	}
}

type sequenceHTTPClient struct {
	responses []*http.Response
	calls     int
}

func (c *sequenceHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c.calls >= len(c.responses) {
		return nil, errors.New("unexpected HTTP call")
	}
	resp := c.responses[c.calls]
	resp.Request = req
	c.calls++
	return resp, nil
}

func openAITestResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
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

func TestBuildOpenAIChatCompletionRequestNormalizesStrictToolSchema(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4.1-mini", llm.NewChatContext(), &llm.ChatOptions{
		Tools: []llm.Tool{requestOptionalSchemaTool{}},
	})

	if len(req.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Function == nil {
		t.Fatal("tool function is nil")
	}
	if !req.Tools[0].Function.Strict {
		t.Fatalf("tool strict = false, want true")
	}

	paramsJSON, err := json.Marshal(req.Tools[0].Function.Parameters)
	if err != nil {
		t.Fatalf("tool parameters marshal error = %v", err)
	}
	var params map[string]any
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("tool parameters json error = %v", err)
	}

	if got := params["additionalProperties"]; got != false {
		t.Fatalf("additionalProperties = %#v, want false", got)
	}

	required, ok := params["required"].([]any)
	if !ok {
		t.Fatalf("required = %#v, want JSON array", params["required"])
	}
	if len(required) != 1 || required[0] != "variant" {
		t.Fatalf("required = %#v, want [variant]", required)
	}

	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want object", params["properties"])
	}
	variant, ok := properties["variant"].(map[string]any)
	if !ok {
		t.Fatalf("properties.variant = %#v, want object", properties["variant"])
	}
	types, ok := variant["type"].([]any)
	if !ok {
		t.Fatalf("properties.variant.type = %#v, want array", variant["type"])
	}
	if len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Fatalf("properties.variant.type = %#v, want [string null]", types)
	}
}

func TestBuildOpenAIChatCompletionRequestNormalizesStrictToolSchemaUnions(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4.1-mini", llm.NewChatContext(), &llm.ChatOptions{
		Tools: []llm.Tool{requestUnionSchemaTool{}},
	})

	paramsJSON, err := json.Marshal(req.Tools[0].Function.Parameters)
	if err != nil {
		t.Fatalf("tool parameters marshal error = %v", err)
	}
	var params map[string]any
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("tool parameters json error = %v", err)
	}

	properties := params["properties"].(map[string]any)
	query := properties["query"].(map[string]any)
	if _, ok := query["oneOf"]; ok {
		t.Fatalf("query.oneOf = %#v, want converted away for strict OpenAI schema", query["oneOf"])
	}
	anyOf, ok := query["anyOf"].([]any)
	if !ok {
		t.Fatalf("query.anyOf = %#v, want anyOf array", query["anyOf"])
	}
	if len(anyOf) != 2 {
		t.Fatalf("query.anyOf = %#v, want two non-empty variants", anyOf)
	}
}

func TestBuildOpenAIChatCompletionRequestNormalizesStrictToolSchemaNullableUnion(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4.1-mini", llm.NewChatContext(), &llm.ChatOptions{
		Tools: []llm.Tool{requestNullableUnionSchemaTool{}},
	})

	paramsJSON, err := json.Marshal(req.Tools[0].Function.Parameters)
	if err != nil {
		t.Fatalf("tool parameters marshal error = %v", err)
	}
	var params map[string]any
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("tool parameters json error = %v", err)
	}

	properties := params["properties"].(map[string]any)
	mode := properties["mode"].(map[string]any)
	if _, ok := mode["anyOf"]; ok {
		t.Fatalf("mode.anyOf = %#v, want collapsed nullable type", mode["anyOf"])
	}
	types, ok := mode["type"].([]any)
	if !ok || len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Fatalf("mode.type = %#v, want [string null]", mode["type"])
	}
	enum, ok := mode["enum"].([]any)
	if !ok || len(enum) != 3 || enum[0] != "fast" || enum[1] != "safe" || enum[2] != nil {
		t.Fatalf("mode.enum = %#v, want [fast safe nil]", mode["enum"])
	}
}

func TestBuildOpenAIChatCompletionRequestNormalizesStrictToolSchemaDefs(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4.1-mini", llm.NewChatContext(), &llm.ChatOptions{
		Tools: []llm.Tool{requestDefsSchemaTool{}},
	})

	paramsJSON, err := json.Marshal(req.Tools[0].Function.Parameters)
	if err != nil {
		t.Fatalf("tool parameters marshal error = %v", err)
	}
	var params map[string]any
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("tool parameters json error = %v", err)
	}

	defs := params["$defs"].(map[string]any)
	payload := defs["payload"].(map[string]any)
	if got := payload["additionalProperties"]; got != false {
		t.Fatalf("$defs.payload.additionalProperties = %#v, want false", got)
	}
	required, ok := payload["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "query" {
		t.Fatalf("$defs.payload.required = %#v, want [query]", payload["required"])
	}
}

func TestBuildOpenAIChatCompletionRequestNormalizesStrictToolSchemaRefSiblings(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4.1-mini", llm.NewChatContext(), &llm.ChatOptions{
		Tools: []llm.Tool{requestRefSiblingSchemaTool{}},
	})

	paramsJSON, err := json.Marshal(req.Tools[0].Function.Parameters)
	if err != nil {
		t.Fatalf("tool parameters marshal error = %v", err)
	}
	var params map[string]any
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("tool parameters json error = %v", err)
	}

	properties := params["properties"].(map[string]any)
	payload := properties["payload"].(map[string]any)
	if _, ok := payload["$ref"]; ok {
		t.Fatalf("payload.$ref = %#v, want inlined strict schema", payload["$ref"])
	}
	if got := payload["description"]; got != "caller payload" {
		t.Fatalf("payload.description = %#v, want caller payload", got)
	}
	if got := payload["additionalProperties"]; got != false {
		t.Fatalf("payload.additionalProperties = %#v, want false", got)
	}
	required, ok := payload["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "query" {
		t.Fatalf("payload.required = %#v, want [query]", payload["required"])
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

func TestBuildOpenAIChatCompletionRequestAppliesExtraParamResponseFormat(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		ExtraParams: map[string]any{
			"response_format": map[string]any{
				"type": "json_object",
			},
		},
	})

	if req.ResponseFormat == nil {
		t.Fatal("ResponseFormat = nil, want json_object response format")
	}
	if req.ResponseFormat.Type != openaisdk.ChatCompletionResponseFormatTypeJSONObject {
		t.Fatalf("ResponseFormat.Type = %q, want json_object", req.ResponseFormat.Type)
	}
}

func TestBuildOpenAIChatCompletionRequestDropsUnsupportedReasoningParams(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("openai/gpt-5", llm.NewChatContext(), &llm.ChatOptions{
		ParallelToolCalls: true,
		ExtraParams: map[string]any{
			"temperature":           0.7,
			"top_p":                 0.8,
			"presence_penalty":      0.1,
			"frequency_penalty":     0.2,
			"n":                     2,
			"logit_bias":            map[string]any{"42": 7.0},
			"logprobs":              true,
			"top_logprobs":          3,
			"reasoning_effort":      "low",
			"max_completion_tokens": 128,
			"service_tier":          "priority",
			"stop":                  []string{"END"},
		},
	})

	if req.Temperature != 0 || req.TopP != 0 || req.PresencePenalty != 0 || req.FrequencyPenalty != 0 {
		t.Fatalf("sampling params = %v/%v/%v/%v, want dropped zero values", req.Temperature, req.TopP, req.PresencePenalty, req.FrequencyPenalty)
	}
	if req.N != 0 || req.LogitBias != nil || req.LogProbs || req.TopLogProbs != 0 {
		t.Fatalf("unsupported params not dropped: N=%d LogitBias=%#v LogProbs=%v TopLogProbs=%d", req.N, req.LogitBias, req.LogProbs, req.TopLogProbs)
	}
	if req.ReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %q, want preserved for reasoning model without tools", req.ReasoningEffort)
	}
	if req.MaxCompletionTokens != 128 || req.ServiceTier != openaisdk.ServiceTierPriority || len(req.Stop) != 1 || req.Stop[0] != "END" {
		t.Fatalf("supported params = max_completion_tokens %d service_tier %q stop %#v, want preserved", req.MaxCompletionTokens, req.ServiceTier, req.Stop)
	}
}

func TestBuildOpenAIChatCompletionRequestDefaultsReasoningEffort(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{model: "gpt-5", want: "minimal"},
		{model: "gpt-5-mini", want: "minimal"},
		{model: "gpt-5.1", want: "none"},
		{model: "gpt-5.2", want: "none"},
		{model: "gpt-4.1", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			req := buildOpenAIChatCompletionRequest(tc.model, llm.NewChatContext(), &llm.ChatOptions{
				ParallelToolCalls: true,
			})
			if req.ReasoningEffort != tc.want {
				t.Fatalf("ReasoningEffort = %q, want %q", req.ReasoningEffort, tc.want)
			}
		})
	}
}

func TestBuildOpenAIChatCompletionRequestDropsReasoningEffortWithIncompatibleTools(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-5.2", llm.NewChatContext(), &llm.ChatOptions{
		Tools: []llm.Tool{requestTestTool{}},
		ExtraParams: map[string]any{
			"reasoning_effort":      "low",
			"max_completion_tokens": 128,
		},
	})

	if req.ReasoningEffort != "" {
		t.Fatalf("ReasoningEffort = %q, want dropped for gpt-5.2 with tools", req.ReasoningEffort)
	}
	if req.MaxCompletionTokens != 128 {
		t.Fatalf("MaxCompletionTokens = %d, want preserved", req.MaxCompletionTokens)
	}
}

func TestOpenAICompletionUsageHandlesMissingTokenDetails(t *testing.T) {
	usage := openAICompletionUsage(&openaisdk.Usage{
		CompletionTokens: 7,
		PromptTokens:     11,
		TotalTokens:      18,
	})

	if usage == nil {
		t.Fatal("openAICompletionUsage() = nil, want usage")
	}
	if usage.CompletionTokens != 7 || usage.PromptTokens != 11 || usage.TotalTokens != 18 {
		t.Fatalf("usage = %#v, want base token counts", usage)
	}
	if usage.PromptCachedTokens != 0 {
		t.Fatalf("PromptCachedTokens = %d, want 0 without prompt token details", usage.PromptCachedTokens)
	}
}

func TestOpenAICompletionUsageMapsCachedPromptTokens(t *testing.T) {
	usage := openAICompletionUsage(&openaisdk.Usage{
		PromptTokensDetails: &openaisdk.PromptTokensDetails{CachedTokens: 5},
	})

	if usage == nil {
		t.Fatal("openAICompletionUsage() = nil, want usage")
	}
	if usage.PromptCachedTokens != 5 {
		t.Fatalf("PromptCachedTokens = %d, want 5", usage.PromptCachedTokens)
	}
}
