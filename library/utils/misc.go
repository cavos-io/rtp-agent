package utils

import (
	"net/url"
	"os"
	"regexp"
	"strings"
)

func NodeName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	return name
}

func CamelToSnakeCase(name string) string {
	first := regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`).ReplaceAllString(name, `${1}_${2}`)
	second := regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(first, `${1}_${2}`)
	return strings.ToLower(second)
}

func IsCloud(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	return strings.HasSuffix(hostname, ".livekit.cloud") || strings.HasSuffix(hostname, ".livekit.run")
}

func IsDevMode() bool {
	return os.Getenv("LIVEKIT_DEV_MODE") == "1"
}

func IsHosted() bool {
	_, ok := os.LookupEnv("LIVEKIT_REMOTE_EOT_URL")
	return ok
}
