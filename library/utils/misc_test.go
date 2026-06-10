package utils

import (
	"os"
	"testing"
)

func TestCamelToSnakeCaseMatchesReference(t *testing.T) {
	tests := map[string]string{
		"HTTPServerID": "http_server_id",
		"roomID":       "room_id",
		"JobContext":   "job_context",
		"already_ok":   "already_ok",
	}

	for input, want := range tests {
		if got := CamelToSnakeCase(input); got != want {
			t.Fatalf("CamelToSnakeCase(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsCloudMatchesReferenceHosts(t *testing.T) {
	if !IsCloud("wss://tenant.livekit.cloud") {
		t.Fatal("IsCloud(livekit.cloud) = false, want true")
	}
	if !IsCloud("https://tenant.livekit.run/path") {
		t.Fatal("IsCloud(livekit.run) = false, want true")
	}
	if !IsCloud("wss://TENANT.LIVEKIT.CLOUD") {
		t.Fatal("IsCloud(uppercase livekit.cloud) = false, want true")
	}
	if IsCloud("http://localhost:7880") {
		t.Fatal("IsCloud(localhost) = true, want false")
	}
	if IsCloud("://bad-url") {
		t.Fatal("IsCloud(invalid URL) = true, want false")
	}
}

func TestIsDevModeMatchesReferenceEnv(t *testing.T) {
	t.Setenv("LIVEKIT_DEV_MODE", "1")
	if !IsDevMode() {
		t.Fatal("IsDevMode() = false, want true")
	}

	t.Setenv("LIVEKIT_DEV_MODE", "")
	if IsDevMode() {
		t.Fatal("IsDevMode() = true for empty value, want false")
	}

	t.Setenv("LIVEKIT_DEV_MODE", "true")
	if IsDevMode() {
		t.Fatal("IsDevMode() = true for value true, want false")
	}

	t.Setenv("LIVEKIT_DEV_MODE", "on")
	if IsDevMode() {
		t.Fatal("IsDevMode() = true for value on, want false")
	}
}

func TestIsHostedUsesReferenceEnv(t *testing.T) {
	const envName = "LIVEKIT_REMOTE_EOT_URL"
	original, ok := os.LookupEnv(envName)
	t.Cleanup(func() {
		if ok {
			if err := os.Setenv(envName, original); err != nil {
				t.Fatalf("restore %s: %v", envName, err)
			}
		} else if err := os.Unsetenv(envName); err != nil {
			t.Fatalf("unset %s: %v", envName, err)
		}
	})

	if err := os.Unsetenv(envName); err != nil {
		t.Fatalf("unset %s: %v", envName, err)
	}
	if IsHosted() {
		t.Fatal("IsHosted() = true for absent env, want false")
	}

	if err := os.Setenv(envName, "https://hosted.example"); err != nil {
		t.Fatalf("set %s: %v", envName, err)
	}
	if !IsHosted() {
		t.Fatal("IsHosted() = false, want true")
	}

	if err := os.Setenv(envName, ""); err != nil {
		t.Fatalf("set %s: %v", envName, err)
	}
	if !IsHosted() {
		t.Fatal("IsHosted() = false for empty but set env, want true")
	}
}

func TestNodeNameReturnsValue(t *testing.T) {
	if NodeName() == "" {
		t.Fatal("NodeName() = empty, want host name or fallback")
	}
}
