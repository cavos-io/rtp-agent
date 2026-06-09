package spitch

import "os"

const spitchAPIKeyEnv = "SPITCH_API_KEY"

func resolveSpitchAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv(spitchAPIKeyEnv)
}
