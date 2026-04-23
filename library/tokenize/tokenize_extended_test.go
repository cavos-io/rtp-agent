package tokenize

import (
	"testing"
	"time"
)

func TestSplitSentences_Extended(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected []string
	}{
		{
			"Basic sentence",
			"Hello world. How are you?",
			[]string{"Hello world.", "How are you?"},
		},
		{
			"Acronyms and prefixes",
			"Mr. Smith went to Washington D.C. with Dr. Brown.",
			[]string{"Mr. Smith went to Washington D.C. with Dr. Brown."},
		},
		{
			"Websites",
			"Visit example.com for more info.",
			[]string{"Visit example.com for more info."},
		},
		{
			"Quotes",
			"She said, \"Hello!\" Then she left.",
			[]string{"She said, \"Hello!\"", "Then she left."},
		},
		{
			"Multiple dots",
			"Wait for it... Here it is.",
			[]string{"Wait for it... Here it is."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := SplitSentences(tt.text, 5, false)
			if len(res) != len(tt.expected) {
				t.Fatalf("Expected %d sentences, got %d. Result: %v", len(tt.expected), len(res), res)
			}
			for i, s := range res {
				if s.Token != tt.expected[i] {
					t.Errorf("Expected %q, got %q", tt.expected[i], s.Token)
				}
			}
		})
	}
}

func TestSplitWords_Extended(t *testing.T) {
	text := "Hello, world! This is a test."
	words := SplitWords(text, true, false, false)
	
	expected := []string{"Hello", "world", "This", "is", "a", "test"}
	if len(words) != len(expected) {
		t.Errorf("Expected %d words, got %d", len(expected), len(words))
	}
	for i, w := range words {
		if w.Token != expected[i] {
			t.Errorf("Expected %q, got %q", expected[i], w.Token)
		}
	}
}

func TestSplitParagraphs(t *testing.T) {
	text := "Para 1\n\nPara 2\n\nPara 3"
	paras := SplitParagraphs(text)
	
	if len(paras) != 3 {
		t.Errorf("Expected 3 paragraphs, got %d", len(paras))
	}
	if paras[0].Token != "Para 1" || paras[1].Token != "Para 2" || paras[2].Token != "Para 3" {
		t.Errorf("Unexpected paragraphs: %v", paras)
	}
}

func TestBasicTokenizer_Stream(t *testing.T) {
	tk := NewBasicSentenceTokenizer()
	stream := tk.Stream("en")
	
	stream.PushText("First sentence. Second sentence.")
	err := stream.Flush()
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}
	
	type res struct {
		tok *TokenData
		err error
	}
	ch := make(chan res, 1)
	go func() {
		tok, err := stream.Next()
		ch <- res{tok, err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("Next failed: %v", r.err)
		}
		if r.tok.Token != "First sentence. Second sentence." {
			t.Errorf("Unexpected token: %q", r.tok.Token)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for token")
	}
	
	stream.Close()
}
