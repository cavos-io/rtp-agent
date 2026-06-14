package livekit

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTurnDetectorTokenizerFormatsChatTemplateAndTruncatesLeft(t *testing.T) {
	var gotText string
	tokenizer := newTurnDetectorTokenizer(ModelEnglish, func(text string) ([]int, error) {
		gotText = text
		ids := make([]int, MaxHistoryTokens+2)
		for i := range ids {
			ids[i] = i + 1
		}
		return ids, nil
	})

	inputIDs, err := tokenizer.TokenizeTurnDetectorPayload(context.Background(), []byte(`{"chat_ctx":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"latest"}]}`))
	if err != nil {
		t.Fatalf("TokenizeTurnDetectorPayload() error = %v", err)
	}

	wantText := "<|im_start|>user\nhello<|im_end|>\n<|im_start|>assistant\nhi<|im_end|>\n<|im_start|>user\nlatest"
	if gotText != wantText {
		t.Fatalf("formatted text = %q, want %q", gotText, wantText)
	}
	if len(inputIDs) != MaxHistoryTokens {
		t.Fatalf("input IDs len = %d, want %d", len(inputIDs), MaxHistoryTokens)
	}
	if inputIDs[0] != 3 || inputIDs[len(inputIDs)-1] != MaxHistoryTokens+2 {
		t.Fatalf("input IDs = [%d..%d], want left-truncated [3..130]", inputIDs[0], inputIDs[len(inputIDs)-1])
	}
}

func TestTurnDetectorTokenizerRejectsEmptyChatContext(t *testing.T) {
	tokenizer := newTurnDetectorTokenizer(ModelEnglish, func(string) ([]int, error) {
		t.Fatal("encoder called for empty chat_ctx")
		return nil, nil
	})

	if _, err := tokenizer.TokenizeTurnDetectorPayload(context.Background(), []byte(`{"chat_ctx":[]}`)); err == nil {
		t.Fatal("TokenizeTurnDetectorPayload() error = nil, want empty chat_ctx error")
	}
}

func TestTurnDetectorTokenizerConvertsIDsToInt64(t *testing.T) {
	tokenizer := newTurnDetectorTokenizer(ModelEnglish, func(string) ([]int, error) {
		return []int{7, 8, 9}, nil
	})

	inputIDs, err := tokenizer.TokenizeTurnDetectorPayload(context.Background(), []byte(`{"chat_ctx":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatalf("TokenizeTurnDetectorPayload() error = %v", err)
	}
	if !reflect.DeepEqual(inputIDs, []int64{7, 8, 9}) {
		t.Fatalf("input IDs = %#v, want int64 IDs", inputIDs)
	}
}

func TestTurnDetectorTokenizerRejectsMalformedPayload(t *testing.T) {
	tokenizer := newTurnDetectorTokenizer(ModelEnglish, func(string) ([]int, error) {
		t.Fatal("encoder called for malformed payload")
		return nil, nil
	})

	_, err := tokenizer.TokenizeTurnDetectorPayload(context.Background(), []byte(`not json`))
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("TokenizeTurnDetectorPayload() error = %v, want parse error", err)
	}
}

func TestNewHuggingFaceTurnDetectorTokenizerRejectsMissingFile(t *testing.T) {
	_, err := NewHuggingFaceTurnDetectorTokenizer(ModelEnglish, filepath.Join(t.TempDir(), "tokenizer.json"))
	if err == nil {
		t.Fatal("NewHuggingFaceTurnDetectorTokenizer() error = nil, want missing file error")
	}
}
