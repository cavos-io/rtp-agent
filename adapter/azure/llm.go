package azure

import (
	"context"
	"time"

	adapteropenai "github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

// AzureLLMOption forwards provider options to the OpenAI-compatible Azure responses implementation.
type AzureLLMOption = adapteropenai.OpenAILLMOption

func WithAzureLLMMaxOutputTokens(maxOutputTokens int) AzureLLMOption {
	return adapteropenai.WithOpenAILLMMaxCompletionTokens(maxOutputTokens)
}

func WithAzureLLMTemperature(temperature float64) AzureLLMOption {
	return adapteropenai.WithOpenAILLMTemperature(temperature)
}

func WithAzureLLMTopP(topP float64) AzureLLMOption {
	return adapteropenai.WithOpenAILLMTopP(topP)
}

func WithAzureLLMServiceTier(serviceTier string) AzureLLMOption {
	return adapteropenai.WithOpenAILLMServiceTier(serviceTier)
}

func WithAzureLLMPromptCacheKey(promptCacheKey string) AzureLLMOption {
	return adapteropenai.WithOpenAILLMPromptCacheKey(promptCacheKey)
}

func WithAzureLLMPromptCacheRetention(promptCacheRetention string) AzureLLMOption {
	return adapteropenai.WithOpenAILLMPromptCacheRetention(promptCacheRetention)
}

func WithAzureLLMParallelToolCalls(parallelToolCalls bool) AzureLLMOption {
	return adapteropenai.WithOpenAILLMParallelToolCalls(parallelToolCalls)
}

func WithAzureLLMToolChoice(toolChoice llm.ToolChoice) AzureLLMOption {
	return adapteropenai.WithOpenAILLMToolChoice(toolChoice)
}

func WithAzureLLMReasoningEffort(reasoningEffort string) AzureLLMOption {
	return adapteropenai.WithOpenAILLMReasoningEffort(reasoningEffort)
}

func WithAzureLLMReasoning(reasoning map[string]any) AzureLLMOption {
	return adapteropenai.WithOpenAILLMReasoning(reasoning)
}

func WithAzureLLMUser(user string) AzureLLMOption {
	return adapteropenai.WithOpenAILLMUser(user)
}

func WithAzureLLMOrganization(organization string) AzureLLMOption {
	return adapteropenai.WithOpenAILLMOrganization(organization)
}

func WithAzureLLMProject(project string) AzureLLMOption {
	return adapteropenai.WithOpenAILLMProject(project)
}

func WithAzureLLMADTokenProvider(provider func(context.Context) (string, error)) AzureLLMOption {
	return adapteropenai.WithOpenAILLMAzureADTokenProvider(provider)
}

func WithAzureLLMBaseURL(baseURL string) AzureLLMOption {
	return adapteropenai.WithOpenAILLMAzureBaseURL(baseURL)
}

func WithAzureLLMTimeout(timeout time.Duration) AzureLLMOption {
	return adapteropenai.WithOpenAILLMAzureTimeout(timeout)
}

func NewAzureLLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...AzureLLMOption) (*adapteropenai.OpenAILLM, error) {
	return adapteropenai.NewAzureOpenAILLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken, opts...)
}
