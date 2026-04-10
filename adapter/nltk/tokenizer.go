package nltk

import (
	"strings"

	"github.com/cavos-io/rtp-agent/library/tokenize"
)

type SentenceTokenizer struct {
	Language         string
	MinSentenceLen   int
	StreamContextLen int
}

func NewSentenceTokenizer(language string, minSentenceLen, streamContextLen int) *SentenceTokenizer {
	if language == "" {
		language = "english"
	}
	if minSentenceLen == 0 {
		minSentenceLen = 20
	}
	if streamContextLen == 0 {
		streamContextLen = 10
	}
	return &SentenceTokenizer{
		Language:         language,
		MinSentenceLen:   minSentenceLen,
		StreamContextLen: streamContextLen,
	}
}

func (t *SentenceTokenizer) Tokenize(text string, language string) []string {
	// Fallback to library regex-based tokenization as NLTK is a python native module
	sentences := tokenize.SplitSentences(text, 20, false)
	
	var newSentences []string
	var buff string

	for _, sentence := range sentences {
		buff += sentence.Token + " "
		if len(buff)-1 >= t.MinSentenceLen {
			newSentences = append(newSentences, strings.TrimRight(buff, " "))
			buff = ""
		}
	}

	if buff != "" {
		newSentences = append(newSentences, strings.TrimRight(buff, " "))
	}

	return newSentences
}

func (t *SentenceTokenizer) Stream(language string) tokenize.SentenceStream {
	return tokenize.NewBufferedTokenStream(func(s string) []string {
		return t.Tokenize(s, language)
	}, t.MinSentenceLen, t.StreamContextLen)
}
