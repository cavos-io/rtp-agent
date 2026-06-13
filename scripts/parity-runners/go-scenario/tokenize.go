package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cavos-io/rtp-agent/library/tokenize"
)

func runTokenizeTokenStream(input json.RawMessage) (any, error) {
	var payload struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	mode := payload.Mode
	if mode == "" {
		mode = "closed_lifecycle"
	}

	switch mode {
	case "closed_lifecycle":
		stream := tokenize.NewBufferedTokenStream(strings.Fields, 1, 1)
		before := stream.Closed()
		closeErr := stream.Close()
		after := stream.Closed()
		return tokenStreamResult(mode, []map[string]any{
			{"name": "closed_before", "result": before},
			{"name": "close", "error": closeErr != nil, "error_class": errorClass(closeErr)},
			{"name": "closed_after", "result": after},
		}), nil
	case "close_flush":
		stream := tokenize.NewBufferedTokenStream(func(text string) []string { return []string{text} }, 1, 1)
		pushErr := stream.PushText("hello")
		closeErr := stream.Close()
		token, nextErr := stream.Next()
		return tokenStreamResult(mode, []map[string]any{
			{"name": "push_text", "error": pushErr != nil, "error_class": errorClass(pushErr)},
			{"name": "close", "error": closeErr != nil, "error_class": errorClass(closeErr)},
			tokenStreamNextEvent("next", token, nextErr),
		}), nil
	case "next_eof_closed":
		stream := tokenize.NewBufferedTokenStream(func(string) []string { return nil }, 1, 1)
		closeErr := stream.Close()
		token, nextErr := stream.Next()
		return tokenStreamResult(mode, []map[string]any{
			{"name": "close", "error": closeErr != nil, "error_class": errorClass(closeErr)},
			tokenStreamNextEvent("next", token, nextErr),
		}), nil
	case "last_token_context":
		stream := tokenize.NewBufferedTokenStream(strings.Fields, 1, 1)
		pushErr := stream.PushText("one two three")
		first, firstErr := stream.Next()
		second, secondErr := stream.Next()
		flushErr := stream.Flush()
		third, thirdErr := stream.Next()
		return tokenStreamResult(mode, []map[string]any{
			{"name": "push_text", "error": pushErr != nil, "error_class": errorClass(pushErr)},
			tokenStreamNextEvent("next", first, firstErr),
			tokenStreamNextEvent("next", second, secondErr),
			{"name": "flush", "error": flushErr != nil, "error_class": errorClass(flushErr)},
			tokenStreamNextEvent("next", third, thirdErr),
		}), nil
	case "end_input_flush_close":
		stream := tokenize.NewBufferedTokenStream(func(text string) []string { return []string{text} }, 1, 10)
		pushErr := stream.PushText("hello")
		endErr := stream.EndInput()
		first, firstErr := stream.Next()
		second, secondErr := stream.Next()
		return tokenStreamResult(mode, []map[string]any{
			{"name": "push_text", "error": pushErr != nil, "error_class": errorClass(pushErr)},
			{"name": "end_input", "error": endErr != nil, "error_class": errorClass(endErr)},
			tokenStreamNextEvent("next", first, firstErr),
			tokenStreamNextEvent("next", second, secondErr),
		}), nil
	case "end_input_closed":
		stream := tokenize.NewBufferedTokenStream(strings.Fields, 1, 1)
		firstErr := stream.EndInput()
		secondErr := stream.EndInput()
		return tokenStreamResult(mode, []map[string]any{
			{"name": "first_end_input", "error": firstErr != nil, "error_class": errorClass(firstErr)},
			{"name": "second_end_input", "error": secondErr != nil, "error_class": errorClass(secondErr)},
		}), nil
	case "aclose_no_flush":
		stream := tokenize.NewBufferedTokenStream(func(text string) []string { return []string{text} }, 1, 10)
		pushErr := stream.PushText("hello")
		closeErr := stream.AClose()
		token, nextErr := stream.Next()
		return tokenStreamResult(mode, []map[string]any{
			{"name": "push_text", "error": pushErr != nil, "error_class": errorClass(pushErr)},
			{"name": "aclose", "error": closeErr != nil, "error_class": errorClass(closeErr)},
			tokenStreamNextEvent("next", token, nextErr),
		}), nil
	case "whitespace_context":
		stream := tokenize.NewBufferedTokenStream(func(text string) []string {
			if strings.HasPrefix(text, "\t") {
				return []string{"\t", "two"}
			}
			return strings.Fields(text)
		}, 1, 1)
		pushErr := stream.PushText("one\t two")
		flushErr := stream.Flush()
		first, firstErr := stream.Next()
		second, secondErr := stream.Next()
		return tokenStreamResult(mode, []map[string]any{
			{"name": "push_text", "error": pushErr != nil, "error_class": errorClass(pushErr)},
			{"name": "flush", "error": flushErr != nil, "error_class": errorClass(flushErr)},
			tokenStreamNextEvent("next", first, firstErr),
			tokenStreamNextEvent("next", second, secondErr),
		}), nil
	default:
		return nil, fmt.Errorf("unknown token stream mode %q", mode)
	}
}

func tokenStreamResult(mode string, events []map[string]any) map[string]any {
	return map[string]any{
		"contract": "token-stream-" + strings.ReplaceAll(mode, "_", "-"),
		"events":   events,
	}
}

func tokenStreamNextEvent(name string, token *tokenize.TokenData, err error) map[string]any {
	event := map[string]any{
		"name":        name,
		"error":       err != nil,
		"error_class": tokenStreamErrorClass(err),
	}
	if token != nil {
		event["token"] = token.Token
	}
	return event
}

func tokenStreamErrorClass(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, io.EOF) {
		return "eof"
	}
	return "error"
}

func runTokenizeReplaceWords(input json.RawMessage) (any, error) {
	var payload struct {
		TextValues   []string          `json:"text_values"`
		Replacements map[string]string `json:"replacements"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.TextValues == nil {
		payload.TextValues = []string{"Hello, WORLD! workflow stays.", "Do not replace flow inside workflow."}
	}
	if payload.Replacements == nil {
		payload.Replacements = map[string]string{"hello": "hi", "world": "there", "flow": "stream"}
	}

	events := make([]map[string]any, 0, len(payload.TextValues))
	for _, value := range payload.TextValues {
		events = append(events, map[string]any{
			"name":   "replace_words",
			"input":  value,
			"result": tokenize.ReplaceWords(value, payload.Replacements),
		})
	}
	return map[string]any{"contract": "tokenize-replace-words", "events": events}, nil
}

func runTokenizeFormatWords(input json.RawMessage) (any, error) {
	var payload struct {
		WordValues [][]string `json:"word_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.WordValues == nil {
		payload.WordValues = [][]string{{"hello", "world"}}
	}
	tokenizer := tokenize.NewBasicWordTokenizer()
	events := make([]map[string]any, 0, len(payload.WordValues))
	for _, words := range payload.WordValues {
		events = append(events, map[string]any{
			"name":   "format_words",
			"input":  words,
			"result": tokenizer.FormatWords(words),
		})
	}
	return map[string]any{"contract": "tokenize-format-words", "events": events}, nil
}

func runTokenizeSentenceTokenizer(input json.RawMessage) (any, error) {
	var payload struct {
		TextValues []string `json:"text_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.TextValues == nil {
		payload.TextValues = []string{"Version 1.5 is ready. Next sentence."}
	}
	tokenizer := tokenize.NewBasicSentenceTokenizer()
	events := make([]map[string]any, 0, len(payload.TextValues))
	for _, value := range payload.TextValues {
		events = append(events, map[string]any{
			"name":   "sentence_tokenize",
			"input":  value,
			"result": tokenizer.Tokenize(value, ""),
		})
	}
	return map[string]any{"contract": "tokenize-sentence-tokenizer", "events": events}, nil
}

func runTokenizeSplitWords(input json.RawMessage) (any, error) {
	var payload struct {
		IgnorePunctuation *bool    `json:"ignore_punctuation"`
		RetainFormat      *bool    `json:"retain_format"`
		SplitCharacter    *bool    `json:"split_character"`
		TextValues        []string `json:"text_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.TextValues == nil {
		payload.TextValues = []string{" Hello, world!  keep-format? ", "alpha beta,gamma"}
	}
	ignorePunctuation := boolValue(payload.IgnorePunctuation, true)
	splitCharacter := boolValue(payload.SplitCharacter, false)
	retainFormat := boolValue(payload.RetainFormat, false)

	events := make([]map[string]any, 0, len(payload.TextValues))
	for _, value := range payload.TextValues {
		words := tokenize.SplitWords(value, ignorePunctuation, splitCharacter, retainFormat)
		result := make([]map[string]any, 0, len(words))
		for _, word := range words {
			result = append(result, map[string]any{
				"token": word.Token,
				"start": word.Start,
				"end":   word.End,
			})
		}
		events = append(events, map[string]any{
			"name":   "split_words",
			"input":  value,
			"result": result,
		})
	}
	return map[string]any{"contract": "tokenize-split-words", "events": events}, nil
}

func runTokenizeSplitSentences(input json.RawMessage) (any, error) {
	var payload struct {
		MinSentenceLen *int     `json:"min_sentence_len"`
		RetainFormat   *bool    `json:"retain_format"`
		TextValues     []string `json:"text_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.TextValues == nil {
		payload.TextValues = []string{"Version 1.5 is ready. Next sentence.", "他说：“你好。” 下一句。"}
	}
	minSentenceLen := intValue(payload.MinSentenceLen, 20)
	retainFormat := boolValue(payload.RetainFormat, false)

	events := make([]map[string]any, 0, len(payload.TextValues))
	for _, value := range payload.TextValues {
		sentences := tokenize.SplitSentences(value, minSentenceLen, retainFormat)
		result := make([]string, 0, len(sentences))
		for _, sentence := range sentences {
			result = append(result, sentence.Token)
		}
		events = append(events, map[string]any{
			"name":   "split_sentences",
			"input":  value,
			"result": result,
		})
	}
	return map[string]any{"contract": "tokenize-split-sentences", "events": events}, nil
}

func runTokenizeParagraphs(input json.RawMessage) (any, error) {
	var payload struct {
		TextValues []string `json:"text_values"`
		WithOffset *bool    `json:"with_offset"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.TextValues == nil {
		payload.TextValues = []string{" one\n\n two\nthree "}
	}
	withOffset := boolValue(payload.WithOffset, false)
	events := make([]map[string]any, 0, len(payload.TextValues))
	for _, value := range payload.TextValues {
		if withOffset {
			paragraphs := tokenize.SplitParagraphs(value)
			result := make([]map[string]any, 0, len(paragraphs))
			for _, paragraph := range paragraphs {
				result = append(result, map[string]any{
					"token": paragraph.Token,
					"start": paragraph.Start,
					"end":   paragraph.End,
				})
			}
			events = append(events, map[string]any{"name": "split_paragraphs", "input": value, "result": result})
			continue
		}
		events = append(events, map[string]any{
			"name":   "tokenize_paragraphs",
			"input":  value,
			"result": tokenize.TokenizeParagraphs(value),
		})
	}
	return map[string]any{"contract": "tokenize-paragraphs", "events": events}, nil
}

func runTokenizeHyphenateWords(input json.RawMessage) (any, error) {
	var payload struct {
		WordValues []string `json:"word_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.WordValues == nil {
		payload.WordValues = []string{"beautiful", "communication", "word"}
	}
	events := make([]map[string]any, 0, len(payload.WordValues))
	for _, value := range payload.WordValues {
		events = append(events, map[string]any{
			"name":   "hyphenate_word",
			"input":  value,
			"result": tokenize.HyphenateWord(value),
		})
	}
	return map[string]any{"contract": "tokenize-hyphenate-words", "events": events}, nil
}
