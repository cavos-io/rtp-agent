package ultravox

import (
	"strings"
	"testing"
)

func TestUltravoxPluginMetadataMatchesReference(t *testing.T) {
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
			want: "v0.1.5",
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
