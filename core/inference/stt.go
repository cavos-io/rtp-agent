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

	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
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
		"sample_rate": "16000",
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
	stt             *STT
	conn            *websocket.Conn
	ctx             context.Context
	cancel          context.CancelFunc
	audioCh         chan *model.AudioFrame
	eventCh         chan *stt.SpeechEvent
	mu              sync.Mutex
	closed          bool
	rateGuard       stt.SampleRateGuard
	speaking        bool
	audioDuration   float64
	startTimeOffset float64
	startTime       float64
}

func (s *inferenceSTTStream) StartTimeOffset() float64 {
	return s.startTimeOffset
}

func (s *inferenceSTTStream) SetStartTimeOffset(offset float64) {
	s.startTimeOffset = offset
}

func (s *inferenceSTTStream) StartTime() float64 {
	return s.startTime
}

func (s *inferenceSTTStream) SetStartTime(startTime float64) {
	s.startTime = startTime
}

func (s *inferenceSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if err := s.rateGuard.Check(frame); err != nil {
		return err
	}
	s.audioDuration += float64(frame.SamplesPerChannel) / float64(frame.SampleRate)
	s.audioCh <- frame
	return nil
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
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	s.conn.Close()
	close(s.audioCh)
	close(s.eventCh)
	return nil
}

func (s *inferenceSTTStream) Next() (*stt.SpeechEvent, error) {
	ev, ok := <-s.eventCh
	if !ok {
		return nil, context.Canceled
	}
	return ev, nil
}

func (s *inferenceSTTStream) buildSpeechData(data map[string]interface{}) stt.SpeechData {
	speechData := stt.SpeechData{
		Text:       stringFromMap(data, "transcript"),
		Language:   stringFromMap(data, "language"),
		SpeakerID:  stringFromMap(data, "speaker_id"),
		Confidence: floatFromMap(data, "confidence"),
	}

	start := floatFromMap(data, "start")
	speechData.StartTime = s.startTimeOffset + start
	speechData.EndTime = s.startTimeOffset + start + floatFromMap(data, "duration")

	if extra, ok := data["extra"].(map[string]interface{}); ok && len(extra) > 0 {
		speechData.Metadata = extra
	}

	if words, ok := data["words"].([]interface{}); ok && len(words) > 0 {
		speechData.Words = make([]stt.TimedString, 0, len(words))
		for _, rawWord := range words {
			word, ok := rawWord.(map[string]interface{})
			if !ok {
				continue
			}
			speechData.Words = append(speechData.Words, stt.TimedString{
				Text:            stringFromMap(word, "word"),
				StartTime:       s.startTimeOffset + floatFromMap(word, "start"),
				EndTime:         s.startTimeOffset + floatFromMap(word, "end"),
				StartTimeOffset: s.startTimeOffset,
				Confidence:      floatFromMap(word, "confidence"),
				SpeakerID:       stringFromMap(word, "speaker_id"),
			})
		}
	}

	return speechData
}

func stringFromMap(data map[string]interface{}, key string) string {
	value, _ := data[key].(string)
	return value
}

func floatFromMap(data map[string]interface{}, key string) float64 {
	value, _ := data[key].(float64)
	return value
}

func (s *inferenceSTTStream) run() {
	defer s.Close()

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
		s.eventCh <- &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech, RequestID: requestID}
	}

	speechData := s.buildSpeechData(data)

	if isFinal {
		s.mu.Lock()
		duration := s.audioDuration
		s.audioDuration = 0
		s.mu.Unlock()

		s.eventCh <- &stt.SpeechEvent{
			Type:      stt.SpeechEventRecognitionUsage,
			RequestID: requestID,
			RecognitionUsage: &stt.RecognitionUsage{
				AudioDuration: duration,
			},
		}

		s.eventCh <- &stt.SpeechEvent{
			Type:         stt.SpeechEventFinalTranscript,
			RequestID:    requestID,
			Alternatives: []stt.SpeechData{speechData},
		}

		if s.speaking {
			s.speaking = false
			s.eventCh <- &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech, RequestID: requestID}
		}
	} else {
		s.eventCh <- &stt.SpeechEvent{
			Type:         stt.SpeechEventInterimTranscript,
			RequestID:    requestID,
			Alternatives: []stt.SpeechData{speechData},
		}
	}
}
