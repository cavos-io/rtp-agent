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
		emitTTSError(provider, err, false)
		return nil, err
	}
	if err := stream.PushText(text); err != nil {
		_ = stream.Close()
		emitTTSError(provider, err, false)
		return nil, err
	}
	if err := endSynthesizeStreamInput(stream); err != nil {
		_ = stream.Close()
		emitTTSError(provider, err, false)
		return nil, err
	}
	return &chunkedStreamFromSynthesizeStream{
		provider:  provider,
		stream:    stream,
		text:      text,
		requestID: cavosmath.ShortUUID(""),
	}, nil
}

type inputEndingSynthesizeStream interface {
	EndInput() error
}

func endSynthesizeStreamInput(stream SynthesizeStream) error {
	return EndSynthesizeStreamInput(stream)
}

func EndSynthesizeStreamInput(stream SynthesizeStream) error {
	if ending, ok := stream.(inputEndingSynthesizeStream); ok {
		return ending.EndInput()
	}
	return stream.Flush()
}

type chunkedStreamFromSynthesizeStream struct {
	provider    TTS
	stream      SynthesizeStream
	text        string
	requestID   string
	pending     *SynthesizedAudio
	pendingTail bool
	audioSeen   bool
	closed      bool
	errEmitted  bool
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
					err := fmt.Errorf("no audio frames were pushed for text: %s", s.text)
					s.emitError(err)
					return nil, err
				}
			}
			if !errors.Is(err, io.EOF) {
				s.emitError(err)
			}
			return nil, err
		}
		s.audioSeen = true
		if s.pending != nil {
			combined, combineErr := combineAudioFrames(s.pending.Frame, audio.Frame)
			if s.pendingTail && combineErr == nil {
				audio = cloneSynthesizedAudio(audio)
				audio.Frame = combined
			} else {
				pending := s.stampAudio(s.pending, false)
				s.pending = audio
				s.pendingTail = false
				return pending, nil
			}
		}
		head, tail, ok := splitSynthesizedAudioTail(audio)
		if ok {
			s.pending = tail
			s.pendingTail = true
			return s.stampAudio(head, false), nil
		}
		s.pending = tail
		s.pendingTail = false
	}
}

func (s *chunkedStreamFromSynthesizeStream) emitError(err error) {
	if s.errEmitted {
		return
	}
	s.errEmitted = true
	emitTTSError(s.provider, err, false)
}

func (s *chunkedStreamFromSynthesizeStream) stampAudio(audio *SynthesizedAudio, isFinal bool) *SynthesizedAudio {
	audio = cloneSynthesizedAudio(audio)
	audio.RequestID = s.requestID
	audio.SegmentID = ""
	audio.IsFinal = isFinal
	return audio
}

func (s *chunkedStreamFromSynthesizeStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.stream.Close()
}
