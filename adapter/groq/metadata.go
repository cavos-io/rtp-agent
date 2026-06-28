package groq

import "net/url"

func groqProviderHost(baseURL string, fallback string) string {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return fallback
	}
	return u.Host
}
