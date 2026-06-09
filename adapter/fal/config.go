package fal

import "os"

const (
	falPrimaryAPIKeyEnv  = "FAL_KEY"
	falFallbackAPIKeyEnv = "FAL_API_KEY"
)

func resolveFalAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	if envKey := os.Getenv(falPrimaryAPIKeyEnv); envKey != "" {
		return envKey
	}
	return os.Getenv(falFallbackAPIKeyEnv)
}
