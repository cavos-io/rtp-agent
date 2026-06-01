package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	cavosmath "github.com/cavos-io/rtp-agent/library/math"
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
	if err := endSynthesizeStreamInput(stream); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &chunkedStreamFromSynthesizeStream{stream: stream, text: text, requestID: cavosmath.ShortUUID("")}, nil
}

type inputEndingSynthesizeStream interface {
	EndInput() error
}

func endSynthesizeStreamInput(stream SynthesizeStream) error {
	if ending, ok := stream.(inputEndingSynthesizeStream); ok {
		return ending.EndInput()
	}
	return stream.Flush()
}

type chunkedStreamFromSynthesizeStream struct {
	stream    SynthesizeStream
	text      string
	requestID string
	pending   *SynthesizedAudio
	audioSeen bool
	closed    bool
}

func (s *chunkedStreamFromSynthesizeStream) Next() (*SynthesizedAudio, error) {
	for {
		audio, err := s.stream.Next()
		if err != nil {
			_ = s.Close()
			if errors.Is(err, io.EOF) {
				if s.pending != nil {
					pending := cloneSynthesizedAudio(s.pending)
					pending.RequestID = s.requestID
					pending.SegmentID = ""
					pending.IsFinal = true
					s.pending = nil
					return pending, nil
				}
				if !s.audioSeen && strings.TrimSpace(s.text) != "" {
					return nil, fmt.Errorf("no audio frames were pushed for text: %s", s.text)
				}
			}
			return nil, err
		}
		s.audioSeen = true
		if s.pending != nil {
			pending := cloneSynthesizedAudio(s.pending)
			pending.RequestID = s.requestID
			pending.SegmentID = ""
			pending.IsFinal = false
			s.pending = audio
			return pending, nil
		}
		s.pending = audio
	}
}

func (s *chunkedStreamFromSynthesizeStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.stream.Close()
}
