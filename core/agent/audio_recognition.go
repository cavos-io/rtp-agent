package agent

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
)

type AudioRecognition struct {
	session *AgentSession
	hooks   RecognitionHooks

	stt stt.STT
	vad vad.VAD

	vadStream vad.VADStream
	sttStream stt.RecognizeStream
	
	// For STTNode override
	sttNodeAudioCh chan *model.AudioFrame
	sttNodeOutCh   <-chan *stt.SpeechEvent

	mu       sync.Mutex
	speaking bool
}

type RecognitionHooks interface {
	OnStartOfSpeech(ev *vad.VADEvent)
	OnEndOfSpeech(ev *vad.VADEvent)
	OnInterimTranscript(ev *stt.SpeechEvent)
	OnFinalTranscript(ev *stt.SpeechEvent)
}

func NewAudioRecognition(session *AgentSession, hooks RecognitionHooks, s stt.STT, v vad.VAD) *AudioRecognition {
	return &AudioRecognition{
		session:        session,
		hooks:          hooks,
		stt:            s,
		vad:            v,
		sttNodeAudioCh: make(chan *model.AudioFrame, 100),
	}
}

func (ar *AudioRecognition) Start(ctx context.Context) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	var started bool
	var startErr error

	if ar.vad != nil {
		stream, err := ar.vad.Stream(ctx)
		if err != nil {
			startErr = fmt.Errorf("failed to start VAD stream: %w", err)
		} else {
			ar.vadStream = stream
			started = true
			go ar.vadLoop(ctx, stream)
		}
	}

	var hasSTTNode bool
	if ar.session != nil && ar.session.Agent != nil {
		if baseAgent := ar.session.Agent.GetAgent(); baseAgent != nil && baseAgent.STTNode != nil {
			hasSTTNode = true
			outCh, err := baseAgent.STTNode(ctx, ar.stt, ar.sttNodeAudioCh)
			if err != nil {
				startErr = fmt.Errorf("failed to start STT node override: %w", err)
			} else {
				ar.sttNodeOutCh = outCh
				started = true
				go ar.sttNodeLoop(ctx)
			}
		}
	}

	if !hasSTTNode && ar.stt != nil {
		stream, err := ar.stt.Stream(ctx, "")
		if err != nil {
			if startErr != nil {
				startErr = fmt.Errorf("%v; failed to start STT stream: %w", startErr, err)
			} else {
				startErr = fmt.Errorf("failed to start STT stream: %w", err)
			}
		} else {
			ar.sttStream = stream
			started = true
			go ar.sttLoop(ctx, stream)
		}
	}

	if !started && startErr != nil {
		return startErr
	}

	return nil
}

func (ar *AudioRecognition) vadLoop(ctx context.Context, stream vad.VADStream) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			ev, err := stream.Next()
			if err != nil {
				if err != io.EOF && err != context.Canceled {
					logger.Logger.Errorw("VAD stream error", err)
				}
				return
			}

			switch ev.Type {
			case vad.VADEventStartOfSpeech:
				ar.mu.Lock()
				ar.speaking = true
				ar.mu.Unlock()
				ar.hooks.OnStartOfSpeech(ev)

			case vad.VADEventEndOfSpeech:
				ar.mu.Lock()
				ar.speaking = false
				ar.mu.Unlock()
				ar.hooks.OnEndOfSpeech(ev)
			}
		}
	}
}

func (ar *AudioRecognition) sttLoop(ctx context.Context, stream stt.RecognizeStream) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			ev, err := stream.Next()
			if err != nil {
				if err != io.EOF && err != context.Canceled {
					logger.Logger.Errorw("STT stream error", err)
				}
				return
			}
			if ev == nil {
				continue
			}

			switch ev.Type {
			case stt.SpeechEventStartOfSpeech:
				ar.mu.Lock()
				ar.speaking = true
				ar.mu.Unlock()
				ar.hooks.OnStartOfSpeech(nil)
			case stt.SpeechEventEndOfSpeech:
				ar.mu.Lock()
				ar.speaking = false
				ar.mu.Unlock()
				ar.hooks.OnEndOfSpeech(nil)
			case stt.SpeechEventInterimTranscript, stt.SpeechEventPreflightTranscript:
				ar.hooks.OnInterimTranscript(ev)
			case stt.SpeechEventFinalTranscript:
				ar.hooks.OnFinalTranscript(ev)
			}
		}
	}
}

func (ar *AudioRecognition) sttNodeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ar.sttNodeOutCh:
			if !ok {
				return
			}
			if ev == nil {
				continue
			}

			switch ev.Type {
			case stt.SpeechEventStartOfSpeech:
				ar.mu.Lock()
				ar.speaking = true
				ar.mu.Unlock()
				ar.hooks.OnStartOfSpeech(nil)
			case stt.SpeechEventEndOfSpeech:
				ar.mu.Lock()
				ar.speaking = false
				ar.mu.Unlock()
				ar.hooks.OnEndOfSpeech(nil)
			case stt.SpeechEventInterimTranscript, stt.SpeechEventPreflightTranscript:
				ar.hooks.OnInterimTranscript(ev)
			case stt.SpeechEventFinalTranscript:
				ar.hooks.OnFinalTranscript(ev)
			}
		}
	}
}

func (ar *AudioRecognition) PushAudio(frame *model.AudioFrame) error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	if ar.vadStream != nil {
		_ = ar.vadStream.PushFrame(frame)
	}

	if ar.sttStream != nil {
		_ = ar.sttStream.PushFrame(frame)
	}

	if ar.sttNodeAudioCh != nil {
		select {
		case ar.sttNodeAudioCh <- frame:
		default:
			// channel full, drop frame to avoid blocking audio thread
		}
	}

	return nil
}

func (ar *AudioRecognition) Flush() error {
	ar.mu.Lock()
	defer ar.mu.Unlock()

	if ar.sttStream != nil {
		return ar.sttStream.Flush()
	}
	
	// STTNode doesn't have an explicit flush method since it's channel based,
	// but we could define a marker frame if needed.
	return nil
}
