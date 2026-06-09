package gradium

import "os"

const gradiumAPIKeyEnv = "GRADIUM_API_KEY"

func resolveGradiumAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv(gradiumAPIKeyEnv)
}
