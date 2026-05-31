package tokenize

import "testing"

func TestSplitSentencesPreservesDecimalNumbers(t *testing.T) {
	tokens := NewBasicSentenceTokenizer().Tokenize("Version 1.5 is ready. Next sentence.", "")

	if len(tokens) != 2 {
		t.Fatalf("tokens = %#v, want 2 sentences", tokens)
	}
	if tokens[0] != "Version 1.5 is ready." {
		t.Fatalf("first token = %q, want decimal preserved in first sentence", tokens[0])
	}
	if tokens[1] != "Next sentence." {
		t.Fatalf("second token = %q, want next sentence", tokens[1])
	}
}

func TestSplitSentencesKeepsClosingQuoteAfterCJKPunctuation(t *testing.T) {
	tokens := NewBasicSentenceTokenizer().Tokenize("他说：“你好。” 下一句。", "")

	if len(tokens) != 2 {
		t.Fatalf("tokens = %#v, want 2 sentences", tokens)
	}
	if tokens[0] != "他说：“你好。”" {
		t.Fatalf("first token = %q, want closing quote in first sentence", tokens[0])
	}
	if tokens[1] != "下一句。" {
		t.Fatalf("second token = %q, want next sentence", tokens[1])
	}
}

func TestSplitWordsSplitsCharacterBasedUnicode(t *testing.T) {
	tokens := SplitWords("你好 world", true, true, false)

	got := make([]string, len(tokens))
	for i, token := range tokens {
		got[i] = token.Token
	}

	want := []string{"你", "好", "world"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokens = %#v, want %#v", got, want)
		}
	}
}

func TestSplitWordsStripsReferencePunctuationList(t *testing.T) {
	tokens := SplitWords("±value… kept", true, false, false)

	got := make([]string, len(tokens))
	for i, token := range tokens {
		got[i] = token.Token
	}

	want := []string{"value", "kept"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokens = %#v, want %#v", got, want)
		}
	}
}

func TestReplaceWordsIsCaseInsensitiveAndPreservesPunctuation(t *testing.T) {
	got := ReplaceWords("Hello, WORLD! workflow stays.", map[string]string{
		"hello": "hi",
		"world": "there",
		"flow":  "stream",
	})

	want := "hi, there! workflow stays."
	if got != want {
		t.Fatalf("ReplaceWords() = %q, want %q", got, want)
	}
}
