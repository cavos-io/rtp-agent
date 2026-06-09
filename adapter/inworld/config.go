package inworld

import "os"

const inworldAPIKeyEnv = "INWORLD_API_KEY"

func resolveInworldAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv(inworldAPIKeyEnv)
}
