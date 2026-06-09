package minimax

import "os"

const minimaxAPIKeyEnv = "MINIMAX_API_KEY"

func resolveMinimaxAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv(minimaxAPIKeyEnv)
}
