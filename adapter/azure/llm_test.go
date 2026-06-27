package azure

import (
	"strings"
	"testing"
)

func TestAzureResponsesLLMFallsBackToReferenceEnvironment(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://env-resource.openai.azure.com")
	t.Setenv("AZURE_OPENAI_API_KEY", "env-azure-key")
	t.Setenv("OPENAI_API_VERSION", "2024-08-01-preview")

	model, err := NewAzureLLM("", "", "", "", "", "")
	if err != nil {
		t.Fatalf("NewAzureLLM error = %v", err)
	}

	if got := model.Model(); got != "gpt-4o" {
		t.Fatalf("Model = %q, want Azure responses reference default gpt-4o", got)
	}
	if got := model.Provider(); got != "env-resource.openai.azure.com" {
		t.Fatalf("Provider = %q, want Azure endpoint host", got)
	}
}

func TestAzureResponsesLLMRequiresEndpoint(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")
	t.Setenv("AZURE_OPENAI_API_KEY", "key")

	_, err := NewAzureLLM("gpt-4o", "", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "AZURE_OPENAI_ENDPOINT") {
		t.Fatalf("NewAzureLLM error = %v, want missing endpoint error", err)
	}
}

func TestAzureResponsesLLMRequiresCredential(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://env-resource.openai.azure.com")
	t.Setenv("AZURE_OPENAI_API_KEY", "")
	t.Setenv("AZURE_OPENAI_AD_TOKEN", "")

	_, err := NewAzureLLM("gpt-4o", "", "", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "AZURE_OPENAI_API_KEY") || !strings.Contains(err.Error(), "AZURE_OPENAI_AD_TOKEN") {
		t.Fatalf("NewAzureLLM error = %v, want missing credential error", err)
	}
}

func TestAzureResponsesLLMAcceptsReferenceMaxOutputTokensOption(t *testing.T) {
	model, err := NewAzureLLM(
		"gpt-4o",
		"https://voice-resource.openai.azure.com",
		"chat-deployment",
		"2024-06-01",
		"azure-key",
		"",
		WithAzureLLMMaxOutputTokens(128),
	)
	if err != nil {
		t.Fatalf("NewAzureLLM error = %v", err)
	}
	if model == nil {
		t.Fatal("NewAzureLLM returned nil model")
	}
}
