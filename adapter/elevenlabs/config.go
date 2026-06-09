package elevenlabs

import (
	"fmt"
	"os"
)

const (
	elevenLabsPrimaryAPIKeyEnv  = "ELEVENLABS_API_KEY"
	elevenLabsFallbackAPIKeyEnv = "ELEVEN_API_KEY"
)

func resolveElevenLabsAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	if envKey := os.Getenv(elevenLabsPrimaryAPIKeyEnv); envKey != "" {
		return envKey
	}
	return os.Getenv(elevenLabsFallbackAPIKeyEnv)
}

func validateElevenLabsAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("elevenlabs API key is required, either as argument or set ELEVEN_API_KEY environmental variable")
	}
	return nil
}
