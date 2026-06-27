package azure

import (
	"context"

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

func WithAzureLLMParallelToolCalls(parallelToolCalls bool) AzureLLMOption {
	return adapteropenai.WithOpenAILLMParallelToolCalls(parallelToolCalls)
}

func WithAzureLLMToolChoice(toolChoice llm.ToolChoice) AzureLLMOption {
	return adapteropenai.WithOpenAILLMToolChoice(toolChoice)
}

func WithAzureLLMReasoningEffort(reasoningEffort string) AzureLLMOption {
	return adapteropenai.WithOpenAILLMReasoningEffort(reasoningEffort)
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

func NewAzureLLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...AzureLLMOption) (*adapteropenai.OpenAILLM, error) {
	return adapteropenai.NewAzureOpenAILLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken, opts...)
}
