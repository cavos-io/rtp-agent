package blingfire

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

	expected := "Hello world."
	if sentences[0] != expected {
		t.Errorf("Expected '%s', got '%s'", expected, sentences[0])
	}
}

func TestWordTokenizer_Tokenize(t *testing.T) {
	tokenizer := NewWordTokenizer("english")
	text := "Hello world"
	words := tokenizer.Tokenize(text, "english")

	if len(words) != 2 {
		t.Errorf("Expected 2 words, got %d", len(words))
	}

	if words[0] != "Hello" || words[1] != "world" {
		t.Errorf("Unexpected words: %v", words)
	}
}
