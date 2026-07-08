package ultravox

import (
	"strings"
	"testing"
)

func TestUltravoxPluginMetadataMatchesReference(t *testing.T) {
	if strings.HasPrefix(PluginVersion, "v") {
		t.Fatalf("PluginVersion = %q, want raw reference __version__ without v prefix", PluginVersion)
	}
	if !strings.Contains(PluginVersion, "rc") {
		t.Fatalf("PluginVersion = %q, want current reference release-candidate marker", PluginVersion)
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
			name: "version",
			got:  PluginVersion,
			want: "1.5.19.rc1",
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
