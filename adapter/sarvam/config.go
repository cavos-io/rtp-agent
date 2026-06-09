package sarvam

import "os"

const sarvamAPIKeyEnv = "SARVAM_API_KEY"

func resolveSarvamAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv(sarvamAPIKeyEnv)
}
