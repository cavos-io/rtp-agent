package gnani

import "os"

const gnaniAPIKeyEnv = "GNANI_API_KEY"

func resolveGnaniAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv(gnaniAPIKeyEnv)
}
