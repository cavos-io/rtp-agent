package minimax

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultMinimaxBaseURL     = "https://api-uw.minimax.io"
	defaultMinimaxModel       = "speech-02-turbo"
	defaultMinimaxVoice       = "socialmedia_female_2_v1"
	defaultMinimaxSampleRate  = 24000
	defaultMinimaxBitrate     = 128000
	defaultMinimaxAudioFormat = "mp3"
)

type MinimaxTTS struct {
	apiKey            string
	baseURL           string
	model             string
	voice             string
	sampleRate        int
	bitrate           int
	audioFormat       string
	emotion           string
	speed             float64
	vol               float64
	pitch             int
	textNormalization bool
}

type MinimaxTTSOption func(*MinimaxTTS)

func WithMinimaxTTSBaseURL(baseURL string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithMinimaxTTSModel(model string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithMinimaxTTSVoice(voice string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithMinimaxTTSSampleRate(sampleRate int) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if sampleRate > 0 {
			t.sampleRate = sampleRate
		}
	}
}

func WithMinimaxTTSBitrate(bitrate int) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if bitrate > 0 {
			t.bitrate = bitrate
		}
	}
}

func WithMinimaxTTSAudioFormat(audioFormat string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		if audioFormat != "" {
			t.audioFormat = audioFormat
		}
	}
}

func WithMinimaxTTSEmotion(emotion string) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.emotion = emotion
	}
}

func WithMinimaxTTSSpeed(speed float64) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.speed = speed
	}
}

func WithMinimaxTTSVolume(vol float64) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.vol = vol
	}
}

func WithMinimaxTTSPitch(pitch int) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.pitch = pitch
	}
}

func WithMinimaxTTSTextNormalization(enabled bool) MinimaxTTSOption {
	return func(t *MinimaxTTS) {
		t.textNormalization = enabled
	}
}

func NewMinimaxTTS(apiKey string, voice string, opts ...MinimaxTTSOption) *MinimaxTTS {
	provider := &MinimaxTTS{
		apiKey:      apiKey,
		baseURL:     defaultMinimaxBaseURL,
		model:       defaultMinimaxModel,
		voice:       voice,
		sampleRate:  defaultMinimaxSampleRate,
		bitrate:     defaultMinimaxBitrate,
		audioFormat: defaultMinimaxAudioFormat,
		speed:       1.0,
		vol:         1.0,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.voice == "" {
		provider.voice = defaultMinimaxVoice
	}
	return provider
}

func (t *MinimaxTTS) Label() string { return "minimax.TTS" }
func (t *MinimaxTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *MinimaxTTS) SampleRate() int  { return t.sampleRate }
func (t *MinimaxTTS) NumChannels() int { return 1 }

func (t *MinimaxTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req, err := buildMinimaxTTSRequest(ctx, t, text)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("minimax tts error: %s", string(respBody))
	}

	return &minimaxTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func buildMinimaxTTSRequest(ctx context.Context, t *MinimaxTTS, text string) (*http.Request, error) {
	reqBody := minimaxOptions(t)
	reqBody["text"] = text
	reqBody["stream"] = true
	reqBody["stream_options"] = map[string]interface{}{
		"exclude_aggregated_audio": true,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.baseURL, "/")+"/v1/t2a_v2", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	return req, nil
}

func minimaxOptions(t *MinimaxTTS) map[string]interface{} {
	voiceSetting := map[string]interface{}{
		"voice_id": t.voice,
		"speed":    t.speed,
		"vol":      t.vol,
		"pitch":    t.pitch,
	}
	if t.emotion != "" {
		voiceSetting["emotion"] = t.emotion
	}

	return map[string]interface{}{
		"model":         t.model,
		"voice_setting": voiceSetting,
		"audio_setting": map[string]interface{}{
			"sample_rate": t.sampleRate,
			"bitrate":     t.bitrate,
			"format":      t.audioFormat,
			"channel":     1,
		},
		"text_normalization": t.textNormalization,
	}
}

func (t *MinimaxTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("minimax streaming tts not natively supported by basic rest api")
}

type minimaxTTSChunkedStream struct {
	resp       *http.Response
	sampleRate int
	scanner    *bufio.Scanner
	requestID  string
}

func (s *minimaxTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.requestID == "" {
		s.requestID = s.resp.Header.Get("Trace-Id")
		if s.requestID == "" {
			s.requestID = s.resp.Header.Get("X-Trace-Id")
		}
	}
	if s.scanner == nil {
		s.scanner = bufio.NewScanner(s.resp.Body)
	}
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		audio, err := minimaxAudioFromSSELine(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		if err != nil {
			return nil, err
		}
		if len(audio) == 0 {
			continue
		}
		return &tts.SynthesizedAudio{
			RequestID: s.requestID,
			Frame: &model.AudioFrame{
				Data:              audio,
				SampleRate:        uint32(s.sampleRate),
				NumChannels:       1,
				SamplesPerChannel: uint32(len(audio) / 2),
			},
		}, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (s *minimaxTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func minimaxAudioFromSSELine(line string) ([]byte, error) {
	var data struct {
		Data struct {
			Audio string `json:"audio"`
		} `json:"data"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}
	if err := json.Unmarshal([]byte(line), &data); err != nil {
		return nil, err
	}
	if data.BaseResp.StatusCode != 0 {
		if data.BaseResp.StatusMsg == "" {
			data.BaseResp.StatusMsg = "unknown error"
		}
		return nil, fmt.Errorf("minimax error [%d]: %s", data.BaseResp.StatusCode, data.BaseResp.StatusMsg)
	}
	if data.Data.Audio == "" {
		return nil, nil
	}
	return hex.DecodeString(data.Data.Audio)
}
