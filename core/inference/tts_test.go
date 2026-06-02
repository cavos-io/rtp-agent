package inference

import (
	"testing"

	"github.com/cavos-io/rtp-agent/library/tokenize"
)

func TestNewTTSUsesConfiguredSentenceTokenizer(t *testing.T) {
	tokenizer := &recordingSentenceTokenizer{}

	provider := NewTTS("cartesia/sonic-3", "key", "secret", WithSentenceTokenizer(tokenizer))

	if got := provider.sentenceTokenizer; got != tokenizer {
		t.Fatalf("sentenceTokenizer = %T, want configured tokenizer", got)
	}
}

type recordingSentenceTokenizer struct{}

func (r *recordingSentenceTokenizer) Tokenize(text string, language string) []string {
	return []string{"custom"}
}

func (r *recordingSentenceTokenizer) Stream(language string) tokenize.SentenceStream {
	return tokenize.NewBufferedTokenStream(func(string) []string {
		return []string{"custom"}
	}, 1, 1)
}
