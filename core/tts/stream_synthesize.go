package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	cavosmath "github.com/cavos-io/conversation-worker/library/math"
)

func SynthesizeWithStream(ctx context.Context, provider TTS, text string) (ChunkedStream, error) {
	stream, err := provider.Stream(ctx)
	if err != nil {
		return nil, err
	}
	if text != "" {
		if err := stream.PushText(text); err != nil {
			_ = stream.Close()
			return nil, err
		}
	}
	if err := stream.Flush(); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &chunkedStreamFromSynthesizeStream{stream: stream, text: text, requestID: cavosmath.ShortUUID("")}, nil
}

type chunkedStreamFromSynthesizeStream struct {
	stream    SynthesizeStream
	text      string
	requestID string
	audioSeen bool
	closed    bool
}

func (s *chunkedStreamFromSynthesizeStream) Next() (*SynthesizedAudio, error) {
	audio, err := s.stream.Next()
	if err != nil {
		_ = s.Close()
		if errors.Is(err, io.EOF) && !s.audioSeen && strings.TrimSpace(s.text) != "" {
			return nil, fmt.Errorf("no audio frames were pushed for text: %s", s.text)
		}
		return nil, err
	}
	s.audioSeen = true
	audio = cloneSynthesizedAudio(audio)
	audio.RequestID = s.requestID
	return audio, nil
}

func (s *chunkedStreamFromSynthesizeStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.stream.Close()
}
