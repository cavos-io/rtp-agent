package inference

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

type STT struct {
	model     string
	apiKey    string
	apiSecret string
	baseURL   string
}

func NewSTT(model string, apiKey, apiSecret string) *STT {
	if model == "" {
		model = "deepgram/nova-3"
	}
	return &STT{
		model:     model,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   "wss://agent-gateway.livekit.cloud/v1",
	}
}

func (s *STT) Label() string {
	return "livekit.STT"
}

func (s *STT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{
		Streaming:        true,
		InterimResults:   true,
		Diarization:      true,
		OfflineRecognize: false,
	}
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, fmt.Errorf("offline recognize is unsupported natively by LiveKit Inference STT, use stream instead")
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	token, err := CreateAccessToken(s.apiKey, s.apiSecret, time.Hour)
	if err != nil {
		return nil, err
	}

	modelName := s.model
	if idx := strings.LastIndex(s.model, ":"); idx != -1 {
		if language == "" {
			language = s.model[idx+1:]
		}
		modelName = s.model[:idx]
	}

	wsURL, err := url.Parse(s.baseURL + "/stt")
	if err != nil {
		return nil, err
	}

	q := wsURL.Query()
	q.Set("model", modelName)
	wsURL.RawQuery = q.Encode()

	header := http.Header{}
	header.Add("Authorization", "Bearer "+token)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), header)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to LiveKit Inference STT: %w", err)
	}

	// Send session.create
	settings := map[string]interface{}{
		"sample_rate": "24000",
		"encoding":    "pcm_s16le",
	}
	if language != "" {
		settings["language"] = language
	}

	createParams := map[string]interface{}{
		"type":     "session.create",
		"settings": settings,
		"model":    modelName,
	}

	if err := conn.WriteJSON(createParams); err != nil {
		conn.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	stream := &inferenceSTTStream{
		stt:     s,
		conn:    conn,
		ctx:     ctx,
		cancel:  cancel,
		audioCh: make(chan *model.AudioFrame, 100),
		eventCh: make(chan *stt.SpeechEvent, 100),
	}

	go stream.run()

	return stream, nil
}

type inferenceSTTStream struct {
	stt           *STT
	conn          *websocket.Conn
	ctx           context.Context
	cancel        context.CancelFunc
	audioCh       chan *model.AudioFrame
	eventCh       chan *stt.SpeechEvent
	mu            sync.Mutex
	closeOnce     sync.Once
	closed        bool
	speaking      bool
	audioDuration float64
}

func (s *inferenceSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("stream closed")
	}
	s.audioDuration += float64(frame.SamplesPerChannel) / float64(frame.SampleRate)
	s.mu.Unlock()

	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.audioCh <- frame:
		return nil
	}
}

func (s *inferenceSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}

	endPkt := map[string]interface{}{
		"type": "session.finalize",
	}
	return s.conn.WriteJSON(endPkt)
}

func (s *inferenceSTTStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.conn.Close()
		close(s.audioCh)
	})

	return nil
}

func (s *inferenceSTTStream) Next() (*stt.SpeechEvent, error) {
	ev, ok := <-s.eventCh
	if !ok {
		return nil, context.Canceled
	}
	return ev, nil
}

func (s *inferenceSTTStream) run() {
	defer func() {
		_ = s.Close()
		close(s.eventCh)
	}()

	// Send loop
	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			case frame, ok := <-s.audioCh:
				if !ok {
					return
				}

				base64Audio := base64.StdEncoding.EncodeToString(frame.Data)
				audioMsg := map[string]interface{}{
					"type":  "input_audio",
					"audio": base64Audio,
				}

				s.mu.Lock()
				if s.closed {
					s.mu.Unlock()
					return
				}
				err := s.conn.WriteJSON(audioMsg)
				s.mu.Unlock()

				if err != nil {
					return
				}
			}
		}
	}()

	// Read loop
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			_, msg, err := s.conn.ReadMessage()
			if err != nil {
				logger.Logger.Errorw("LiveKit Inference STT disconnected", err)
				return
			}

			var ev map[string]interface{}
			if err := json.Unmarshal(msg, &ev); err != nil {
				continue
			}

			evType, _ := ev["type"].(string)
			switch evType {
			case "session.created", "session.finalized", "session.closed":
				// ignore
			case "interim_transcript", "final_transcript":
				s.processTranscript(ev, evType == "final_transcript")
			case "error":
				logger.Logger.Errorw("LiveKit Inference STT error", nil, "msg", string(msg))
			}
		}
	}
}

func (s *inferenceSTTStream) processTranscript(data map[string]interface{}, isFinal bool) {
	text, _ := data["transcript"].(string)
	requestID, _ := data["request_id"].(string)

	if text == "" && !isFinal {
		return
	}

	if !s.speaking {
		s.speaking = true
		s.emitEvent(&stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech, RequestID: requestID})
	}

	speechData := stt.SpeechData{
		Text: text,
	}
	if lang, ok := data["language"].(string); ok {
		speechData.Language = lang
	}
	if conf, ok := data["confidence"].(float64); ok {
		speechData.Confidence = conf
	}
	if start, ok := data["start"].(float64); ok {
		speechData.StartTime = start
	}
	if dur, ok := data["duration"].(float64); ok {
		speechData.EndTime = speechData.StartTime + dur
	}

	if isFinal {
		s.mu.Lock()
		duration := s.audioDuration
		s.audioDuration = 0
		s.mu.Unlock()

		s.emitEvent(&stt.SpeechEvent{
			Type:      stt.SpeechEventRecognitionUsage,
			RequestID: requestID,
			Alternatives: []stt.SpeechData{
				{
					Text:      "",
					StartTime: 0,
					EndTime:   duration,
				},
			},
		})

		s.emitEvent(&stt.SpeechEvent{
			Type:         stt.SpeechEventFinalTranscript,
			RequestID:    requestID,
			Alternatives: []stt.SpeechData{speechData},
		})

		if s.speaking {
			s.speaking = false
			s.emitEvent(&stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech, RequestID: requestID})
		}
	} else {
		s.emitEvent(&stt.SpeechEvent{
			Type:         stt.SpeechEventInterimTranscript,
			RequestID:    requestID,
			Alternatives: []stt.SpeechData{speechData},
		})
	}
}

func (s *inferenceSTTStream) emitEvent(ev *stt.SpeechEvent) {
	if ev == nil {
		return
	}

	select {
	case <-s.ctx.Done():
		return
	case s.eventCh <- ev:
	}
}

