package blingfire

import (
	"regexp"
	"strings"
)

// Offset represents a start and end position of a token within the original string.
type Offset struct {
	Start int
	End   int
}

// TextToSentences splits text into sentences using a robust regex.
func TextToSentences(text string) []string {
	sentences, _ := TextToSentencesWithOffsets(text)
	return sentences
}

// TextToSentencesWithOffsets splits text into sentences and returns their offsets.
func TextToSentencesWithOffsets(text string) ([]string, []Offset) {
	// Mimic python's nltk/blingfire splits
	re := regexp.MustCompile(`(?P<sentence>.*?[.!?]+(?:\s+|$))`)
	
	var sentences []string
	var offsets []Offset

	matches := re.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			start := strings.Index(text, trimmed)
			return []string{trimmed}, []Offset{{Start: start, End: start + len(trimmed)}}
		}
		return []string{}, []Offset{}
	}

	for _, match := range matches {
		start, end := match[0], match[1]
		segment := text[start:end]
		trimmed := strings.TrimSpace(segment)
		if trimmed != "" {
			sentences = append(sentences, trimmed)
			// Adjust offset for leading spaces if any
			trimStart := start + strings.Index(segment, trimmed)
			offsets = append(offsets, Offset{Start: trimStart, End: trimStart + len(trimmed)})
		}
	}

	// Handle trailing text
	lastEnd := matches[len(matches)-1][1]
	if lastEnd < len(text) {
		remaining := text[lastEnd:]
		trimmed := strings.TrimSpace(remaining)
		if trimmed != "" {
			sentences = append(sentences, trimmed)
			trimStart := lastEnd + strings.Index(remaining, trimmed)
			offsets = append(offsets, Offset{Start: trimStart, End: trimStart + len(trimmed)})
		}
	}

	return sentences, offsets
}

// TextToWords splits text into words.
func TextToWords(text string) []string {
	words, _ := TextToWordsWithOffsets(text)
	return words
}

// TextToWordsWithOffsets splits text into words and returns their offsets.
func TextToWordsWithOffsets(text string) ([]string, []Offset) {
	re := regexp.MustCompile(`\S+`)
	matches := re.FindAllStringIndex(text, -1)
	
	var words []string
	var offsets []Offset
	
	for _, match := range matches {
		start, end := match[0], match[1]
		words = append(words, text[start:end])
		offsets = append(offsets, Offset{Start: start, End: end})
	}
	
	return words, offsets
}

