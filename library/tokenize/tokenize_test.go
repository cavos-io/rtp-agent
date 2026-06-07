package tokenize

import (
	"reflect"
	"testing"
)

func TestBasicWordTokenizerFormatWordsJoinsWithSpaces(t *testing.T) {
	tokenizer := NewBasicWordTokenizer()
	if got := tokenizer.FormatWords([]string{"hello", "world"}); got != "hello world" {
		t.Fatalf("FormatWords() = %q, want hello world", got)
	}
}

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

	if len(tokens) != 1 {
		t.Fatalf("tokens = %#v, want buffered short CJK sentence", tokens)
	}
	if tokens[0] != "他说：“你好。” 下一句。" {
		t.Fatalf("token = %q, want closing quote preserved and short CJK fragments buffered", tokens[0])
	}
}

func TestSplitSentencesUsesRuneLengthForReferenceMinimum(t *testing.T) {
	tokens := SplitSentences("短句一。 短句二。", 20, false)

	if len(tokens) != 1 {
		t.Fatalf("tokens = %#v, want one token while rune length remains under minimum", tokens)
	}
	if tokens[0].Token != "短句一。 短句二。" {
		t.Fatalf("token = %q, want CJK fragments buffered by rune length", tokens[0].Token)
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

func TestSplitParagraphsSplitsOnBlankLinesAndTracksOffsets(t *testing.T) {
	tokens := SplitParagraphs("  first paragraph\n\n \t\n second paragraph  \nthird line\n\n")

	want := []TokenData{
		{Token: "first paragraph", Start: 2, End: 17},
		{Token: "second paragraph  \nthird line", Start: 23, End: 52},
	}
	if len(tokens) != len(want) {
		t.Fatalf("tokens = %#v, want %#v", tokens, want)
	}
	for i := range want {
		if tokens[i].Token != want[i].Token || tokens[i].Start != want[i].Start || tokens[i].End != want[i].End {
			t.Fatalf("tokens[%d] = %#v, want %#v", i, tokens[i], want[i])
		}
	}
}

func TestBasicSentenceTokenizerSplitsBlankLineParagraphsWithoutPunctuation(t *testing.T) {
	tokens := NewBasicSentenceTokenizer().Tokenize("First paragraph without punctuation\n\nSecond paragraph without punctuation", "")
	want := []string{"First paragraph without punctuation", "Second paragraph without punctuation"}

	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("Tokenize() = %#v, want %#v", tokens, want)
	}
}

func TestSplitParagraphsSkipsWhitespaceOnlyText(t *testing.T) {
	if tokens := SplitParagraphs(" \n\t\n "); len(tokens) != 0 {
		t.Fatalf("tokens = %#v, want empty", tokens)
	}
}

func TestTokenizeParagraphsReturnsParagraphText(t *testing.T) {
	got := TokenizeParagraphs(" one\n\n two\nthree ")
	want := []string{"one", "two\nthree"}
	if len(got) != len(want) {
		t.Fatalf("paragraphs = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paragraphs = %#v, want %#v", got, want)
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

func TestHyphenateWordMatchesReferenceExceptions(t *testing.T) {
	tests := map[string][]string{
		"Associate":     {"As", "so", "ciate"},
		"associate":     {"as", "so", "ciate"},
		"obligatory":    {"oblig", "a", "tory"},
		"philanthropic": {"phil", "an", "thropic"},
		"recognizance":  {"re", "cog", "ni", "zance"},
		"table":         {"ta", "ble"},
	}

	for word, want := range tests {
		got := HyphenateWord(word)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("HyphenateWord(%q) = %#v, want %#v", word, got, want)
		}
	}
}

func TestHyphenateWordMatchesReferencePatterns(t *testing.T) {
	tests := map[string][]string{
		"beautiful":     {"beau", "ti", "ful"},
		"communication": {"com", "mu", "ni", "ca", "tion"},
		"computer":      {"com", "put", "er"},
		"development":   {"de", "vel", "op", "ment"},
		"extraordinary": {"ex", "tra", "or", "di", "nary"},
		"hyphenation":   {"hy", "phen", "ation"},
		"reference":     {"ref", "er", "ence"},
		"tokenizer":     {"to", "k", "eniz", "er"},
		"workflow":      {"work", "flow"},
	}

	for word, want := range tests {
		got := HyphenateWord(word)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("HyphenateWord(%q) = %#v, want %#v", word, got, want)
		}
	}
}

func TestHyphenateWordKeepsShortWordsWhole(t *testing.T) {
	for _, word := range []string{"", "go", "word"} {
		got := HyphenateWord(word)
		want := []string{word}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("HyphenateWord(%q) = %#v, want %#v", word, got, want)
		}
	}
}
