package tts

import "testing"

func TestFilterMarkdownRemovesInlineFormatting(t *testing.T) {
	input := "# Greeting\n\nThis is **bold**, *italic*, __strong__, and _emphasis_.\n- [Link](https://example.com)\n![Alt text](image.png)"
	want := "Greeting\n\nThis is bold, italic, strong, and emphasis.\nLink\nAlt text"

	if got := FilterMarkdown(input); got != want {
		t.Fatalf("FilterMarkdown() = %q, want %q", got, want)
	}
}

func TestFilterMarkdownPreservesLiteralPunctuationInsideWords(t *testing.T) {
	input := "Use snake_case and calculate 2*3, not __strong__ or **bold**."
	want := "Use snake_case and calculate 2*3, not strong or bold."

	if got := FilterMarkdown(input); got != want {
		t.Fatalf("FilterMarkdown() = %q, want %q", got, want)
	}
}
