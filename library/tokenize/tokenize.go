package tokenize

import (
	"regexp"
	"strings"
	"unicode"
)

const punctuationChars = `!"#$%&'()*+,-./:;<=>?@[\]^_` + "`" + `{|}~±—‘’“”…`

type TokenData struct {
	SegmentID string
	Token     string
	Start     int
	End       int
}

type SentenceTokenizer interface {
	Tokenize(text string, language string) []string
	Stream(language string) SentenceStream
}

type SentenceStream interface {
	PushText(text string) error
	Flush() error
	EndInput() error
	AClose() error
	Close() error
	Next() (*TokenData, error)
	Closed() bool
}

type WordTokenizer interface {
	Tokenize(text string, language string) []string
	Stream(language string) WordStream
	FormatWords(words []string) string
}

type WordStream interface {
	PushText(text string) error
	Flush() error
	EndInput() error
	AClose() error
	Close() error
	Next() (*TokenData, error)
	Closed() bool
}

// Basic Sentence Tokenizer

type BasicSentenceTokenizer struct{}

func NewBasicSentenceTokenizer() *BasicSentenceTokenizer {
	return &BasicSentenceTokenizer{}
}

func (t *BasicSentenceTokenizer) Tokenize(text string, language string) []string {
	paragraphs := TokenizeParagraphs(text)
	tokens := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		for _, sentence := range SplitSentences(paragraph, 20, false) {
			tokens = append(tokens, sentence.Token)
		}
	}
	return tokens
}

func (t *BasicSentenceTokenizer) Stream(language string) SentenceStream {
	return NewBufferedTokenStream(func(s string) []string {
		return t.Tokenize(s, language)
	}, 20, 10)
}

// Basic Word Tokenizer

type BasicWordTokenizer struct{}

func NewBasicWordTokenizer() *BasicWordTokenizer {
	return &BasicWordTokenizer{}
}

func (t *BasicWordTokenizer) Tokenize(text string, language string) []string {
	res := SplitWords(text, true, false, false)
	tokens := make([]string, len(res))
	for i, r := range res {
		tokens[i] = r.Token
	}
	return tokens
}

func (t *BasicWordTokenizer) Stream(language string) WordStream {
	return NewBufferedTokenStream(func(s string) []string {
		return t.Tokenize(s, language)
	}, 1, 1)
}

func (t *BasicWordTokenizer) FormatWords(words []string) string {
	return strings.Join(words, " ")
}

// Sentence-splitting patterns are compiled once: SplitSentences runs on every
// streamed text chunk, so compiling them per call dominated its cost.
const (
	sentenceAlphabets = `([A-Za-z])`
	sentenceSuffixes  = `(Inc|Ltd|Jr|Sr|Co)`
	sentenceStarters  = `(Mr|Mrs|Ms|Dr|Prof|Capt|Cpt|Lt|He\s|She\s|It\s|They\s|Their\s|Our\s|We\s|But\s|However\s|That\s|This\s|Wherever)`
	sentenceAcronyms  = `([A-Z]\.[A-Z]\.(?:[A-Z]\.)?)`
)

var (
	sentencePrefixesRE      = regexp.MustCompile(`(Mr|St|Mrs|Ms|Dr)\.`)
	sentenceWebsitesRE      = regexp.MustCompile(`\.(com|net|org|io|gov|edu|me)`)
	sentenceDecimalDigitsRE = regexp.MustCompile(`([0-9])\.([0-9])`)
	sentenceMultipleDotsRE  = regexp.MustCompile(`\.{2,}`)
	sentenceSpacedInitialRE = regexp.MustCompile(`\s` + sentenceAlphabets + `\. `)
	sentenceAcronymStartRE  = regexp.MustCompile(sentenceAcronyms + ` ` + sentenceStarters)
	sentenceThreeInitialsRE = regexp.MustCompile(sentenceAlphabets + `\.` + sentenceAlphabets + `\.` + sentenceAlphabets + `\.`)
	sentenceTwoInitialsRE   = regexp.MustCompile(sentenceAlphabets + `\.` + sentenceAlphabets + `\.`)
	sentenceSuffixStartRE   = regexp.MustCompile(` ` + sentenceSuffixes + `\. ` + sentenceStarters)
	sentenceSuffixRE        = regexp.MustCompile(` ` + sentenceSuffixes + `\.`)
	sentenceInitialRE       = regexp.MustCompile(` ` + sentenceAlphabets + `\.`)
	sentenceQuoteEndRE      = regexp.MustCompile(`([.!?。！？])(["”])`)
	paragraphBreakRE        = regexp.MustCompile(`\n\s*\n`)
)

func SplitSentences(text string, minSentenceLen int, retainFormat bool) []TokenData {
	if retainFormat {
		text = strings.ReplaceAll(text, "\n", "<nel><stop>")
	} else {
		text = strings.ReplaceAll(text, "\n", " ")
	}

	text = sentencePrefixesRE.ReplaceAllString(text, "${1}<prd>")
	text = sentenceWebsitesRE.ReplaceAllString(text, "<prd>${1}")
	text = sentenceDecimalDigitsRE.ReplaceAllString(text, "${1}<prd>${2}")

	text = sentenceMultipleDotsRE.ReplaceAllStringFunc(text, func(match string) string {
		return strings.Repeat("<prd>", len(match))
	})

	if strings.Contains(text, "Ph.D") {
		text = strings.ReplaceAll(text, "Ph.D.", "Ph<prd>D<prd>")
	}

	text = sentenceSpacedInitialRE.ReplaceAllString(text, " ${1}<prd> ")
	text = sentenceAcronymStartRE.ReplaceAllString(text, "${1}<stop> ${2}")
	text = sentenceThreeInitialsRE.ReplaceAllString(text, "${1}<prd>${2}<prd>${3}<prd>")
	text = sentenceTwoInitialsRE.ReplaceAllString(text, "${1}<prd>${2}<prd>")
	text = sentenceSuffixStartRE.ReplaceAllString(text, " ${1}<stop> ${2}")
	text = sentenceSuffixRE.ReplaceAllString(text, " ${1}<prd>")
	text = sentenceInitialRE.ReplaceAllString(text, " ${1}<prd>")

	text = sentenceQuoteEndRE.ReplaceAllString(text, "${1}${2}<stop>")
	text = addStopAfterSentencePunctuation(text)

	text = strings.ReplaceAll(text, "<prd>", ".")

	if retainFormat {
		text = strings.ReplaceAll(text, "<nel>", "\n")
	}

	splittedSentences := strings.Split(text, "<stop>")
	text = strings.ReplaceAll(text, "<stop>", "")

	var sentences []TokenData
	buff := ""
	startPos := 0
	endPos := 0
	prePad := " "
	if retainFormat {
		prePad = ""
	}

	for _, match := range splittedSentences {
		sentence := match
		if !retainFormat {
			sentence = strings.TrimSpace(match)
		}
		if sentence == "" {
			continue
		}

		buff += prePad + sentence
		endPos += len([]rune(match))
		if len([]rune(buff)) > minSentenceLen {
			prefixLen := len(prePad)
			if len(buff) >= prefixLen {
				sentences = append(sentences, TokenData{Token: buff[prefixLen:], Start: startPos, End: endPos})
			}
			startPos = endPos
			buff = ""
		}
	}

	if buff != "" {
		prefixLen := len(prePad)
		if len(buff) >= prefixLen {
			sentences = append(sentences, TokenData{Token: buff[prefixLen:], Start: startPos, End: len([]rune(text)) - 1})
		}
	}

	return sentences
}

func addStopAfterSentencePunctuation(text string) string {
	var builder strings.Builder
	runes := []rune(text)
	for i, char := range runes {
		builder.WriteRune(char)
		if char != '.' && char != '!' && char != '?' && char != '。' && char != '！' && char != '？' {
			continue
		}
		if i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '”') {
			continue
		}
		builder.WriteString("<stop>")
	}
	return builder.String()
}

func SplitWords(text string, ignorePunctuation bool, splitCharacter bool, retainFormat bool) []TokenData {
	var words []TokenData
	var charBasedCodes *regexp.Regexp
	if splitCharacter {
		charBasedCodes = regexp.MustCompile(`[\x{4e00}-\x{9fff}\x{3040}-\x{30ff}\x{3400}-\x{4dbf}\x{0E00}-\x{0E7F}]`)
	}

	wordStartByte := 0
	wordStartRune := 0

	addCurrentWord := func(startByte, endByte, startRune, endRune int) {
		word := text[startByte:endByte]
		if ignorePunctuation {
			word = stripPunctuation(word)
		}
		if word != "" {
			words = append(words, TokenData{Token: word, Start: startRune, End: endRune})
		}
	}

	runePos := 0
	for bytePos, char := range text {
		if unicode.IsSpace(char) {
			if retainFormat && strings.TrimSpace(text[wordStartByte:bytePos]) == "" {
				runePos++
				continue
			}
			addCurrentWord(wordStartByte, bytePos, wordStartRune, runePos)
			if retainFormat {
				wordStartByte = bytePos
				wordStartRune = runePos
			} else {
				wordStartByte = bytePos + len(string(char))
				wordStartRune = runePos + 1
			}
		} else if charBasedCodes != nil && charBasedCodes.MatchString(string(char)) {
			if wordStartByte < bytePos {
				addCurrentWord(wordStartByte, bytePos, wordStartRune, runePos)
			}
			nextBytePos := bytePos + len(string(char))
			addCurrentWord(bytePos, nextBytePos, runePos, runePos+1)
			wordStartByte = nextBytePos
			wordStartRune = runePos + 1
		}
		runePos++
	}

	addCurrentWord(wordStartByte, len(text), wordStartRune, runePos)

	return words
}

func ReplaceWords(text string, replacements map[string]string) string {
	if text == "" || len(replacements) == 0 {
		return text
	}

	normalized := make(map[string]string, len(replacements))
	for word, replacement := range replacements {
		normalized[strings.ToLower(word)] = replacement
	}

	words := SplitWords(text, false, false, false)
	var builder strings.Builder
	lastIndex := 0
	for _, word := range words {
		noPunctuation := strings.TrimRight(word.Token, punctuationChars)
		replacement, ok := normalized[strings.ToLower(noPunctuation)]
		if !ok || noPunctuation == "" {
			continue
		}

		punctuationOffset := len(word.Token) - len(noPunctuation)
		wordStart := runeOffsetToByteOffset(text, word.Start)
		wordEnd := runeOffsetToByteOffset(text, word.End)
		builder.WriteString(text[lastIndex:wordStart])
		builder.WriteString(replacement)
		builder.WriteString(text[wordEnd-punctuationOffset : wordEnd])
		lastIndex = wordEnd
	}

	if lastIndex == 0 {
		return text
	}
	builder.WriteString(text[lastIndex:])
	return builder.String()
}

func runeOffsetToByteOffset(text string, offset int) int {
	if offset <= 0 {
		return 0
	}
	runePos := 0
	for bytePos := range text {
		if runePos == offset {
			return bytePos
		}
		runePos++
	}
	return len(text)
}

func byteOffsetToRuneOffset(text string, offset int) int {
	if offset <= 0 {
		return 0
	}
	if offset >= len(text) {
		return len([]rune(text))
	}
	runePos := 0
	for bytePos := range text {
		if bytePos >= offset {
			return runePos
		}
		runePos++
	}
	return len([]rune(text))
}

func SplitParagraphs(text string) []TokenData {
	pattern := paragraphBreakRE
	indices := pattern.FindAllStringIndex(text, -1)

	var paragraphs []TokenData
	start := 0

	if len(indices) == 0 {
		stripped := strings.TrimSpace(text)
		if stripped == "" {
			return paragraphs
		}
		startIndex := strings.Index(text, stripped)
		startRune := byteOffsetToRuneOffset(text, startIndex)
		return []TokenData{{Token: stripped, Start: startRune, End: startRune + len([]rune(stripped))}}
	}

	for _, idx := range indices {
		end := idx[0]
		paragraph := strings.TrimSpace(text[start:end])
		if paragraph != "" {
			paraStart := start + strings.Index(text[start:end], paragraph)
			paraStartRune := byteOffsetToRuneOffset(text, paraStart)
			paraEndRune := paraStartRune + len([]rune(paragraph))
			paragraphs = append(paragraphs, TokenData{Token: paragraph, Start: paraStartRune, End: paraEndRune})
		}
		start = idx[1]
	}

	lastParagraph := strings.TrimSpace(text[start:])
	if lastParagraph != "" {
		paraStart := start + strings.Index(text[start:], lastParagraph)
		paraStartRune := byteOffsetToRuneOffset(text, paraStart)
		paraEndRune := paraStartRune + len([]rune(lastParagraph))
		paragraphs = append(paragraphs, TokenData{Token: lastParagraph, Start: paraStartRune, End: paraEndRune})
	}

	return paragraphs
}

func TokenizeParagraphs(text string) []string {
	paragraphs := SplitParagraphs(text)
	tokens := make([]string, len(paragraphs))
	for i, paragraph := range paragraphs {
		tokens[i] = paragraph.Token
	}
	return tokens
}

func stripPunctuation(s string) string {
	var result strings.Builder
	for _, r := range s {
		if !strings.ContainsRune(punctuationChars, r) {
			result.WriteRune(r)
		}
	}
	return result.String()
}
