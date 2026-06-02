package blingfire

import (
	"reflect"
	"testing"
)

func TestTextToSentencesWithOffsetsDoesNotPanicAndPreservesOffsets(t *testing.T) {
	text := "  Hello world. Next line!\nTrailing"

	sentences, offsets := TextToSentencesWithOffsets(text)

	wantSentences := []string{"Hello world.", "Next line!", "Trailing"}
	wantOffsets := []Offset{{Start: 2, End: 14}, {Start: 15, End: 25}, {Start: 26, End: 34}}
	if !reflect.DeepEqual(sentences, wantSentences) {
		t.Fatalf("sentences = %#v, want %#v", sentences, wantSentences)
	}
	if !reflect.DeepEqual(offsets, wantOffsets) {
		t.Fatalf("offsets = %#v, want %#v", offsets, wantOffsets)
	}
}

func TestSentenceTokenizerMergesShortSentencesLikeReference(t *testing.T) {
	tokenizer := NewSentenceTokenizer("", 20, 10)

	got := tokenizer.Tokenize("Short. Still short. Long enough sentence.", "")

	want := []string{"Short. Still short. Long enough sentence."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %#v, want %#v", got, want)
	}
}

func TestSentenceTokenizerNormalizesEmbeddedNewlinesLikeReference(t *testing.T) {
	tokenizer := NewSentenceTokenizer("", 1, 10)

	got := tokenizer.Tokenize("Hello\nworld. Next.", "")

	want := []string{"Hello world.", "Next."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %#v, want %#v", got, want)
	}
}

func TestWordTokenizerReturnsWordsWithOffsets(t *testing.T) {
	words, offsets := TextToWordsWithOffsets(" hi  there ")

	if !reflect.DeepEqual(words, []string{"hi", "there"}) {
		t.Fatalf("words = %#v, want hi/there", words)
	}
	if !reflect.DeepEqual(offsets, []Offset{{Start: 1, End: 3}, {Start: 5, End: 10}}) {
		t.Fatalf("offsets = %#v, want word offsets", offsets)
	}
}

func TestTextToWordsUsesOffsetTokenizer(t *testing.T) {
	if got := TextToWords(" hi  there "); !reflect.DeepEqual(got, []string{"hi", "there"}) {
		t.Fatalf("words = %#v, want hi/there", got)
	}
}

func TestSentenceTokenizerStreamFlushesTokens(t *testing.T) {
	stream := NewSentenceTokenizer("", 1, 1).Stream("")

	if err := stream.PushText("Hello. Next."); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if first.Token != "Hello." {
		t.Fatalf("first token = %q, want Hello.", first.Token)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	token, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if token.Token != "Next." {
		t.Fatalf("token = %q, want flushed context token", token.Token)
	}
}

func TestWordTokenizerTokenizeAndStream(t *testing.T) {
	tokenizer := NewWordTokenizer("")

	if got := tokenizer.Tokenize(" hi  there ", ""); !reflect.DeepEqual(got, []string{"hi", "there"}) {
		t.Fatalf("Tokenize = %#v, want hi/there", got)
	}

	stream := tokenizer.Stream("")
	if err := stream.PushText("hi there"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if first.Token != "hi" {
		t.Fatalf("first token = %q, want hi", first.Token)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v", err)
	}
	if second.Token != "there" {
		t.Fatalf("second token = %q, want there", second.Token)
	}
}
