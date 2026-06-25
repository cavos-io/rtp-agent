package fal

import "testing"

func TestFalSharedAPIKeyResolutionScenarios(t *testing.T) {
	tests := []struct {
		name        string
		explicit    string
		primaryEnv  string
		fallbackEnv string
		want        string
	}{
		{
			name:        "explicit key wins",
			explicit:    "explicit-key",
			primaryEnv:  "env-key",
			fallbackEnv: "fallback-env-key",
			want:        "explicit-key",
		},
		{
			name:        "primary environment wins",
			primaryEnv:  "env-key",
			fallbackEnv: "fallback-env-key",
			want:        "env-key",
		},
		{
			name:        "fallback environment used",
			fallbackEnv: "fallback-env-key",
			want:        "fallback-env-key",
		},
		{
			name: "empty configuration remains empty",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FAL_KEY", tt.primaryEnv)
			t.Setenv("FAL_API_KEY", tt.fallbackEnv)

			if got := resolveFalAPIKey(tt.explicit); got != tt.want {
				t.Fatalf("api key = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFalSharedAPIKeyResolutionPrefersPrimaryEnvironment(t *testing.T) {
	t.Setenv("FAL_KEY", "env-key")
	t.Setenv("FAL_API_KEY", "fallback-env-key")

	if got := resolveFalAPIKey(""); got != "env-key" {
		t.Fatalf("api key = %q, want primary env key", got)
	}
	if got := resolveFalAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", got)
	}
}

func TestFalSharedAPIKeyResolutionUsesFallbackEnvironment(t *testing.T) {
	t.Setenv("FAL_KEY", "")
	t.Setenv("FAL_API_KEY", "fallback-env-key")

	if got := resolveFalAPIKey(""); got != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", got)
	}
}

func TestFalSharedAPIKeyResolutionAllowsEmptyConfiguration(t *testing.T) {
	t.Setenv("FAL_KEY", "")
	t.Setenv("FAL_API_KEY", "")

	if got := resolveFalAPIKey(""); got != "" {
		t.Fatalf("api key = %q, want empty key for provider-specific validation", got)
	}
}

func TestFalSharedAPIKeyResolutionPreservesConfiguredValue(t *testing.T) {
	t.Setenv("FAL_KEY", " env-key ")
	t.Setenv("FAL_API_KEY", " fallback-env-key ")

	if got := resolveFalAPIKey(""); got != " env-key " {
		t.Fatalf("api key = %q, want configured primary value unchanged", got)
	}
	if got := resolveFalAPIKey(" explicit-key "); got != " explicit-key " {
		t.Fatalf("api key = %q, want configured explicit value unchanged", got)
	}
}
