package azure

import (
	"context"
	"time"

	adapteropenai "github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

// LLM forwards the OpenAI-compatible implementation configured for Azure.
type LLM = adapteropenai.LLM

// LLMOption forwards provider options to the OpenAI-compatible Azure responses implementation.
type LLMOption = adapteropenai.LLMOption

func WithAzureLLMMaxOutputTokens(maxOutputTokens int) LLMOption {
	return adapteropenai.WithOpenAILLMMaxCompletionTokens(maxOutputTokens)
}

func WithAzureLLMTemperature(temperature float64) LLMOption {
	return adapteropenai.WithOpenAILLMTemperature(temperature)
}

func WithAzureLLMTopP(topP float64) LLMOption {
	return adapteropenai.WithOpenAILLMTopP(topP)
}

func WithAzureLLMServiceTier(serviceTier string) LLMOption {
	return adapteropenai.WithOpenAILLMServiceTier(serviceTier)
}

func WithAzureLLMPromptCacheKey(promptCacheKey string) LLMOption {
	return adapteropenai.WithOpenAILLMPromptCacheKey(promptCacheKey)
}

func WithAzureLLMPromptCacheRetention(promptCacheRetention string) LLMOption {
	return adapteropenai.WithOpenAILLMPromptCacheRetention(promptCacheRetention)
}

func WithAzureLLMParallelToolCalls(parallelToolCalls bool) LLMOption {
	return adapteropenai.WithOpenAILLMParallelToolCalls(parallelToolCalls)
}

func WithAzureLLMToolChoice(toolChoice llm.ToolChoice) LLMOption {
	return adapteropenai.WithOpenAILLMToolChoice(toolChoice)
}

func WithAzureLLMReasoningEffort(reasoningEffort string) LLMOption {
	return adapteropenai.WithOpenAILLMReasoningEffort(reasoningEffort)
}

func WithAzureLLMReasoning(reasoning map[string]any) LLMOption {
	return adapteropenai.WithOpenAILLMReasoning(reasoning)
}

func WithAzureLLMVerbosity(verbosity string) LLMOption {
	return adapteropenai.WithOpenAILLMVerbosity(verbosity)
}

func WithAzureLLMUser(user string) LLMOption {
	return adapteropenai.WithOpenAILLMUser(user)
}

func WithAzureLLMOrganization(organization string) LLMOption {
	return adapteropenai.WithOpenAILLMOrganization(organization)
}

func WithAzureLLMProject(project string) LLMOption {
	return adapteropenai.WithOpenAILLMProject(project)
}

func WithAzureLLMADTokenProvider(provider func(context.Context) (string, error)) LLMOption {
	return adapteropenai.WithOpenAILLMAzureADTokenProvider(provider)
}

func WithAzureLLMBaseURL(baseURL string) LLMOption {
	return adapteropenai.WithOpenAILLMAzureBaseURL(baseURL)
}

func WithAzureLLMTimeout(timeout time.Duration) LLMOption {
	return adapteropenai.WithOpenAILLMAzureTimeout(timeout)
}

func NewLLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...LLMOption) (*LLM, error) {
	return adapteropenai.NewAzureOpenAILLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken, opts...)
}

// Deprecated: use LLMOption.
type AzureLLMOption = LLMOption

// Deprecated: use NewLLM.
func NewAzureLLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...LLMOption) (*LLM, error) {
	return NewLLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken, opts...)
}
