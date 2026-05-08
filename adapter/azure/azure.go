package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/audio"
	"github.com/cavos-io/rtp-agent/library/utils/language"
	"github.com/cavos-io/rtp-agent/model"
)

type AzureSTT struct {
	apiKey   string
	region   string
	language string
}

func NewAzureSTT(apiKey string, region string, language string) *AzureSTT {
	return &AzureSTT{
		apiKey:   apiKey,
		region:   region,
		language: language,
	}
}

func (s *AzureSTT) Label() string { return "azure.STT" }
func (s *AzureSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *AzureSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	slog.Error("[STT] azure: streaming not supported — Stream() called but Azure STT only supports Recognize()")
	return nil, fmt.Errorf("streaming azure stt not yet implemented via rest")
}

func (s *AzureSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, languageStr string) (*stt.SpeechEvent, error) {
	languageStr = language.NormalizeLanguage(languageStr)
	if languageStr == "" {
		languageStr = s.language
	}

	url := fmt.Sprintf("https://%s.stt.speech.microsoft.com/speech/recognition/conversation/cognitiveservices/v1?language=%s", s.region, languageStr)
	slog.Debug("azure stt recognize request", "url", url)

	buf := audio.FramesToWAV(frames)
	slog.Info("[STT] azure: sending WAV", "total_bytes", buf.Len(), "frames", len(frames))

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(buf.Bytes()))
	if err != nil {
		slog.Error("[STT] azure: failed to create request", "err", err)
		return nil, err
	}

	req.Header.Set("Content-Type", "audio/wav")
	req.Header.Set("Ocp-Apim-Subscription-Key", s.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("[STT] azure: request failed", "err", err)
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	slog.Info("[STT] azure: response body", "status", resp.StatusCode, "body", string(respBody))

	if resp.StatusCode != http.StatusOK {
		slog.Error("[STT] azure: request failed", "status", resp.StatusCode, "body", string(respBody))
		return nil, fmt.Errorf("azure stt error: %s", string(respBody))
	}

	var result struct {
		DisplayText       string `json:"DisplayText"`
		RecognitionStatus string `json:"RecognitionStatus"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	slog.Info("[STT] azure: Recognize result", "status", result.RecognitionStatus, "text", result.DisplayText)
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: result.DisplayText},
		},
	}, nil
}

type AzureTTS struct {
	apiKey string
	region string
	voice  string
}

func NewAzureTTS(apiKey string, region string, voice string) *AzureTTS {
	if voice == "" {
		voice = "en-US-AvaMultilingualNeural"
	}
	return &AzureTTS{
		apiKey: apiKey,
		region: region,
		voice:  voice,
	}
}

func (t *AzureTTS) Label() string { return "azure.TTS" }
func (t *AzureTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *AzureTTS) SampleRate() int  { return 48000 }
func (t *AzureTTS) NumChannels() int { return 1 }

func (t *AzureTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/v1", t.region)
	ssml := fmt.Sprintf(`<speak version='1.0' xml:lang='en-US'><voice name='%s'>%s</voice></speak>`, t.voice, text)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(ssml))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/ssml+xml")
	// Request 48kHz PCM — native WebRTC/Opus sample rate. Avoids resampling
	// artifacts and eliminates Opus frame-boundary silence padding that occurs
	// when chunk sizes are not exact multiples of the Opus frame size.
	req.Header.Set("X-Microsoft-OutputFormat", "raw-48khz-16bit-mono-pcm")
	req.Header.Set("Ocp-Apim-Subscription-Key", t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure tts error: %s", string(respBody))
	}

	// Read the full response body upfront. Azure TTS is non-streaming; reading
	// the HTTP body incrementally risks io.Read returning (n>0, io.EOF) on the
	// last call, which would silently drop the final audio chunk and cause an
	// audible click/pop at the end of every synthesized sentence.
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("azure tts: failed to read response body: %w", err)
	}

	// Trim to an even number of bytes to guarantee 16-bit PCM sample alignment.
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}

	// Deliver the entire PCM buffer as a single frame so the playout loop can
	// split it into exact 20ms Opus sub-frames (1920 bytes at 48kHz) with no
	// inter-chunk silence padding that would otherwise cause crackling.
	return &azureTTSChunkedStream{data: data}, nil
}

func (t *AzureTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming azure tts not yet implemented")
}

type azureTTSChunkedStream struct {
	data []byte
	done bool
}

func (s *azureTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	n := len(s.data)
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              s.data,
			SampleRate:        48000,
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
		IsFinal: true,
	}, nil
}

func (s *azureTTSChunkedStream) Close() error {
	return nil
}
