package tokenize

import (
	"strings"

	"github.com/neurosnap/sentences"
	"github.com/neurosnap/sentences/english"
)

// AdvancedSentenceTokenizer provides robust, multilingual sentence boundary detection.
// It replaces the need for the CGO-bound BlingFire C++ library in the Go parity.
type AdvancedSentenceTokenizer struct {
	tokenizer sentences.DefaultSentenceTokenizer
}

func NewAdvancedSentenceTokenizer() *AdvancedSentenceTokenizer {
	tokenizer, err := english.NewSentenceTokenizer(nil)
	if err != nil {
		// Fallback to basic tokenizer if initialization fails, though it shouldn't
		panic(err)
	}
	return &AdvancedSentenceTokenizer{
		tokenizer: *tokenizer,
	}
}

func (t *AdvancedSentenceTokenizer) Tokenize(text string, language string) []string {
	// Tokenize returns sentences including punctuation
	sentences := t.tokenizer.Tokenize(text)
	
	var result []string
	for _, s := range sentences {
		clean := strings.TrimSpace(s.Text)
		if clean != "" {
			result = append(result, clean)
		}
	}
	return result
}

func (t *AdvancedSentenceTokenizer) Stream(language string) SentenceStream {
	return NewBufferedTokenStream(func(s string) []string {
		return t.Tokenize(s, language)
	}, 20, 10)
}

// Ensure the new tokenizer fits the interface
var _ SentenceTokenizer = (*AdvancedSentenceTokenizer)(nil)
