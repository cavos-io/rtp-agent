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
	lowercase := TextTransform(func(input TextStream) (TextStream, error) {
		return NewTextStream(func(ctx context.Context) (string, error) {
			text, err := input.Next(ctx)
			return strings.ToLower(text), err
		}, input.Close), nil
	})

	stream, err := ApplyTextTransformPipeline(input, []TextTransform{
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
	stream, err := ApplyTextTransformPipeline(input, []TextTransform{
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
	stream, err := ApplyTextTransformPipeline(input, []TextTransform{
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
			stream, err := ApplyTextTransformPipeline(input, []TextTransform{
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
	stream, err := ApplyTextTransformPipeline(input, []TextTransform{
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
	custom := TextTransform(func(input TextStream) (TextStream, error) {
		return NewTextStream(input.Next, func() error {
			customCloseCalls++
			return nil
		}), nil
	})

	stream, err := ApplyTextTransformPipeline(input, []TextTransform{custom})
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
	custom := TextTransform(func(input TextStream) (TextStream, error) {
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

	stream, err := ApplyTextTransformPipeline(source, []TextTransform{custom})
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

func TestTextTransformPipelinePassesNextContextThroughCallable(t *testing.T) {
	type contextKey struct{}
	want := "request context"
	input := NewTextStream(func(ctx context.Context) (string, error) {
		got, _ := ctx.Value(contextKey{}).(string)
		if got != want {
			return "", errors.New("source received the wrong context")
		}
		return "HELLO", nil
	}, nil)
	lowercase := TextTransform(func(input TextStream) (TextStream, error) {
		return NewTextStream(func(ctx context.Context) (string, error) {
			text, err := input.Next(ctx)
			return strings.ToLower(text), err
		}, input.Close), nil
	})

	stream, err := ApplyTextTransformPipeline(input, []TextTransform{lowercase})
	if err != nil {
		t.Fatalf("ApplyTextTransformPipeline error = %v", err)
	}
	defer stream.Close()

	ctx := context.WithValue(context.Background(), contextKey{}, want)
	if got, err := stream.Next(ctx); got != "hello" || err != nil {
		t.Fatalf("Next() = %q, %v, want %q, nil", got, err, "hello")
	}
}

func TestTextStreamStopsBeforeReadingCanceledContext(t *testing.T) {
	read := false
	stream := NewTextStream(func(context.Context) (string, error) {
		read = true

		return "hello", nil
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := stream.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next() error = %v, want %v", err, context.Canceled)
	}
	if read {
		t.Fatal("Next() read from source after context cancellation")
	}
}

func TestBufferedTextTransformStopsBeforeReadingCanceledContext(t *testing.T) {
	input := &sliceTextStream{chunks: []string{"hello"}}
	stream, err := ApplyTextTransformPipeline(input, []TextTransform{FilterMarkdownTransform()})
	if err != nil {
		t.Fatalf("ApplyTextTransformPipeline error = %v", err)
	}
	defer stream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stream.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next() error = %v, want %v", err, context.Canceled)
	}
	if input.next != 0 {
		t.Fatalf("input reads = %d, want 0", input.next)
	}
}

type sliceTextStream struct {
	chunks     []string
	next       int
	closeCalls int
}

func (s *sliceTextStream) Next(context.Context) (string, error) {
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
		chunk, err := stream.Next(context.Background())
		if err == io.EOF {
			return text.String()
		}
		if err != nil {
			t.Fatalf("Next error = %v", err)
		}
		text.WriteString(chunk)
	}
}
