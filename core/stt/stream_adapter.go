package stt

import (
	"context"
	"strings"
	"sync"

	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
)

var whisperHallucinationPhrases = []string{
	"terima kasih telah menonton",
	"jangan lupa like",
	"jangan lupa subscribe",
	"like share and subscribe",
	"like, share, dan subscribe",
	"sampai jumpa di video",
	"subtitle by",
	"subtitles by",
	"thank you for watching",
	"thanks for watching",
	"please subscribe",
}

func isHallucination(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, phrase := range whisperHallucinationPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

type StreamAdapter struct {
	stt STT
	vad vad.VAD
}

func NewStreamAdapter(stt STT, vad vad.VAD) *StreamAdapter {
	return &StreamAdapter{
		stt: stt,
		vad: vad,
	}
}

func (s *StreamAdapter) Label() string {
	return "stream_adapter(" + s.stt.Label() + ")"
}

func (s *StreamAdapter) Capabilities() STTCapabilities {
	return STTCapabilities{
		Streaming:        true,
		InterimResults:   false,
		Diarization:      false,
		OfflineRecognize: true,
	}
}

func (s *StreamAdapter) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	return s.stt.Recognize(ctx, frames, language)
}

func (s *StreamAdapter) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	vadStream, err := s.vad.Stream(ctx)
	if err != nil {
		return nil, err
	}

	wrapper := &streamAdapterWrapper{
		adapter:   s,
		ctx:       ctx,
		language:  language,
		vadStream: vadStream,
		events:    make(chan *SpeechEvent, 10),
	}

	go wrapper.run()

	return wrapper, nil
}

type streamAdapterWrapper struct {
	adapter   *StreamAdapter
	ctx       context.Context
	language  string
	vadStream vad.VADStream
	events    chan *SpeechEvent
	closed    bool

	mu           sync.Mutex
	speechFrames []*model.AudioFrame // accumulated frames while speaking
	isSpeaking   bool
}

func (w *streamAdapterWrapper) PushFrame(frame *model.AudioFrame) error {
	w.mu.Lock()
	if w.isSpeaking {
		// deep copy the data so the frame buffer can be reused by the caller
		cp := &model.AudioFrame{
			Data:              append([]byte(nil), frame.Data...),
			SampleRate:        frame.SampleRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: frame.SamplesPerChannel,
		}
		w.speechFrames = append(w.speechFrames, cp)
	}
	w.mu.Unlock()
	return w.vadStream.PushFrame(frame)
}

func (w *streamAdapterWrapper) Flush() error {
	return w.vadStream.Flush()
}

func (w *streamAdapterWrapper) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.vadStream.Close()
	w.mu.Lock()
	w.speechFrames = nil
	w.mu.Unlock()
	return nil
}

func (w *streamAdapterWrapper) Next() (*SpeechEvent, error) {
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case ev, ok := <-w.events:
		if !ok {
			return nil, context.Canceled
		}
		return ev, nil
	}
}

func (w *streamAdapterWrapper) run() {
	defer close(w.events)

	eventCh := make(chan *vad.VADEvent)
	errCh := make(chan error, 1)

	go func() {
		for {
			vEvent, err := w.vadStream.Next()
			if err != nil {
				select {
				case errCh <- err:
				case <-w.ctx.Done():
				}
				return
			}
			select {
			case eventCh <- vEvent:
			case <-w.ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-w.ctx.Done():
			return
		case err := <-errCh:
			logger.Logger.Errorw("VAD stream error in StreamAdapter", err)
			return
		case vEvent := <-eventCh:
			if vEvent.Type == vad.VADEventStartOfSpeech {
				w.mu.Lock()
				w.isSpeaking = true
				w.speechFrames = w.speechFrames[:0]
				w.mu.Unlock()
				w.events <- &SpeechEvent{Type: SpeechEventStartOfSpeech}
			} else if vEvent.Type == vad.VADEventEndOfSpeech {
				w.mu.Lock()
				w.isSpeaking = false
				frames := w.speechFrames
				w.speechFrames = nil
				w.mu.Unlock()

				w.events <- &SpeechEvent{Type: SpeechEventEndOfSpeech}

				const minFramesForRecognition = 50
				if len(frames) >= minFramesForRecognition {
					tEvent, err := w.adapter.stt.Recognize(w.ctx, frames, w.language)
					if err != nil {
						logger.Logger.Errorw("StreamAdapter: STT Recognize failed", err, "frames", len(frames), "language", w.language)
					} else if tEvent != nil && len(tEvent.Alternatives) > 0 && tEvent.Alternatives[0].Text != "" {
						text := tEvent.Alternatives[0].Text
						if isHallucination(text) {
							logger.Logger.Warnw("StreamAdapter: filtered Whisper hallucination", nil, "text", text, "frames", len(frames))
						} else {
							w.events <- &SpeechEvent{
								Type:         SpeechEventFinalTranscript,
								Alternatives: []SpeechData{tEvent.Alternatives[0]},
							}
						}
					}
				} else if len(frames) > 0 {
					logger.Logger.Infow("StreamAdapter: skipping recognition, audio too short", "frames", len(frames), "minRequired", minFramesForRecognition)
				}
			}
		}
	}
}

