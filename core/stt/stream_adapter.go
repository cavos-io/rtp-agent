package stt

import (
	"context"

	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/model"
)

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
}

func (w *streamAdapterWrapper) PushFrame(frame *model.AudioFrame) error {
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
	close(w.events)
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
				errCh <- err
				return
			}
			eventCh <- vEvent
		}
	}()

	for {
		select {
		case <-w.ctx.Done():
			return
		case err := <-errCh:
			// Handle error if needed
			_ = err
			return
		case vEvent := <-eventCh:
			if vEvent.Type == vad.VADEventStartOfSpeech {
				w.events <- &SpeechEvent{
					Type: SpeechEventStartOfSpeech,
				}
			} else if vEvent.Type == vad.VADEventEndOfSpeech {
				w.events <- &SpeechEvent{
					Type: SpeechEventEndOfSpeech,
				}

				if len(vEvent.Frames) > 0 {
					tEvent, err := w.adapter.stt.Recognize(w.ctx, vEvent.Frames, w.language)
					if err == nil && tEvent != nil && len(tEvent.Alternatives) > 0 && tEvent.Alternatives[0].Text != "" {
						w.events <- &SpeechEvent{
							Type:         SpeechEventFinalTranscript,
							Alternatives: []SpeechData{tEvent.Alternatives[0]},
						}
					}
				}
			} else if vEvent.Type == vad.VADEventInferenceDone {
				// If VAD says we are still speaking, we can use the frames for interim recognition
				if vEvent.Speaking && len(vEvent.Frames) > 0 {
					tEvent, err := w.adapter.stt.Recognize(w.ctx, vEvent.Frames, w.language)
					if err == nil && tEvent != nil && len(tEvent.Alternatives) > 0 && tEvent.Alternatives[0].Text != "" {
						w.events <- &SpeechEvent{
							Type:         SpeechEventInterimTranscript,
							Alternatives: []SpeechData{tEvent.Alternatives[0]},
						}
					}
				}
			}
		}
	}
}
