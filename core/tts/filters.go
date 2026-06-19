package tts

import (
	"regexp"
	"sort"
	"strings"

	"github.com/cavos-io/rtp-agent/library/tokenize"
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

	completeLinksPattern        = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`)
	completeImagesPattern       = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	inlineSplitTokens           = " ,.?!;，。？！；"
	replacementPunctuationChars = `!"#$%&'()*+,-./:;<=>?@[\]^_` + "`" + `{|}~±—‘’“”…`
)

type TextTransformBuffer struct {
	buffer          string
	bufferIsNewline bool
	transforms      []string
}

type TextReplaceBuffer struct {
	replacements  []textReplacement
	caseSensitive bool
	buffer        string
}

type TextRegexReplaceBuffer struct {
	replacements  []textReplacement
	caseSensitive bool
	buffer        string
	tailLen       int
}

type TextReplacement struct {
	Old string
	New string
}

type textReplacement struct {
	old string
	new string
}

type invalidTextTransformError struct {
	transform string
}

func (e invalidTextTransformError) Error() string {
	return "Invalid transform: " + e.transform + ", available transforms: [filter_markdown filter_emoji]"
}

func NewTextTransformBuffer() *TextTransformBuffer {
	return &TextTransformBuffer{
		bufferIsNewline: true,
		transforms:      []string{"filter_markdown", "filter_emoji"},
	}
}

func NewTextTransformBufferWithTransforms(transforms []string) (*TextTransformBuffer, error) {
	compiled, err := validateTextTransforms(transforms)
	if err != nil {
		return nil, err
	}
	return &TextTransformBuffer{
		bufferIsNewline: true,
		transforms:      compiled,
	}, nil
}

func NewTextReplaceBuffer(replacements map[string]string, caseSensitive bool) *TextReplaceBuffer {
	keys := make([]string, 0, len(replacements))
	for old := range replacements {
		keys = append(keys, old)
	}
	sort.Strings(keys)

	ordered := make([]textReplacement, 0, len(keys))
	for _, old := range keys {
		ordered = append(ordered, textReplacement{old: old, new: replacements[old]})
	}

	return &TextReplaceBuffer{
		replacements:  ordered,
		caseSensitive: caseSensitive,
	}
}

func NewTextRegexReplaceBuffer(replacements map[string]string, caseSensitive bool) *TextRegexReplaceBuffer {
	ordered := orderedTextReplacements(replacements)
	return newTextRegexReplaceBuffer(ordered, caseSensitive)
}

func NewOrderedTextRegexReplaceBuffer(replacements []TextReplacement, caseSensitive bool) *TextRegexReplaceBuffer {
	ordered := make([]textReplacement, 0, len(replacements))
	for _, replacement := range replacements {
		ordered = append(ordered, textReplacement{old: replacement.Old, new: replacement.New})
	}
	return newTextRegexReplaceBuffer(ordered, caseSensitive)
}

func newTextRegexReplaceBuffer(ordered []textReplacement, caseSensitive bool) *TextRegexReplaceBuffer {
	tailLen := 0
	for _, replacement := range ordered {
		if len(replacement.old) > tailLen {
			tailLen = len(replacement.old)
		}
	}
	if tailLen > 0 {
		tailLen--
	}
	return &TextRegexReplaceBuffer{
		replacements:  ordered,
		caseSensitive: caseSensitive,
		tailLen:       tailLen,
	}
}

func orderedTextReplacements(replacements map[string]string) []textReplacement {
	keys := make([]string, 0, len(replacements))
	for old := range replacements {
		keys = append(keys, old)
	}
	sort.Strings(keys)

	ordered := make([]textReplacement, 0, len(keys))
	for _, old := range keys {
		ordered = append(ordered, textReplacement{old: old, new: replacements[old]})
	}
	return ordered
}

func (b *TextTransformBuffer) Push(text string) []string {
	if text == "" {
		return nil
	}
	if !b.hasMarkdownTransform() {
		text = b.applyTextTransforms(text, b.bufferIsNewline, false)
		b.bufferIsNewline = strings.HasSuffix(text, "\n")
		if text == "" {
			return nil
		}
		return []string{text}
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
			out = b.appendTransformedText(out, line+"\n", isNewline, false)
		}
		b.bufferIsNewline = true
		return out
	}

	lastSplitPos := strings.LastIndexAny(b.buffer, inlineSplitTokens)
	if lastSplitPos >= 1 {
		splitEnd := lastSplitPos
		processable := b.buffer[:splitEnd]
		rest := b.buffer[splitEnd:]
		if !b.hasMarkdownTransform() || !hasIncompleteMarkdownPattern(processable) {
			b.buffer = rest
			out := b.appendTransformedText(nil, processable, b.bufferIsNewline, false)
			b.bufferIsNewline = false
			return out
		}
	}

	return nil
}

func (b *TextRegexReplaceBuffer) Push(text string) []string {
	if text == "" {
		return nil
	}
	b.buffer += text
	if len(b.replacements) == 0 {
		out := b.buffer
		b.buffer = ""
		return []string{out}
	}
	if len(b.buffer) <= b.tailLen {
		return nil
	}

	b.buffer = b.apply(b.buffer)
	flushTo := len(b.buffer) - b.tailLen
	if flushTo < 0 {
		flushTo = len(b.buffer) + flushTo
		if flushTo < 0 {
			flushTo = 0
		}
	}
	out := b.buffer[:flushTo]
	b.buffer = b.buffer[flushTo:]
	if out == "" {
		return nil
	}
	return []string{out}
}

func (b *TextReplaceBuffer) Push(text string) []string {
	if text == "" {
		return nil
	}
	b.buffer += text
	if len(b.replacements) == 0 {
		out := b.buffer
		b.buffer = ""
		return []string{out}
	}

	words := tokenize.SplitWords(b.buffer, false, false, false)
	if len(words) <= 1 {
		return nil
	}

	flushTo := words[len(words)-2].End
	processable := b.buffer[:flushTo]
	rest := b.buffer[flushTo:]
	out := b.apply(processable)
	b.buffer = rest
	if out == "" {
		return nil
	}
	return []string{out}
}

func (b *TextRegexReplaceBuffer) Flush() []string {
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

func (b *TextRegexReplaceBuffer) apply(text string) string {
	for _, replacement := range b.replacements {
		if replacement.old == "" {
			continue
		}
		if b.caseSensitive {
			text = strings.ReplaceAll(text, replacement.old, replacement.new)
			continue
		}
		pattern := regexp.MustCompile("(?i)" + regexp.QuoteMeta(replacement.old))
		text = pattern.ReplaceAllStringFunc(text, func(string) string {
			return replacement.new
		})
	}
	return text
}

func (b *TextReplaceBuffer) apply(text string) string {
	if !b.caseSensitive {
		replacements := make(map[string]string, len(b.replacements))
		for _, replacement := range b.replacements {
			if replacement.old == "" {
				continue
			}
			replacements[replacement.old] = replacement.new
		}
		return tokenize.ReplaceWords(text, replacements)
	}

	for _, replacement := range b.replacements {
		if replacement.old == "" {
			continue
		}
		text = replaceCaseSensitiveWord(text, replacement.old, replacement.new)
	}
	return text
}

func replaceCaseSensitiveWord(text, old, replacement string) string {
	words := tokenize.SplitWords(text, false, false, false)
	var builder strings.Builder
	lastIndex := 0
	for _, word := range words {
		noPunctuation := strings.TrimRight(word.Token, replacementPunctuationChars)
		if noPunctuation != old || noPunctuation == "" {
			continue
		}

		punctuationOffset := len(word.Token) - len(noPunctuation)
		builder.WriteString(text[lastIndex:word.Start])
		builder.WriteString(replacement)
		builder.WriteString(text[word.End-punctuationOffset : word.End])
		lastIndex = word.End
	}

	if lastIndex == 0 {
		return text
	}
	builder.WriteString(text[lastIndex:])
	return builder.String()
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
	return applyTextTransforms(text, true, true)
}

func ApplyTextTransformsWithTransforms(text string, transforms []string) (string, error) {
	compiled, err := validateTextTransforms(transforms)
	if err != nil {
		return "", err
	}
	return applyNamedTextTransforms(text, compiled, true, true), nil
}

func applyTextTransforms(text string, applyLinePatterns bool, trim bool) string {
	return applyNamedTextTransforms(text, []string{"filter_markdown", "filter_emoji"}, applyLinePatterns, trim)
}

func (b *TextTransformBuffer) flush() []string {
	if b.buffer == "" {
		return nil
	}
	text := b.applyTextTransforms(b.buffer, b.bufferIsNewline, false)
	b.buffer = ""
	b.bufferIsNewline = true
	if text == "" {
		return nil
	}
	return []string{text}
}

func (b *TextTransformBuffer) appendTransformedText(out []string, text string, applyLinePatterns bool, trim bool) []string {
	text = b.applyTextTransforms(text, applyLinePatterns, trim)
	if text == "" {
		return out
	}
	return append(out, text)
}

func (b *TextTransformBuffer) applyTextTransforms(text string, applyLinePatterns bool, trim bool) string {
	return applyNamedTextTransforms(text, b.transforms, applyLinePatterns, trim)
}

func (b *TextTransformBuffer) hasMarkdownTransform() bool {
	for _, transform := range b.transforms {
		if transform == "filter_markdown" {
			return true
		}
	}
	return false
}

func validateTextTransforms(transforms []string) ([]string, error) {
	compiled := append([]string(nil), transforms...)
	for _, transform := range compiled {
		switch transform {
		case "filter_markdown", "filter_emoji":
		default:
			return nil, invalidTextTransformError{transform: transform}
		}
	}
	return compiled, nil
}

func applyNamedTextTransforms(text string, transforms []string, applyLinePatterns bool, trim bool) string {
	for _, transform := range transforms {
		switch transform {
		case "filter_markdown":
			text = filterMarkdown(text, applyLinePatterns, trim)
		case "filter_emoji":
			text = FilterEmoji(text)
		case "filter_tool_call_markers":
			text = FilterToolCallMarkers(text)
		}
	}
	return text
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
