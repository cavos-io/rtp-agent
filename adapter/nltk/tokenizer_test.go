package nltk

import (
	"testing"
)

func TestSentenceTokenizer_Tokenize(t *testing.T) {
	tokenizer := NewSentenceTokenizer("english", 10, 5)
	text := "Hello world. This is a test. Another sentence."
	sentences := tokenizer.Tokenize(text, "english")

	if len(sentences) == 0 {
		t.Fatalf("Expected sentences, got none")
	}

	// Based on tokenize.SplitSentences behavior
	if sentences[0] == "" {
		t.Errorf("Expected non-empty sentence")
	}
}
