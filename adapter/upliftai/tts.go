package upliftai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultUpliftAIVoiceID    = "v_meklc281"
	defaultUpliftAISampleRate = 22050
)

type UpliftAITTS struct {
	apiKey  string
	voice   string
	mu      sync.Mutex
	closed  bool
	streams map[*upliftAITTSChunkedStream]struct{}
}

func NewUpliftAITTS(apiKey string, voice string) *UpliftAITTS {
	if apiKey == "" {
		apiKey = os.Getenv("UPLIFTAI_API_KEY")
	}
	if voice == "" {
		voice = defaultUpliftAIVoiceID
	}
	return &UpliftAITTS{
		apiKey:  apiKey,
		voice:   voice,
		streams: make(map[*upliftAITTSChunkedStream]struct{}),
	}
}

func (t *UpliftAITTS) Label() string { return "upliftai.TTS" }
func (t *UpliftAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *UpliftAITTS) SampleRate() int  { return defaultUpliftAISampleRate }
func (t *UpliftAITTS) NumChannels() int { return 1 }

func (t *UpliftAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.apiKey == "" {
		return nil, fmt.Errorf("API key is required, either as argument or set UPLIFTAI_API_KEY environment variable")
	}

	stream := &upliftAITTSChunkedStream{
		owner: t,
		ctx:   ctx,
		text:  text,
	}
	if !t.registerStream(stream) {
		_ = stream.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *UpliftAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	return nil, fmt.Errorf("upliftai streaming tts not natively supported by basic rest api")
}

func (t *UpliftAITTS) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	streams := make([]*upliftAITTSChunkedStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*upliftAITTSChunkedStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *UpliftAITTS) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *UpliftAITTS) registerStream(stream *upliftAITTSChunkedStream) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.streams[stream] = struct{}{}
	return true
}

func (t *UpliftAITTS) unregisterStream(stream *upliftAITTSChunkedStream) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

type upliftAITTSChunkedStream struct {
	owner        *UpliftAITTS
	ctx          context.Context
	text         string
	resp         *http.Response
	once         sync.Once
	err          error
	pendingFinal bool
	finalSent    bool
	closed       bool
}

func (s *upliftAITTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed || s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	buf := make([]byte, 4096)
	for {
		n, err := s.resp.Body.Read(buf)
		if n > 0 {
			if err == io.EOF {
				s.pendingFinal = true
			}
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              buf[:n],
					SampleRate:        defaultUpliftAISampleRate,
					NumChannels:       1,
					SamplesPerChannel: uint32(n / 2),
				},
			}, nil
		}
		if err != nil {
			if err == io.EOF {
				if !s.finalSent {
					s.finalSent = true
					return &tts.SynthesizedAudio{IsFinal: true}, nil
				}
				return nil, io.EOF
			}
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS stream read failed: %v", err))
		}
	}
}

func (s *upliftAITTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.owner == nil {
		return nil
	}
	reqBody := map[string]interface{}{
		"text":  s.text,
		"voice": s.owner.voice,
	}
	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(s.ctx, "POST", "https://api.upliftai.org/v1/tts", bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.owner.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS request failed: %v", err))
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("UpliftAI TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *upliftAITTSChunkedStream) Close() error {
	s.once.Do(func() {
		s.closed = true
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
		if s.resp != nil && s.resp.Body != nil {
			s.err = s.resp.Body.Close()
		}
	})
	return s.err
}
