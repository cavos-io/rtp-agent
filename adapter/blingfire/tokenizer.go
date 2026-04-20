package blingfire

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
	sentences := TextToSentences(text)
	
	var newSentences []string
	var buff string

	for _, sentence := range sentences {
		buff += sentence + " "
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

type WordTokenizer struct {
	Language string
}

func NewWordTokenizer(language string) *WordTokenizer {
	if language == "" {
		language = "english"
	}
	return &WordTokenizer{
		Language: language,
	}
}

func (t *WordTokenizer) Tokenize(text string, language string) []string {
	return TextToWords(text)
}

func (t *WordTokenizer) Stream(language string) tokenize.WordStream {
	return tokenize.NewBufferedTokenStream(func(s string) []string {
		return t.Tokenize(s, language)
	}, 1, 1)
}

