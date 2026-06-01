package tts

import (
	"strings"
	"testing"
)

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

func TestFilterMarkdownPreservesFormattingMarkersInsideUnicodeWords(t *testing.T) {
	input := "Keep café**bold** and mañana_italic_ intact."
	want := "Keep café**bold** and mañana_italic_ intact."

	if got := FilterMarkdown(input); got != want {
		t.Fatalf("FilterMarkdown() = %q, want %q", got, want)
	}
}

func TestFilterMarkdownRemovesSingleCharacterEmphasis(t *testing.T) {
	input := "Say **x** and *y*, plus __z__ and _q_."
	want := "Say x and y, plus z and q."

	if got := FilterMarkdown(input); got != want {
		t.Fatalf("FilterMarkdown() = %q, want %q", got, want)
	}
}

func TestFilterMarkdownRemovesCodeFencesWithoutDroppingCode(t *testing.T) {
	input := "Before\n```go\nfmt.Println(\"hi\")\n```\nAfter"
	want := "Before\n\nfmt.Println(\"hi\")\n\nAfter"

	if got := FilterMarkdown(input); got != want {
		t.Fatalf("FilterMarkdown() = %q, want %q", got, want)
	}
}

func TestFilterMarkdownPreservesLooseTildes(t *testing.T) {
	input := "Remove ~~deleted~~ but keep ~~ spaced ~~ and approx ~10."
	want := "Remove  but keep ~~ spaced ~~ and approx ~10."

	if got := FilterMarkdown(input); got != want {
		t.Fatalf("FilterMarkdown() = %q, want %q", got, want)
	}
}

func TestFilterMarkdownRemovesSingleCharacterStrikethrough(t *testing.T) {
	input := "Remove ~~x~~ but keep ~~ spaced ~~."
	want := "Remove  but keep ~~ spaced ~~."

	if got := FilterMarkdown(input); got != want {
		t.Fatalf("FilterMarkdown() = %q, want %q", got, want)
	}
}

func TestTextTransformBufferYieldsBeforeTrailingSplitToken(t *testing.T) {
	buffer := NewTextTransformBuffer()

	if got, want := buffer.Push("Hello, "), []string{"Hello,"}; !equalStringSlices(got, want) {
		t.Fatalf("Push() = %#v, want %#v", got, want)
	}
	if got := buffer.Push("world"); len(got) != 0 {
		t.Fatalf("Push() = %#v, want no output before flush", got)
	}
	if got, want := buffer.Flush(), []string{" world"}; !equalStringSlices(got, want) {
		t.Fatalf("Flush() = %#v, want %#v", got, want)
	}
}

func TestTextTransformBufferFiltersMarkdownAcrossChunks(t *testing.T) {
	buffer := NewTextTransformBuffer()

	chunks := append(buffer.Push("Say **bo"), buffer.Push("ld** now")...)
	chunks = append(chunks, buffer.Flush()...)

	if got, want := strings.Join(chunks, ""), "Say bold now"; got != want {
		t.Fatalf("joined output = %q, want %q; chunks = %#v", got, want, chunks)
	}
	for _, chunk := range chunks {
		if strings.Contains(chunk, "**") {
			t.Fatalf("chunk %q leaked markdown markers; chunks = %#v", chunk, chunks)
		}
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
