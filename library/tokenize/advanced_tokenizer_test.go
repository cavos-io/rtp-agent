package tokenize

import (
	"errors"
	"testing"

	"github.com/neurosnap/sentences"
)

func TestAdvancedSentenceTokenizerFallsBackWhenEnglishTokenizerFails(t *testing.T) {
	prev := newEnglishSentenceTokenizer
	newEnglishSentenceTokenizer = func(*sentences.Storage) (*sentences.DefaultSentenceTokenizer, error) {
		return nil, errors.New("init failed")
	}
	defer func() {
		newEnglishSentenceTokenizer = prev
	}()

	tokenizer := NewAdvancedSentenceTokenizer()
	tokens := tokenizer.Tokenize("Hello world. This fallback should still work.", "")
	if len(tokens) == 0 {
		t.Fatal("Tokenize returned no tokens")
	}
}
