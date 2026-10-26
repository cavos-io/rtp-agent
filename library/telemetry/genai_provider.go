package telemetry

import (
	"strings"
	"unicode"
)

// NormalizeGenAIProvider maps provider labels to the Python GenAI registry.
func NormalizeGenAIProvider(provider string) string {
	value := strings.TrimSpace(provider)

	host := strings.ToLower(value)
	if normalized := normalizeGenAIProviderHost(host); normalized != "" {
		return normalized
	}

	canonical := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}

		return -1
	}, host)

	if normalized := normalizeCanonicalGenAIProvider(canonical); normalized != "" {
		return normalized
	}

	return value
}

func normalizeGenAIProviderHost(host string) string {
	switch host {
	case "api.anthropic.com":
		return "anthropic"
	case "api.cohere.ai", "api.cohere.com":
		return "cohere"
	case "api.deepseek.com":
		return "deepseek"
	case "api.groq.com":
		return "groq"
	case "api.mistral.ai":
		return "mistral_ai"
	case "api.moonshot.ai", "api.moonshot.cn":
		return "moonshot_ai"
	case "api.openai.com":
		return "openai"
	case "api.perplexity.ai":
		return "perplexity"
	default:
		return normalizeOtherGenAIProviderHost(host)
	}
}

func normalizeOtherGenAIProviderHost(host string) string {
	switch host {
	case "api.x.ai":
		return "x_ai"
	case "generativelanguage.googleapis.com":
		return "gcp.gemini"
	}

	if strings.HasSuffix(host, ".openai.azure.com") {
		return "azure.ai.openai"
	}

	if strings.HasSuffix(host, ".services.ai.azure.com") {
		return "azure.ai.inference"
	}

	if strings.HasSuffix(host, ".aiplatform.googleapis.com") {
		return "gcp.vertex_ai"
	}

	if strings.HasPrefix(host, "bedrock") && strings.HasSuffix(host, ".amazonaws.com") {
		return "aws.bedrock"
	}

	return ""
}

func normalizeCanonicalGenAIProvider(canonical string) string {
	switch canonical {
	case "openai":
		return "openai"
	case "google", "googlegenai", "googlecloudplatform":
		return "gcp.gen_ai"
	case "gemini", "generativelanguagegoogleapiscom":
		return "gcp.gemini"
	case "vertexai", "vertexaimodelgarden":
		return "gcp.vertex_ai"
	case "amazon", "amazonbedrock", "awsbedrock", "bedrock":
		return "aws.bedrock"
	case "mistral", "mistralai":
		return "mistral_ai"
	case "xai":
		return "x_ai"
	default:
		return ""
	}
}
