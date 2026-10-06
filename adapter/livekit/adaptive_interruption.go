package livekit

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/gorilla/websocket"
)

const adaptiveSampleRate = 16000
const adaptiveBatchSamples = 1600
const adaptiveMaxSamples = 3 * adaptiveSampleRate
const adaptivePrefixSamples = adaptiveSampleRate

type AdaptiveInterruptionDetector struct {
	apiKey    string
	apiSecret string
	baseURL   string
}

type AdaptiveInterruptionOption func(*AdaptiveInterruptionDetector)

// WithAdaptiveInterruptionURL overrides the LiveKit inference gateway URL.
func WithAdaptiveInterruptionURL(url string) AdaptiveInterruptionOption {
	return func(d *AdaptiveInterruptionDetector) { d.baseURL = url }
}

// NewAdaptiveInterruptionDetector uses LiveKit Cloud's acoustic barge-in model.
// Empty credentials use the existing LiveKit inference environment variables.
func NewAdaptiveInterruptionDetector(apiKey, apiSecret string, opts ...AdaptiveInterruptionOption) *AdaptiveInterruptionDetector {
	d := &AdaptiveInterruptionDetector{apiKey: apiKey, apiSecret: apiSecret, baseURL: defaultInferenceWebsocketURL()}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

var _ agent.InterruptionDetector = (*AdaptiveInterruptionDetector)(nil)

func (*AdaptiveInterruptionDetector) Model() string    { return "adaptive interruption" }
func (*AdaptiveInterruptionDetector) Provider() string { return "livekit" }

func (d *AdaptiveInterruptionDetector) Stream(ctx context.Context) (agent.InterruptionStream, error) {
	key, secret := resolveInferenceCredentials(d.apiKey, d.apiSecret)
	if key == "" || secret == "" {
		return nil, errors.New("LiveKit adaptive interruption requires inference API key and secret")
	}
	token, err := CreateAccessToken(key, secret, InferenceAccessTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("create adaptive interruption token: %w", err)
	}
	url := strings.TrimRight(inferenceWebsocketURL(d.baseURL), "/") + "/bargein"
	headers := InferenceHeaders()
	headers.Set("Authorization", "Bearer "+token)
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, url, headers)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("LiveKit adaptive interruption quota exceeded: %w", err)
		}
		return nil, fmt.Errorf("connect LiveKit adaptive interruption: %w", err)
	}
	create := map[string]any{"type": "session.create", "settings": map[string]any{
		"sample_rate": adaptiveSampleRate, "num_channels": 1, "min_frames": 2, "encoding": "s16le",
	}}
	if err := conn.WriteJSON(create); err != nil {
		_ = conn.Close()
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	s := &adaptiveInterruptionStream{
		conn: conn, ctx: streamCtx, cancel: cancel, requestReady: make(chan struct{}, 1), events: make(chan adaptiveResult, 8),
		pending: make(map[uint64]time.Time),
	}
	go s.writeLoop()
	go s.readLoop()
	go s.watchTimeouts()
	return s, nil
}

type adaptiveResult struct {
	event agent.OverlappingSpeechEvent
	err   error
}

type adaptiveInterruptionStream struct {
	mu               sync.Mutex
	conn             *websocket.Conn
	ctx              context.Context
	cancel           context.CancelFunc
	requests         [][]byte
	requestReady     chan struct{}
	events           chan adaptiveResult
	pending          map[uint64]time.Time
	buffer           []byte
	accumulated      int
	numRequests      int
	overlapStartedAt time.Time
	agentSpeaking    bool
	overlapCount     int
	closed           bool
	once             sync.Once
}

var _ agent.InterruptionStream = (*adaptiveInterruptionStream)(nil)

func (s *adaptiveInterruptionStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil {
		return nil
	}
	converted, err := audio.ResampleAudioFrame(frame, adaptiveSampleRate)
	if err != nil {
		return err
	}
	if converted.NumChannels == 0 || len(converted.Data) < int(converted.SamplesPerChannel*converted.NumChannels*2) {
		return errors.New("invalid adaptive interruption PCM frame")
	}
	pcm := converted.Data[:int(converted.SamplesPerChannel*converted.NumChannels*2)]
	if converted.NumChannels != 1 {
		pcm = make([]byte, converted.SamplesPerChannel*2)
		for i := 0; i < int(converted.SamplesPerChannel); i++ {
			var total int32
			for ch := 0; ch < int(converted.NumChannels); ch++ {
				offset := (i*int(converted.NumChannels) + ch) * 2
				total += int32(int16(binary.LittleEndian.Uint16(converted.Data[offset:])))
			}
			binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(total/int32(converted.NumChannels))))
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.EOF
	}
	if !s.agentSpeaking {
		return nil
	}
	s.buffer = append(s.buffer, pcm...)
	if len(s.buffer) > adaptiveMaxSamples*2 {
		s.buffer = append([]byte(nil), s.buffer[len(s.buffer)-adaptiveMaxSamples*2:]...)
	}
	if s.overlapStartedAt.IsZero() {
		return nil
	}
	s.accumulated += len(pcm) / 2
	if s.accumulated < adaptiveBatchSamples {
		return nil
	}
	s.accumulated = 0
	id := uint64(time.Now().UnixNano())
	request := make([]byte, 8+len(s.buffer))
	binary.LittleEndian.PutUint64(request[:8], id)
	copy(request[8:], s.buffer)
	// Like Python's inference channel, buffer audio bursts without blocking the
	// input loop. The inference and write deadlines still bound transport stalls.
	s.requests = append(s.requests, request)
	s.pending[id] = time.Now()
	s.numRequests++
	select {
	case s.requestReady <- struct{}{}:
	default:
	}
	return nil
}

func (s *adaptiveInterruptionStream) StartOverlap(startedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.EOF
	}
	if !s.agentSpeaking || !s.overlapStartedAt.IsZero() {
		return nil
	}
	s.overlapCount++
	if s.overlapCount == 1 {
		// Include VAD's already-observed speech and one second of preceding audio.
		speechSamples := int(max(time.Since(startedAt), 0).Seconds() * adaptiveSampleRate)
		prefixBytes := min(adaptiveMaxSamples, adaptivePrefixSamples+speechSamples) * 2
		if len(s.buffer) > prefixBytes {
			s.buffer = append([]byte(nil), s.buffer[len(s.buffer)-prefixBytes:]...)
		}
	}
	s.overlapStartedAt = startedAt
	s.accumulated = 0
	s.numRequests = 0
	s.pending = make(map[uint64]time.Time)
	return nil
}

func (s *adaptiveInterruptionStream) StartAgentSpeech() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.EOF
	}
	s.agentSpeaking = true
	s.buffer = nil
	s.overlapCount = 0
	s.accumulated = 0
	s.numRequests = 0
	s.overlapStartedAt = time.Time{}
	s.pending = make(map[uint64]time.Time)
	return nil
}

func (s *adaptiveInterruptionStream) EndAgentSpeech(endedAt time.Time) error {
	return s.EndOverlap(endedAt, true)
}

func (s *adaptiveInterruptionStream) EndOverlap(endedAt time.Time, agentEnded bool) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.EOF
	}
	startedAt := s.overlapStartedAt
	numRequests := s.numRequests
	s.overlapStartedAt = time.Time{}
	s.accumulated = 0
	s.numRequests = 0
	s.pending = make(map[uint64]time.Time)
	if agentEnded {
		s.agentSpeaking = false
		s.buffer = nil
		s.overlapCount = 0
	}
	s.mu.Unlock()
	if startedAt.IsZero() {
		return nil
	}
	ev := agent.OverlappingSpeechEvent{CreatedAt: time.Now(), DetectedAt: endedAt, OverlapStartedAt: &startedAt, NumRequests: numRequests, AgentEnded: agentEnded}
	select {
	case s.events <- adaptiveResult{event: ev}:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
		return errors.New("adaptive interruption event queue full")
	}
}

func (s *adaptiveInterruptionStream) Next() (agent.OverlappingSpeechEvent, error) {
	select {
	case result := <-s.events:
		return result.event, result.err
	default:
	}
	select {
	case result := <-s.events:
		return result.event, result.err
	case <-s.ctx.Done():
		return agent.OverlappingSpeechEvent{}, s.ctx.Err()
	}
}

func (s *adaptiveInterruptionStream) Close() error {
	var err error
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.requests = nil
		s.mu.Unlock()
		err = s.conn.Close()
		s.cancel()
	})
	return err
}

func (s *adaptiveInterruptionStream) watchTimeouts() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			timedOut := false
			for _, sentAt := range s.pending {
				if time.Since(sentAt) > 700*time.Millisecond {
					timedOut = true
					break
				}
			}
			s.mu.Unlock()
			if timedOut {
				s.fail(errors.New("adaptive interruption inference timed out"))
				return
			}
		}
	}
}

func (s *adaptiveInterruptionStream) writeLoop() {
	for {
		select {
		case <-s.ctx.Done():
			_ = s.Close()
			return
		case <-s.requestReady:
			for {
				s.mu.Lock()
				if len(s.requests) == 0 || s.closed || s.ctx.Err() != nil {
					s.mu.Unlock()
					break
				}
				request := s.requests[0]
				s.requests[0] = nil
				s.requests = s.requests[1:]
				s.mu.Unlock()
				_ = s.conn.SetWriteDeadline(time.Now().Add(700 * time.Millisecond))
				if err := s.conn.WriteMessage(websocket.BinaryMessage, request); err != nil {
					s.fail(err)
					return
				}
			}
		}
	}
}

func (s *adaptiveInterruptionStream) readLoop() {
	for {
		_, payload, err := s.conn.ReadMessage()
		if err != nil {
			s.fail(err)
			return
		}
		var msg struct {
			Type               string    `json:"type"`
			CreatedAt          uint64    `json:"created_at"`
			PredictionDuration float64   `json:"prediction_duration"`
			Probabilities      []float32 `json:"probabilities"`
			DefaultThreshold   *float64  `json:"default_threshold"`
			Message            string    `json:"message"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			s.fail(err)
			return
		}
		switch msg.Type {
		case "session.created":
			if msg.DefaultThreshold == nil {
				s.fail(errors.New("adaptive interruption session has no threshold"))
				return
			}
		case "bargein_detected":
			s.mu.Lock()
			requestAt, known := s.pending[msg.CreatedAt]
			startedAt := s.overlapStartedAt
			numRequests := s.numRequests
			if known && !startedAt.IsZero() {
				s.overlapStartedAt = time.Time{}
				s.pending = make(map[uint64]time.Time)
				s.numRequests = 0
			}
			s.mu.Unlock()
			if !known || startedAt.IsZero() {
				continue
			}
			probability := 0.0
			for _, value := range msg.Probabilities {
				if float64(value) > probability {
					probability = float64(value)
				}
			}
			ev := agent.OverlappingSpeechEvent{
				CreatedAt: time.Now(), DetectedAt: time.Now(), IsInterruption: true, OverlapStartedAt: &startedAt,
				TotalDuration: time.Since(requestAt), PredictionDuration: time.Duration(msg.PredictionDuration * float64(time.Second)),
				DetectionDelay: time.Since(startedAt), Probabilities: msg.Probabilities, Probability: probability, NumRequests: numRequests,
			}
			select {
			case s.events <- adaptiveResult{event: ev}:
			case <-s.ctx.Done():
				return
			}
		case "inference_done":
			s.mu.Lock()
			delete(s.pending, msg.CreatedAt)
			s.mu.Unlock()
		case "error":
			s.fail(fmt.Errorf("LiveKit adaptive interruption: %s", msg.Message))
			return
		}
	}
}

func (s *adaptiveInterruptionStream) fail(err error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	select {
	case s.events <- adaptiveResult{err: err}:
	default:
	}
	_ = s.Close()
}
