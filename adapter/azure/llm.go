package azure

import (
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

func NewAzureLLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...AzureLLMOption) (*adapteropenai.OpenAILLM, error) {
	return adapteropenai.NewAzureOpenAILLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken, opts...)
}
