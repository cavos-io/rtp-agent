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
	// bold: remove asterisks from **text** while preserving literal asterisks in words/expressions
	boldPattern = regexp.MustCompile(`(^|[^\w*])\*\*([^\s*][^*\n]*?[^\s*])\*\*($|[^\w*])`)
	// italic: remove asterisks from *text* while preserving literal asterisks in words/expressions
	italicPattern = regexp.MustCompile(`(^|[^\w*])\*([^\s*][^*\n]*?[^\s*])\*($|[^\w*])`)
	// bold with underscores: remove underscores from __text__ while preserving intra-word underscores
	boldUnderPattern = regexp.MustCompile(`(^|\W)__([^_]+?)__($|\W)`)
	// italic with underscores: remove underscores from _text_ while preserving intra-word underscores
	italicUnderPattern = regexp.MustCompile(`(^|\W)_([^_]+?)_($|\W)`)
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
	text = boldPattern.ReplaceAllString(text, "$1$2$3")
	text = italicPattern.ReplaceAllString(text, "$1$2$3")
	text = boldUnderPattern.ReplaceAllString(text, "$1$2$3")
	text = italicUnderPattern.ReplaceAllString(text, "$1$2$3")
	text = codeBlockPattern.ReplaceAllString(text, "")
	text = inlineCodePattern.ReplaceAllString(text, "$1")
	text = strikePattern.ReplaceAllString(text, "")

	// Final cleanup
	text = strings.ReplaceAll(text, "~~", "")
	text = strings.ReplaceAll(text, "`", "")

	return strings.TrimSpace(text)
}

func FilterEmoji(text string) string {
	return emojiPattern.ReplaceAllString(text, "")
}

func ApplyTextTransforms(text string) string {
	text = FilterMarkdown(text)
	text = FilterEmoji(text)
	return text
}
