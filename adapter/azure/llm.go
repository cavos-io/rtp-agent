package azure

import adapteropenai "github.com/cavos-io/rtp-agent/adapter/openai"

// AzureLLMOption forwards provider options to the OpenAI-compatible Azure responses implementation.
type AzureLLMOption = adapteropenai.OpenAILLMOption

func WithAzureLLMMaxOutputTokens(maxOutputTokens int) AzureLLMOption {
	return adapteropenai.WithOpenAILLMMaxCompletionTokens(maxOutputTokens)
}

func NewAzureLLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken string, opts ...AzureLLMOption) (*adapteropenai.OpenAILLM, error) {
	return adapteropenai.NewAzureOpenAILLM(model, azureEndpoint, azureDeployment, apiVersion, apiKey, azureADToken, opts...)
}
