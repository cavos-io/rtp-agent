package ultravox

import (
	"strconv"
	"strings"
	"testing"
)

func TestUltravoxPluginMetadataMatchesReference(t *testing.T) {
	if !isProjectPluginVersion(PluginVersion) {
		t.Fatalf("PluginVersion = %q, want vMAJOR.MINOR.PATCH", PluginVersion)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "title",
			got:  PluginTitle,
			want: "rtp-agent.plugins.ultravox",
		},
		{
			name: "package",
			got:  PluginPackage,
			want: "rtp-agent.plugins.ultravox",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
			if strings.TrimSpace(tt.got) != tt.got {
				t.Fatalf("%s = %q, want no surrounding whitespace", tt.name, tt.got)
			}
		})
	}
}

func TestUltravoxPluginVersionPattern(t *testing.T) {
	tests := []struct {
		name    string
		version string
		valid   bool
	}{
		{name: "current release", version: PluginVersion, valid: true},
		{name: "future release", version: "v0.4.1", valid: true},
		{name: "major release", version: "v1.0.0", valid: true},
		{name: "missing prefix", version: "0.4.1"},
		{name: "missing patch", version: "v0.4"},
		{name: "release candidate", version: "v0.4.1.rc1"},
		{name: "non-numeric", version: "vnext.4.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isProjectPluginVersion(tt.version); got != tt.valid {
				t.Fatalf("isProjectPluginVersion(%q) = %v, want %v", tt.version, got, tt.valid)
			}
		})
	}
}

func isProjectPluginVersion(version string) bool {
	if !strings.HasPrefix(version, "v") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}
