package tts

import (
	"regexp"
	"strings"
)

var (
	// header: remove # and following spaces
	headerPattern = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	// list markers: remove -, +, * and following spaces
	listPattern = regexp.MustCompile(`(?m)^\s*[-+*]\s+`)
	// block quotes: remove > and following spaces
	quotePattern = regexp.MustCompile(`(?m)^\s*>\s+`)

	// images: keep alt text ![alt](url) -> alt
	imagePattern = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	// links: keep text part [text](url) -> text
	linkPattern = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// bold: remove asterisks from **text**
	boldPattern = regexp.MustCompile(`\*\*([^*]+?)\*\*`)
	// italic: remove asterisks from *text*
	italicPattern = regexp.MustCompile(`\*([^*]+?)\*`)
	// bold with underscores: remove underscores from __text__
	boldUnderPattern = regexp.MustCompile(`\b__([^_]+?)__\b`)
	// italic with underscores: remove underscores from _text_
	italicUnderPattern = regexp.MustCompile(`\b_([^_]+?)_\b`)
	// code blocks: remove ``` from ```text```
	codeBlockPattern = regexp.MustCompile("(?s)```.*?```")
	// inline code: remove ` from `text`
	inlineCodePattern = regexp.MustCompile("`([^`]+?)`")
	// strikethrough: remove ~~text~~
	strikePattern = regexp.MustCompile(`~~([^~]*?)~~`)

	// Emoji block ranges
	emojiPattern = regexp.MustCompile(`[\x{1F000}-\x{1FBFF}]|[\x{2600}-\x{26FF}]|[\x{2700}-\x{27BF}]|[\x{2B00}-\x{2BFF}]|[\x{FE00}-\x{FE0F}]|\x{200D}|\x{20E3}`)
)

func FilterMarkdown(text string) string {
	if text == "" {
		return ""
	}

	// Line patterns
	text = headerPattern.ReplaceAllString(text, "")
	text = listPattern.ReplaceAllString(text, "")
	text = quotePattern.ReplaceAllString(text, "")

	// Inline patterns
	text = imagePattern.ReplaceAllString(text, "$1")
	text = linkPattern.ReplaceAllString(text, "$1")
	text = boldPattern.ReplaceAllString(text, "$1")
	text = italicPattern.ReplaceAllString(text, "$1")
	text = boldUnderPattern.ReplaceAllString(text, "$1")
	text = italicUnderPattern.ReplaceAllString(text, "$1")
	text = codeBlockPattern.ReplaceAllString(text, "")
	text = inlineCodePattern.ReplaceAllString(text, "$1")
	text = strikePattern.ReplaceAllString(text, "")

	// Final cleanup
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "*", "")
	text = strings.ReplaceAll(text, "__", "")
	text = strings.ReplaceAll(text, "_", "")
	text = strings.ReplaceAll(text, "~~", "")
	text = strings.ReplaceAll(text, "`", "")

	// Trim only vertical whitespace (newlines/tabs left by markdown removal),
	// but preserve leading/trailing spaces so that BPE word-boundary tokens
	// (e.g. " selamat") keep their inter-word space when sent to TTS.
	return strings.Trim(text, "\r\n\t")
}

func FilterEmoji(text string) string {
	return emojiPattern.ReplaceAllString(text, "")
}

func ApplyTextTransforms(text string) string {
	text = FilterMarkdown(text)
	text = FilterEmoji(text)
	return text
}
