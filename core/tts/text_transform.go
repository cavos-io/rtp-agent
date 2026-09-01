package tts

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// TextStream supplies transformed text chunks until exhaustion or cancellation.
type TextStream interface {
	Next(ctx context.Context) (string, error)
	Close() error
}

// TextTransform wraps a text stream with one stage of transformation.
type TextTransform func(TextStream) (TextStream, error)

type textStream struct {
	next      func(context.Context) (string, error)
	close     func() error
	closeOnce sync.Once
	closeErr  error
}

// NewTextStream creates a TextStream from pull and close functions.
func NewTextStream(next func(context.Context) (string, error), closeFn func() error) TextStream {
	if next == nil {
		next = func(context.Context) (string, error) { return "", io.EOF }
	}

	if closeFn == nil {
		closeFn = func() error { return nil }
	}

	return &textStream{next: next, close: closeFn}
}

func (s *textStream) Next(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	return s.next(ctx)
}

func (s *textStream) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.close()
	})
	return s.closeErr
}

// ApplyTextTransformPipeline applies transforms in slice order and owns every stage.
func ApplyTextTransformPipeline(input TextStream, transforms []TextTransform) (TextStream, error) {
	if input == nil {
		return nil, fmt.Errorf("text transform input stream is nil")
	}

	stream := NewTextStream(input.Next, input.Close)
	owned := []TextStream{stream}
	for i, transform := range transforms {
		if transform == nil {
			_ = closeTextStreams(owned)
			return nil, fmt.Errorf("text transform %d is nil", i)
		}

		next, err := transform(stream)
		if err != nil {
			_ = closeTextStreams(owned)
			return nil, err
		}
		if next == nil {
			_ = closeTextStreams(owned)
			return nil, fmt.Errorf("text transform %d returned a nil stream", i)
		}
		stream = NewTextStream(next.Next, next.Close)
		owned = append(owned, stream)
	}
	return NewTextStream(stream.Next, func() error {
		return closeTextStreams(owned)
	}), nil
}

func closeTextStreams(streams []TextStream) error {
	var firstErr error
	for i := len(streams) - 1; i >= 0; i-- {
		if err := streams[i].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func FilterMarkdownTransform() TextTransform {
	return bufferedTextTransform(func() textTransformBuffer {
		buffer, _ := NewTextTransformBufferWithTransforms([]string{"filter_markdown"})
		return buffer
	})
}

func FilterEmojiTransform() TextTransform {
	return bufferedTextTransform(func() textTransformBuffer {
		buffer, _ := NewTextTransformBufferWithTransforms([]string{"filter_emoji"})
		return buffer
	})
}

func ReplaceTransform(replacements map[string]string, caseSensitive bool) TextTransform {
	copied := make(map[string]string, len(replacements))
	for old, replacement := range replacements {
		copied[old] = replacement
	}
	return bufferedTextTransform(func() textTransformBuffer {
		return newSinglePassTextReplaceBuffer(copied, caseSensitive)
	})
}

func NamedTextTransform(name string) (TextTransform, error) {
	switch name {
	case "filter_markdown":
		return FilterMarkdownTransform(), nil
	case "filter_emoji":
		return FilterEmojiTransform(), nil
	default:
		return nil, invalidTextTransformError{transform: name}
	}
}

type textTransformBuffer interface {
	Push(string) []string
	Flush() []string
}

func bufferedTextTransform(newBuffer func() textTransformBuffer) TextTransform {
	return func(input TextStream) (TextStream, error) {
		return &bufferedTextStream{
			input:  input,
			buffer: newBuffer(),
		}, nil
	}
}

type bufferedTextStream struct {
	input     TextStream
	buffer    textTransformBuffer
	pending   []string
	inputDone bool
	closeOnce sync.Once
	closeErr  error
}

func (s *bufferedTextStream) Next(ctx context.Context) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(s.pending) > 0 {
			chunk := s.pending[0]
			s.pending = s.pending[1:]
			if chunk != "" {
				return chunk, nil
			}
			continue
		}
		if s.inputDone {
			return "", io.EOF
		}

		chunk, err := s.input.Next(ctx)
		switch err {
		case nil:
			s.pending = s.buffer.Push(chunk)
		case io.EOF:
			s.inputDone = true
			s.pending = s.buffer.Flush()
		default:
			return "", err
		}
	}
}

func (s *bufferedTextStream) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.input.Close()
	})
	return s.closeErr
}

type singlePassTextReplaceBuffer struct {
	replacements  []textReplacement
	caseSensitive bool
	pattern       *regexp.Regexp
	prefixes      []string
	buffer        string
}

func newSinglePassTextReplaceBuffer(replacements map[string]string, caseSensitive bool) *singlePassTextReplaceBuffer {
	ordered := make([]textReplacement, 0, len(replacements))
	for old, replacement := range replacements {
		if old == "" {
			continue
		}
		ordered = append(ordered, textReplacement{old: old, new: replacement})
	}
	sort.Slice(ordered, func(i, j int) bool {
		iLen := utf8.RuneCountInString(ordered[i].old)
		jLen := utf8.RuneCountInString(ordered[j].old)
		if iLen == jLen {
			return ordered[i].old < ordered[j].old
		}
		return iLen > jLen
	})

	parts := make([]string, 0, len(ordered))
	prefixSet := map[string]struct{}{}
	for _, replacement := range ordered {
		parts = append(parts, regexp.QuoteMeta(replacement.old))
		runes := []rune(replacement.old)
		for i := 1; i < len(runes); i++ {
			prefixSet[string(runes[:i])] = struct{}{}
		}
	}
	prefixes := make([]string, 0, len(prefixSet))
	for prefix := range prefixSet {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		return utf8.RuneCountInString(prefixes[i]) > utf8.RuneCountInString(prefixes[j])
	})

	var pattern *regexp.Regexp
	if len(parts) > 0 {
		expression := strings.Join(parts, "|")
		if !caseSensitive {
			expression = "(?i)" + expression
		}
		pattern = regexp.MustCompile(expression)
	}
	return &singlePassTextReplaceBuffer{
		replacements:  ordered,
		caseSensitive: caseSensitive,
		pattern:       pattern,
		prefixes:      prefixes,
	}
}

func (b *singlePassTextReplaceBuffer) Push(text string) []string {
	if text == "" {
		return nil
	}
	buffer := b.buffer + text
	hold := b.holdbackLen(buffer)
	flushTo := len(buffer) - hold
	if flushTo <= 0 {
		b.buffer = buffer
		return nil
	}
	out := b.apply(buffer[:flushTo])
	b.buffer = buffer[flushTo:]
	return []string{out}
}

func (b *singlePassTextReplaceBuffer) Flush() []string {
	if b.buffer == "" {
		return nil
	}
	out := b.apply(b.buffer)
	b.buffer = ""
	return []string{out}
}

func (b *singlePassTextReplaceBuffer) apply(text string) string {
	if b.pattern == nil {
		return text
	}
	return b.pattern.ReplaceAllStringFunc(text, func(match string) string {
		for _, replacement := range b.replacements {
			if match == replacement.old || (!b.caseSensitive && strings.EqualFold(match, replacement.old)) {
				return replacement.new
			}
		}
		return match
	})
}

func (b *singlePassTextReplaceBuffer) holdbackLen(text string) int {
	for _, prefix := range b.prefixes {
		if len(prefix) > len(text) {
			continue
		}
		tail := text[len(text)-len(prefix):]
		if tail == prefix || (!b.caseSensitive && strings.EqualFold(tail, prefix)) {
			return len(prefix)
		}
	}
	return 0
}
