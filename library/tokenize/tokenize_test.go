package tokenize

import (
	"testing"
)

func TestSplitSentences(t *testing.T) {
	tests := []struct {
		text     string
		expected []string
	}{
		{
			text:     "Hello world. This is a test.",
			expected: []string{"Hello world.", "This is a test."},
		},
		{
			text:     "He said \"Hello!\" and then left.",
			expected: []string{"He said \"Hello!\"", "and then left."},
		},
		{
			text:     "Mr. Smith went to Washington.",
			expected: []string{"Mr. Smith went to Washington."},
		},
	}

	for _, tt := range tests {
		results := SplitSentences(tt.text, 1, false)
		if len(results) != len(tt.expected) {
			t.Errorf("For text '%s', expected %d sentences, got %d", tt.text, len(tt.expected), len(results))
			continue
		}
		for i, s := range results {
			if s.Token != tt.expected[i] {
				t.Errorf("For text '%s', expected sentence %d to be '%s', got '%s'", tt.text, i, tt.expected[i], s.Token)
			}
		}
	}
}
