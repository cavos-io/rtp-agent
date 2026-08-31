package tts

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestTextTransformPipelineAppliesCallableEntriesInOrder(t *testing.T) {
	input := &sliceTextStream{chunks: []string{"Say **ACME** 😊"}}
	lowercase := TextTransform(func(_ context.Context, input TextStream) (TextStream, error) {
		return NewTextStream(func() (string, error) {
			text, err := input.Next()
			return strings.ToLower(text), err
		}, input.Close), nil
	})

	stream, err := ApplyTextTransformPipeline(context.Background(), input, []TextTransform{
		FilterMarkdownTransform(),
		lowercase,
		ReplaceTransform(map[string]string{"acme": "Acme Corp"}, true),
		FilterEmojiTransform(),
	})
	if err != nil {
		t.Fatalf("ApplyTextTransformPipeline error = %v", err)
	}
	defer stream.Close()

	if got, want := collectTextStream(t, stream), "say Acme Corp "; got != want {
		t.Fatalf("transformed text = %q, want %q", got, want)
	}
}

func TestReplaceTransformMatchesAcrossChunks(t *testing.T) {
	input := &sliceTextStream{chunks: []string{"a", "b b"}}
	stream, err := ApplyTextTransformPipeline(context.Background(), input, []TextTransform{
		ReplaceTransform(map[string]string{
			"ab": "X",
			"b":  "c",
		}, true),
	})
	if err != nil {
		t.Fatalf("ApplyTextTransformPipeline error = %v", err)
	}
	defer stream.Close()

	if got, want := collectTextStream(t, stream), "X c"; got != want {
		t.Fatalf("transformed text = %q, want %q", got, want)
	}
}

func TestReplaceTransformDoesNotCascadeReplacementValues(t *testing.T) {
	input := &sliceTextStream{chunks: []string{"a", "c"}}
	stream, err := ApplyTextTransformPipeline(context.Background(), input, []TextTransform{
		ReplaceTransform(map[string]string{
			"a":  "b",
			"bc": "X",
		}, true),
	})
	if err != nil {
		t.Fatalf("ApplyTextTransformPipeline error = %v", err)
	}
	defer stream.Close()

	if got, want := collectTextStream(t, stream), "bc"; got != want {
		t.Fatalf("transformed text = %q, want %q", got, want)
	}
}

func TestReplaceTransformHonorsCaseSensitivity(t *testing.T) {
	for _, tc := range []struct {
		name          string
		caseSensitive bool
		want          string
	}{
		{name: "insensitive", want: "Cavos Cavos"},
		{name: "sensitive", caseSensitive: true, want: "LiveKit Cavos"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &sliceTextStream{chunks: []string{"Live", "Kit livekit"}}
			stream, err := ApplyTextTransformPipeline(context.Background(), input, []TextTransform{
				ReplaceTransform(map[string]string{"livekit": "Cavos"}, tc.caseSensitive),
			})
			if err != nil {
				t.Fatalf("ApplyTextTransformPipeline error = %v", err)
			}
			defer stream.Close()

			if got := collectTextStream(t, stream); got != tc.want {
				t.Fatalf("transformed text = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTextTransformPipelineClosesUpstreamOnce(t *testing.T) {
	input := &sliceTextStream{chunks: []string{"hello"}}
	stream, err := ApplyTextTransformPipeline(context.Background(), input, []TextTransform{
		FilterEmojiTransform(),
		FilterMarkdownTransform(),
	})
	if err != nil {
		t.Fatalf("ApplyTextTransformPipeline error = %v", err)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if input.closeCalls != 1 {
		t.Fatalf("upstream close calls = %d, want 1", input.closeCalls)
	}
}

func TestTextTransformPipelineOwnsClosureAcrossCustomTransforms(t *testing.T) {
	input := &sliceTextStream{chunks: []string{"hello"}}
	customCloseCalls := 0
	custom := TextTransform(func(_ context.Context, input TextStream) (TextStream, error) {
		return NewTextStream(input.Next, func() error {
			customCloseCalls++
			return nil
		}), nil
	})

	stream, err := ApplyTextTransformPipeline(context.Background(), input, []TextTransform{custom})
	if err != nil {
		t.Fatalf("ApplyTextTransformPipeline error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	if customCloseCalls != 1 {
		t.Fatalf("custom close calls = %d, want 1", customCloseCalls)
	}
	if input.closeCalls != 1 {
		t.Fatalf("upstream close calls = %d, want 1", input.closeCalls)
	}
}

func TestTextTransformPipelinePreventsCustomCloseFromDoubleClosingUpstream(t *testing.T) {
	source := &sliceTextStream{chunks: []string{"hello"}}
	custom := TextTransform(func(_ context.Context, input TextStream) (TextStream, error) {
		return NewTextStream(input.Next, func() error {
			if err := input.Close(); err != nil {
				return err
			}
			if source.closeCalls != 1 {
				return errors.New("custom input Close did not reach upstream")
			}
			return nil
		}), nil
	})

	stream, err := ApplyTextTransformPipeline(context.Background(), source, []TextTransform{custom})
	if err != nil {
		t.Fatalf("ApplyTextTransformPipeline error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	if source.closeCalls != 1 {
		t.Fatalf("upstream close calls = %d, want 1", source.closeCalls)
	}
}

type sliceTextStream struct {
	chunks     []string
	next       int
	closeCalls int
}

func (s *sliceTextStream) Next() (string, error) {
	if s.next >= len(s.chunks) {
		return "", io.EOF
	}
	chunk := s.chunks[s.next]
	s.next++
	return chunk, nil
}

func (s *sliceTextStream) Close() error {
	s.closeCalls++
	return nil
}

func collectTextStream(t *testing.T, stream TextStream) string {
	t.Helper()
	var text strings.Builder
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			return text.String()
		}
		if err != nil {
			t.Fatalf("Next error = %v", err)
		}
		text.WriteString(chunk)
	}
}
