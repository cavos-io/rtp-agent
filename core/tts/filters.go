package tts

import (
	"regexp"
	"sort"
	"strings"
)

var (
	toolCallMarkerPattern = regexp.MustCompile(`(?i)[\(\[\{]\s*end_?call\s*[\)\]\}]`)

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
	boldPattern = regexp.MustCompile(`(^|[^\p{L}\p{N}_*])\*\*([^\s*](?:[^*\n]*?[^\s*])?)\*\*($|[^\p{L}\p{N}_*])`)
	// italic: remove asterisks from *text* while preserving literal asterisks in words/expressions
	italicPattern = regexp.MustCompile(`(^|[^\p{L}\p{N}_*])\*([^\s*](?:[^*\n]*?[^\s*])?)\*($|[^\p{L}\p{N}_*])`)
	// bold with underscores: remove underscores from __text__ while preserving intra-word underscores
	boldUnderPattern = regexp.MustCompile(`(^|[^\p{L}\p{N}_])__([^_]+?)__($|[^\p{L}\p{N}_])`)
	// italic with underscores: remove underscores from _text_ while preserving intra-word underscores
	italicUnderPattern = regexp.MustCompile(`(^|[^\p{L}\p{N}_])_([^_]+?)_($|[^\p{L}\p{N}_])`)
	// code fences: remove ``` or ```lang while preserving fenced text
	codeBlockPattern = regexp.MustCompile("`{3,4}\\S*")
	// inline code: remove ` from `text`
	inlineCodePattern = regexp.MustCompile("`([^`]+?)`")
	// strikethrough: remove ~~text~~ only when text is tight against tildes
	strikePattern = regexp.MustCompile(`~~([^\s~](?:[^~]*?[^\s~])?)~~`)

	// Emoji block ranges
	emojiPattern = regexp.MustCompile(`[\x{1F000}-\x{1FBFF}]|[\x{2600}-\x{26FF}]|[\x{2700}-\x{27BF}]|[\x{2B00}-\x{2BFF}]|[\x{FE00}-\x{FE0F}]|\x{200D}|\x{20E3}`)

	completeLinksPattern  = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`)
	completeImagesPattern = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	inlineSplitTokens     = " ,.?!;，。？！；"
)

type TextTransformBuffer struct {
	buffer          string
	bufferIsNewline bool
}

type TextReplaceBuffer struct {
	replacements  []textReplacement
	tailLen       int
	caseSensitive bool
	buffer        string
}

type textReplacement struct {
	old string
	new string
}

func NewTextTransformBuffer() *TextTransformBuffer {
	return &TextTransformBuffer{bufferIsNewline: true}
}

func NewTextReplaceBuffer(replacements map[string]string, caseSensitive bool) *TextReplaceBuffer {
	keys := make([]string, 0, len(replacements))
	tailLen := 0
	for old := range replacements {
		keys = append(keys, old)
		if len(old) > tailLen {
			tailLen = len(old)
		}
	}
	sort.Strings(keys)

	ordered := make([]textReplacement, 0, len(keys))
	for _, old := range keys {
		ordered = append(ordered, textReplacement{old: old, new: replacements[old]})
	}
	if tailLen > 0 {
		tailLen--
	}

	return &TextReplaceBuffer{
		replacements:  ordered,
		tailLen:       tailLen,
		caseSensitive: caseSensitive,
	}
}

func (b *TextTransformBuffer) Push(text string) []string {
	if text == "" {
		return nil
	}
	b.buffer += text

	if strings.Contains(b.buffer, "\n") {
		lines := strings.Split(b.buffer, "\n")
		b.buffer = lines[len(lines)-1]

		out := make([]string, 0, len(lines)-1)
		for i, line := range lines[:len(lines)-1] {
			isNewline := true
			if i == 0 {
				isNewline = b.bufferIsNewline
			}
			out = appendTransformedText(out, line+"\n", isNewline, false)
		}
		b.bufferIsNewline = true
		return out
	}

	lastSplitPos := strings.LastIndexAny(b.buffer, inlineSplitTokens)
	if lastSplitPos >= 1 {
		processable := b.buffer[:lastSplitPos]
		rest := b.buffer[lastSplitPos:]
		if !hasIncompleteMarkdownPattern(processable) {
			b.buffer = rest
			out := appendTransformedText(nil, processable, b.bufferIsNewline, false)
			b.bufferIsNewline = false
			return out
		}
	}

	return nil
}

func (b *TextReplaceBuffer) Push(text string) []string {
	if text == "" {
		return nil
	}
	b.buffer += text
	if len(b.buffer) <= b.tailLen {
		return nil
	}

	b.buffer = b.apply(b.buffer)
	if len(b.buffer) <= b.tailLen {
		return nil
	}
	flushTo := len(b.buffer) - b.tailLen
	out := b.buffer[:flushTo]
	b.buffer = b.buffer[flushTo:]
	if out == "" {
		return nil
	}
	return []string{out}
}

func (b *TextTransformBuffer) Flush() []string {
	return b.flush()
}

func (b *TextReplaceBuffer) Flush() []string {
	if b.buffer == "" {
		return nil
	}
	text := b.apply(b.buffer)
	b.buffer = ""
	if text == "" {
		return nil
	}
	return []string{text}
}

func (b *TextReplaceBuffer) apply(text string) string {
	for _, replacement := range b.replacements {
		if replacement.old == "" {
			continue
		}
		pattern := regexp.QuoteMeta(replacement.old)
		if !b.caseSensitive {
			pattern = "(?i)" + pattern
		}
		re := regexp.MustCompile(pattern)
		text = re.ReplaceAllStringFunc(text, func(string) string {
			return replacement.new
		})
	}
	return text
}

func FilterMarkdown(text string) string {
	if text == "" {
		return ""
	}

	text = filterMarkdown(text, true, true)
	return text
}

func filterMarkdown(text string, applyLinePatterns bool, trim bool) string {
	if text == "" {
		return ""
	}

	if applyLinePatterns {
		text = headerPattern.ReplaceAllString(text, "")
		text = listPattern.ReplaceAllString(text, "")
		text = quotePattern.ReplaceAllString(text, "")
	}

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
	text = strings.ReplaceAll(text, "`", "")

	if trim {
		return strings.TrimSpace(text)
	}
	return text
}

func FilterEmoji(text string) string {
	return emojiPattern.ReplaceAllString(text, "")
}

func FilterToolCallMarkers(text string) string {
	return toolCallMarkerPattern.ReplaceAllString(text, "")
}

func ApplyTextTransforms(text string) string {
	text = FilterMarkdown(text)
	text = FilterEmoji(text)
	text = FilterToolCallMarkers(text)
	return text
}

func applyTextTransforms(text string, applyLinePatterns bool, trim bool) string {
	text = filterMarkdown(text, applyLinePatterns, trim)
	text = FilterEmoji(text)
	text = FilterToolCallMarkers(text)
	return text
}

func (b *TextTransformBuffer) flush() []string {
	if b.buffer == "" {
		return nil
	}
	text := applyTextTransforms(b.buffer, b.bufferIsNewline, false)
	b.buffer = ""
	b.bufferIsNewline = true
	if text == "" {
		return nil
	}
	return []string{text}
}

func appendTransformedText(out []string, text string, applyLinePatterns bool, trim bool) []string {
	text = applyTextTransforms(text, applyLinePatterns, trim)
	if text == "" {
		return out
	}
	return append(out, text)
}

func hasIncompleteMarkdownPattern(buffer string) bool {
	if buffer == "" {
		return false
	}
	if strings.HasSuffix(buffer, "#") ||
		strings.HasSuffix(buffer, "-") ||
		strings.HasSuffix(buffer, "+") ||
		strings.HasSuffix(buffer, "*") ||
		strings.HasSuffix(buffer, ">") ||
		strings.HasSuffix(buffer, "!") ||
		strings.HasSuffix(buffer, "`") ||
		strings.HasSuffix(buffer, "~") ||
		strings.HasSuffix(buffer, " ") {
		return true
	}

	doubleAsterisks := strings.Count(buffer, "**")
	if doubleAsterisks%2 == 1 {
		return true
	}
	singleAsterisks := strings.Count(buffer, "*") - doubleAsterisks*2
	if singleAsterisks%2 == 1 {
		return true
	}

	doubleUnderscores := strings.Count(buffer, "__")
	if doubleUnderscores%2 == 1 {
		return true
	}
	singleUnderscores := strings.Count(buffer, "_") - doubleUnderscores*2
	if singleUnderscores%2 == 1 {
		return true
	}

	if strings.Count(buffer, "`")%2 == 1 {
		return true
	}
	if strings.Count(buffer, "~~")%2 == 1 {
		return true
	}

	openBrackets := strings.Count(buffer, "[")
	completeLinks := len(completeLinksPattern.FindAllString(buffer, -1))
	completeImages := len(completeImagesPattern.FindAllString(buffer, -1))
	return openBrackets-completeLinks-completeImages > 0
}
