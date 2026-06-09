package trugen

import "os"

const trugenAPIKeyEnv = "TRUGEN_API_KEY"

func resolveTrugenAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv(trugenAPIKeyEnv)
}
