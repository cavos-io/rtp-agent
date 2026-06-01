package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

func SynthesizeWithStream(ctx context.Context, provider TTS, text string) (ChunkedStream, error) {
	stream, err := provider.Stream(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.PushText(text); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := stream.Flush(); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &chunkedStreamFromSynthesizeStream{stream: stream, text: text}, nil
}

type chunkedStreamFromSynthesizeStream struct {
	stream    SynthesizeStream
	text      string
	audioSeen bool
}

func (s *chunkedStreamFromSynthesizeStream) Next() (*SynthesizedAudio, error) {
	audio, err := s.stream.Next()
	if err != nil {
		if errors.Is(err, io.EOF) && !s.audioSeen && strings.TrimSpace(s.text) != "" {
			return nil, fmt.Errorf("no audio frames were pushed for text: %s", s.text)
		}
		return nil, err
	}
	s.audioSeen = true
	return audio, nil
}

func (s *chunkedStreamFromSynthesizeStream) Close() error {
	return s.stream.Close()
}
