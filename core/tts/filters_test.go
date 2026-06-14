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

func TestApplyTextTransformsRemovesEndCallToolMarkers(t *testing.T) {
	input := "Goodbye (end_call). [endcall] {END_CALL}"
	want := "Goodbye .  "

	if got := ApplyTextTransforms(input); got != want {
		t.Fatalf("ApplyTextTransforms() = %q, want %q", got, want)
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

func TestTextTransformBufferSplitsTerminalPunctuationLikeReference(t *testing.T) {
	buffer := NewTextTransformBuffer()

	chunks := append(buffer.Push("Halo, ada yang bisa saya bantu?"), buffer.Flush()...)

	want := []string{"Halo, ada yang bisa saya bantu", "?"}
	if !equalStringSlices(chunks, want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
	if got := strings.Join(chunks, ""); got != "Halo, ada yang bisa saya bantu?" {
		t.Fatalf("joined chunks = %q, want original utterance", got)
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

func TestTextTransformBufferCanApplyEmojiOnlyTransform(t *testing.T) {
	buffer, err := NewTextTransformBufferWithTransforms([]string{"filter_emoji"})
	if err != nil {
		t.Fatalf("NewTextTransformBufferWithTransforms error = %v", err)
	}

	chunks := append(buffer.Push("Say **hi** 😊"), buffer.Flush()...)

	if want := []string{"Say **hi** "}; !equalStringSlices(chunks, want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
	if got, want := strings.Join(chunks, ""), "Say **hi** "; got != want {
		t.Fatalf("joined output = %q, want %q; chunks = %#v", got, want, chunks)
	}
}

func TestTextTransformBufferCanApplyEmptyTransformList(t *testing.T) {
	buffer, err := NewTextTransformBufferWithTransforms([]string{})
	if err != nil {
		t.Fatalf("NewTextTransformBufferWithTransforms error = %v", err)
	}

	chunks := append(buffer.Push("Say **hi** 😊"), buffer.Flush()...)

	if want := []string{"Say **hi** 😊"}; !equalStringSlices(chunks, want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
}

func TestTextTransformBufferFiltersEndCallToolMarkersAcrossChunks(t *testing.T) {
	buffer := NewTextTransformBuffer()

	chunks := append(buffer.Push("Goodbye (end_"), buffer.Push("call).")...)
	chunks = append(chunks, buffer.Flush()...)

	if got, want := strings.Join(chunks, ""), "Goodbye ."; got != want {
		t.Fatalf("joined output = %q, want %q; chunks = %#v", got, want, chunks)
	}
	for _, chunk := range chunks {
		if strings.Contains(strings.ToLower(chunk), "end_call") || strings.Contains(strings.ToLower(chunk), "endcall") {
			t.Fatalf("chunk %q leaked tool marker; chunks = %#v", chunk, chunks)
		}
	}
}

func TestTextReplaceBufferReplacesTermsAcrossChunks(t *testing.T) {
	buffer := NewTextReplaceBuffer(map[string]string{"LiveKit": "Cavos"}, false)

	chunks := append(buffer.Push("Use Li"), buffer.Push("veKit now.")...)
	chunks = append(chunks, buffer.Flush()...)

	if got, want := strings.Join(chunks, ""), "Use Cavos now."; got != want {
		t.Fatalf("joined output = %q, want %q; chunks = %#v", got, want, chunks)
	}
	for _, chunk := range chunks {
		if strings.Contains(chunk, "LiveKit") {
			t.Fatalf("chunk %q leaked replaced term; chunks = %#v", chunk, chunks)
		}
	}

	shorter := NewTextReplaceBuffer(map[string]string{"LiveKit": "LK"}, false)

	chunks = append(shorter.Push("Try Live"), shorter.Push("Kit.")...)
	chunks = append(chunks, shorter.Flush()...)

	if got, want := strings.Join(chunks, ""), "Try LK."; got != want {
		t.Fatalf("joined output = %q, want %q; chunks = %#v", got, want, chunks)
	}
}

func TestTextRegexReplaceBufferReplacesReferenceSubstringsAcrossChunks(t *testing.T) {
	buffer := NewTextRegexReplaceBuffer(map[string]string{"cat": "dog"}, false)

	chunks := append(buffer.Push("Please con"), buffer.Push("catenate cat.")...)
	chunks = append(chunks, buffer.Flush()...)

	if got, want := strings.Join(chunks, ""), "Please condogenate dog."; got != want {
		t.Fatalf("joined output = %q, want reference substring replacement %q; chunks = %#v", got, want, chunks)
	}

	sensitive := NewTextRegexReplaceBuffer(map[string]string{"cat": "dog"}, true)
	chunks = append(sensitive.Push("Cat cat"), sensitive.Flush()...)
	if got, want := strings.Join(chunks, ""), "Cat dog"; got != want {
		t.Fatalf("case-sensitive output = %q, want %q; chunks = %#v", got, want, chunks)
	}
}

func TestTextRegexReplaceBufferPreservesReferenceReplacementOrder(t *testing.T) {
	buffer := NewOrderedTextRegexReplaceBuffer([]TextReplacement{
		{Old: "ab", New: "X"},
		{Old: "a", New: "Y"},
	}, true)

	chunks := append(buffer.Push("ab"), buffer.Flush()...)

	if got, want := strings.Join(chunks, ""), "X"; got != want {
		t.Fatalf("joined output = %q, want reference ordered replacement %q; chunks = %#v", got, want, chunks)
	}
}

func TestTextReplaceBufferReplacesWholeWordsAndPreservesPunctuation(t *testing.T) {
	buffer := NewTextReplaceBuffer(map[string]string{"flow": "stream"}, false)

	chunks := append(buffer.Push("Flow,"), buffer.Push(" workflow flow!")...)
	chunks = append(chunks, buffer.Flush()...)

	if got, want := strings.Join(chunks, ""), "stream, workflow stream!"; got != want {
		t.Fatalf("joined output = %q, want %q; chunks = %#v", got, want, chunks)
	}
}

func TestTextReplaceBufferDoesNotReplaceIncompleteFinalWord(t *testing.T) {
	buffer := NewTextReplaceBuffer(map[string]string{"flow": "stream"}, false)

	chunks := append(buffer.Push("flow"), buffer.Push("er ")...)
	chunks = append(chunks, buffer.Flush()...)

	if got, want := strings.Join(chunks, ""), "flower "; got != want {
		t.Fatalf("joined output = %q, want %q; chunks = %#v", got, want, chunks)
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
