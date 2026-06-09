package tavus

import "os"

const tavusAPIKeyEnv = "TAVUS_API_KEY"

func resolveTavusAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv(tavusAPIKeyEnv)
}
