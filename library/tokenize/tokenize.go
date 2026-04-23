package tokenize

import (
	"regexp"
	"strings"
	"unicode"
)

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
	Close() error
	Next() (*TokenData, error)
}

type WordTokenizer interface {
	Tokenize(text string, language string) []string
	Stream(language string) WordStream
}

type WordStream interface {
	PushText(text string) error
	Flush() error
	Close() error
	Next() (*TokenData, error)
}

// Basic Sentence Tokenizer

type BasicSentenceTokenizer struct{}

func NewBasicSentenceTokenizer() *BasicSentenceTokenizer {
	return &BasicSentenceTokenizer{}
}

func (t *BasicSentenceTokenizer) Tokenize(text string, language string) []string {
	res := SplitSentences(text, 20, false)
	tokens := make([]string, len(res))
	for i, r := range res {
		tokens[i] = r.Token
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

func SplitSentences(text string, minSentenceLen int, retainFormat bool) []TokenData {
	alphabets := regexp.MustCompile(`([A-Za-z])`)
	prefixes := regexp.MustCompile(`(Mr|St|Mrs|Ms|Dr|Prof|Capt|Cpt|Lt|Col|Gen|Rep|Sen|Gov|Sr|Jr|Maj|Sgt|Adm|Rev|Hon)\.`)
	suffixes := regexp.MustCompile(`(Inc|Ltd|Jr|Sr|Co|Corp|LLC)`)
	starters := regexp.MustCompile(`(Mr|Mrs|Ms|Dr|Prof|Capt|Cpt|Lt|He\s|She\s|It\s|They\s|Their\s|Our\s|We\s|But\s|However\s|That\s|This\s|Wherever|Moreover|Furthermore|Therefore|Consequently)`)
	acronyms := regexp.MustCompile(`([A-Z]\.[A-Z]\.(?:[A-Z]\.)?)`)
	websites := regexp.MustCompile(`\.(com|net|org|io|gov|edu|me|info|biz|dev|ai)`)
	digits := regexp.MustCompile(`([0-9])`)
	multipleDots := regexp.MustCompile(`\.{2,}`)

	if retainFormat {
		text = strings.ReplaceAll(text, "\n", "<nel><stop>")
	} else {
		text = strings.ReplaceAll(text, "\n", " ")
	}

	text = prefixes.ReplaceAllString(text, "${1}<prd>")
	text = websites.ReplaceAllString(text, "<prd>${1}")
	text = digits.ReplaceAllString(text, "${1}<prd>${2}")
	
	text = multipleDots.ReplaceAllStringFunc(text, func(match string) string {
		return strings.Repeat("<prd>", len(match))
	})
	
	if strings.Contains(text, "Ph.D") {
		text = strings.ReplaceAll(text, "Ph.D.", "Ph<prd>D<prd>")
	}

	text = regexp.MustCompile(`\s`+alphabets.String()+`\. `).ReplaceAllString(text, " ${1}<prd> ")
	text = regexp.MustCompile(acronyms.String()+` `+starters.String()).ReplaceAllString(text, "${1}<stop> ${2}")
	text = regexp.MustCompile(alphabets.String()+`\.`+alphabets.String()+`\.`+alphabets.String()+`\.`).ReplaceAllString(text, "${1}<prd>${2}<prd>${3}<prd>")
	text = regexp.MustCompile(alphabets.String()+`\.`+alphabets.String()+`\.`).ReplaceAllString(text, "${1}<prd>${2}<prd>")
	text = regexp.MustCompile(` `+suffixes.String()+`\. `+starters.String()).ReplaceAllString(text, " ${1}<stop> ${2}")
	text = regexp.MustCompile(` `+suffixes.String()+`\.`).ReplaceAllString(text, " ${1}<prd>")
	text = regexp.MustCompile(` `+alphabets.String()+`\.`).ReplaceAllString(text, " ${1}<prd>")

	// Han, Hiragana, Katakana, Thai punctuation
	text = regexp.MustCompile(`([。！？])`).ReplaceAllString(text, "${1}<stop>")
	
	// Common English punctuation - handle quotes separately to avoid lookahead
	text = regexp.MustCompile(`([.!?])(["”])`).ReplaceAllString(text, "${1}${2}<stop>")
	// Use a two-step process to mark remaining sentence ends
	text = regexp.MustCompile(`([.!?])`).ReplaceAllString(text, "${1}<stop>")
	// Remove <stop> if it was followed by a quote (which was already handled)
	text = strings.ReplaceAll(text, "<stop>\"", "\"<stop>")
	text = strings.ReplaceAll(text, "<stop>”", "”<stop>")
	// Fix potential double stops
	text = strings.ReplaceAll(text, "<stop><stop>", "<stop>")

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
		endPos += len(match)
		if len(buff) > minSentenceLen {
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
			sentences = append(sentences, TokenData{Token: buff[prefixLen:], Start: startPos, End: len(text) - 1})
		}
	}

	return sentences
}

func SplitWords(text string, ignorePunctuation bool, splitCharacter bool, retainFormat bool) []TokenData {
	var words []TokenData
	var charBasedCodes *regexp.Regexp
	if splitCharacter {
		charBasedCodes = regexp.MustCompile(`[\x{4e00}-\x{9fff}\x{3040}-\x{30ff}\x{3400}-\x{4dbf}\x{0E00}-\x{0E7F}]`)
	}

	wordStart := 0

	addCurrentWord := func(start, end int) {
		word := text[start:end]
		if ignorePunctuation {
			word = stripPunctuation(word)
		}
		if word != "" {
			words = append(words, TokenData{Token: word, Start: start, End: end})
		}
	}

	runes := []rune(text)
	for pos, char := range runes {
		if unicode.IsSpace(char) {
			if retainFormat && strings.TrimSpace(string(runes[wordStart:pos])) == "" {
				continue
			}
			addCurrentWord(wordStart, pos)
			if retainFormat {
				wordStart = pos
			} else {
				wordStart = pos + 1
			}
		} else if charBasedCodes != nil && charBasedCodes.MatchString(string(char)) {
			if wordStart < pos {
				addCurrentWord(wordStart, pos)
			}
			addCurrentWord(pos, pos+1)
			wordStart = pos + 1
		}
	}

	addCurrentWord(wordStart, len(runes))

	return words
}

func SplitParagraphs(text string) []TokenData {
	pattern := regexp.MustCompile(`\n\s*\n`)
	indices := pattern.FindAllStringIndex(text, -1)

	var paragraphs []TokenData
	start := 0

	if len(indices) == 0 {
		stripped := strings.TrimSpace(text)
		if stripped == "" {
			return paragraphs
		}
		startIndex := strings.Index(text, stripped)
		return []TokenData{{Token: stripped, Start: startIndex, End: startIndex + len(stripped)}}
	}

	for _, idx := range indices {
		end := idx[0]
		paragraph := strings.TrimSpace(text[start:end])
		if paragraph != "" {
			paraStart := start + strings.Index(text[start:end], paragraph)
			paraEnd := paraStart + len(paragraph)
			paragraphs = append(paragraphs, TokenData{Token: paragraph, Start: paraStart, End: paraEnd})
		}
		start = idx[1]
	}

	lastParagraph := strings.TrimSpace(text[start:])
	if lastParagraph != "" {
		paraStart := start + strings.Index(text[start:], lastParagraph)
		paraEnd := paraStart + len(lastParagraph)
		paragraphs = append(paragraphs, TokenData{Token: lastParagraph, Start: paraStart, End: paraEnd})
	}

	return paragraphs
}

func stripPunctuation(s string) string {
	var result strings.Builder
	for _, r := range s {
		if !unicode.IsPunct(r) {
			result.WriteRune(r)
		}
	}
	return result.String()
}

