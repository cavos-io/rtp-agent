package agent

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
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

	mu         sync.Mutex
	speaking   bool
	frameCount int

	ctx    context.Context
	cancel context.CancelFunc
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
		logger.Logger.Infow("[STT-PIPE] starting STT stream")
		stream, err := ar.stt.Stream(ctx, "")
		if err != nil {
			logger.Logger.Errorw("[STT-PIPE] failed to create STT stream", err)
			if startErr != nil {
				startErr = fmt.Errorf("%v; failed to start STT stream: %w", startErr, err)
			} else {
				startErr = fmt.Errorf("failed to start STT stream: %w", err)
			}
		} else {
			logger.Logger.Infow("[STT-PIPE] STT stream created successfully")
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
	logger.Logger.Infow("[STT-PIPE] vadLoop started — waiting for speech events")
	for {
		ev, err := stream.Next()
		if err != nil {
			if err != io.EOF {
				logger.Logger.Errorw("VAD stream error", err)
			}
			return
		}

		if ev.Type == vad.VADEventStartOfSpeech {
			logger.Logger.Infow("User started speaking")
			ar.session.UpdateUserState(UserStateSpeaking)

			// Interrupt ongoing agent speech/generation
			ar.mu.Lock()
			if ar.cancel != nil {
				ar.cancel()
				ctx, cancel := context.WithCancel(context.Background())
				ar.ctx = ctx
				ar.cancel = cancel
			}
			ar.mu.Unlock()
		} else if ev.Type == vad.VADEventEndOfSpeech {
			logger.Logger.Infow("User stopped speaking")
			ar.session.UpdateUserState(UserStateListening)
		}
	}
}

func (ar *AudioRecognition) sttLoop(ctx context.Context, stream stt.RecognizeStream) {
	logger.Logger.Infow("[STT-PIPE] sttLoop started — waiting for transcripts")
	var eventCount int
	for {
		select {
		case <-ctx.Done():
			logger.Logger.Infow("[STT-PIPE] sttLoop context cancelled")
			return
		default:
			ev, err := stream.Next()
			if err != nil {
				if err != io.EOF && err != context.Canceled {
					logger.Logger.Errorw("[STT-PIPE] STT stream error", err)
				} else {
					logger.Logger.Infow("[STT-PIPE] STT stream closed", "totalEventsReceived", eventCount)
				}
				return
			}
			if ev == nil {
				continue
			}

			eventCount++
			switch ev.Type {
			case stt.SpeechEventStartOfSpeech:
				logger.Logger.Infow("[STT-PIPE] STT event: StartOfSpeech")
				ar.mu.Lock()
				ar.speaking = true
				ar.mu.Unlock()
				ar.hooks.OnStartOfSpeech(nil)
			case stt.SpeechEventEndOfSpeech:
				logger.Logger.Infow("[STT-PIPE] STT event: EndOfSpeech")
				ar.mu.Lock()
				ar.speaking = false
				ar.mu.Unlock()
				ar.hooks.OnEndOfSpeech(nil)
			case stt.SpeechEventInterimTranscript:
				text := ""
				if len(ev.Alternatives) > 0 {
					text = ev.Alternatives[0].Text
				}
				if text != "" {
					logger.Logger.Infow("[STT-PIPE] STT interim transcript", "text", text, "confidence", ev.Alternatives[0].Confidence)
				}
				ar.hooks.OnInterimTranscript(ev)
			case stt.SpeechEventPreflightTranscript:
				logger.Logger.Infow("[STT-PIPE] STT preflight transcript")
				ar.hooks.OnInterimTranscript(ev)
			case stt.SpeechEventFinalTranscript:
				text := ""
				if len(ev.Alternatives) > 0 {
					text = ev.Alternatives[0].Text
				}
				logger.Logger.Infow("[STT-PIPE] STT final transcript", "text", text)
				ar.hooks.OnFinalTranscript(ev)
			default:
				logger.Logger.Infow("[STT-PIPE] STT unknown event type", "type", ev.Type)
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

	ar.frameCount++
	if ar.frameCount%100 == 1 {
		logger.Logger.Infow("[STT-PIPE] AudioRecognition.PushAudio receiving frames",
			"frameCount", ar.frameCount,
			"vadStreamActive", ar.vadStream != nil,
			"sttStreamActive", ar.sttStream != nil,
		)
	}

	if ar.vadStream != nil {
		_ = ar.vadStream.PushFrame(frame)
	}

	if ar.sttStream != nil {
		if err := ar.sttStream.PushFrame(frame); err != nil {
			logger.Logger.Errorw("[STT-PIPE] failed to push frame to STT stream", err, "frameCount", ar.frameCount)
		}
	} else {
		if ar.frameCount == 1 {
			logger.Logger.Warnw("[STT-PIPE] STT stream is nil, frames not being sent to STT", nil)
		}
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

func (ar *AudioRecognition) Close() {
	if ar.cancel != nil {
		ar.cancel()
	}
	ar.mu.Lock()
	if ar.sttStream != nil {
		ar.sttStream.Close()
		ar.sttStream = nil
	}
	if ar.vadStream != nil {
		ar.vadStream.Close()
		ar.vadStream = nil
	}
	if ar.sttNodeAudioCh != nil {
		close(ar.sttNodeAudioCh)
		ar.sttNodeAudioCh = nil
	}
	ar.hooks = nil
	ar.session = nil
	ar.mu.Unlock()
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

