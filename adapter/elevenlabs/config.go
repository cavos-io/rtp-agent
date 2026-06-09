package elevenlabs

import "os"

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
