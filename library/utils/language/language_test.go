package language

import (
	"testing"
)

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"English", "en"},
		{"eng", "en"},
		{"en-us", "en-US"},
		{"en_gb", "en-GB"},
		{"ZH-HANS", "zh-Hans"},
		{"indonesian", "id"},
		{"ind", "id"},
	}

	for _, tt := range tests {
		got := NormalizeLanguage(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeLanguage(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}
