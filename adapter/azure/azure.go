package azure

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/logger"
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
	sampleRate := 16000
	if len(frames) > 0 {
		sampleRate = int(frames[0].SampleRate)
	}
	logger.Logger.Debugw("Azure STT Recognize calling", "url", url, "frames_count", len(frames), "sample_rate", sampleRate)

	var buf bytes.Buffer
	// Azure STT REST API requires a WAV header for audio/wav content type.
	// 16-bit PCM, 16000Hz, Mono.
	totalAudioSize := 0
	for _, f := range frames {
		totalAudioSize += len(f.Data)
	}
	writeWavHeader(&buf, totalAudioSize, sampleRate, 1)

	for _, f := range frames {
		buf.Write(f.Data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(buf.Bytes()))
	if err != nil {
		slog.Error("[STT] azure: failed to create request", "err", err)
		return nil, err
	}

	req.Header.Set("Content-Type", fmt.Sprintf("audio/wav; codecs=audio/pcm; samplerate=%d", sampleRate))
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
		respBody, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("azure stt error: %s", string(respBody))
		logger.Logger.Errorw("Azure STT API error", err, "status", resp.Status)
		return nil, err
	}

	var result struct {
		DisplayText       string `json:"DisplayText"`
		RecognitionStatus string `json:"RecognitionStatus"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		logger.Logger.Errorw("Azure STT decode failed", err)
		return nil, err
	}
	logger.Logger.Debugw("Azure STT result", "status", result.RecognitionStatus, "text", result.DisplayText)

	slog.Info("[STT] azure: Recognize result", "status", result.RecognitionStatus, "text", result.DisplayText)
	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: result.DisplayText},
		},
	}, nil
}

type AzureTTS struct {
	apiKey   string
	region   string
	voice    string
	language string
}

func NewAzureTTS(apiKey string, region string, voice string, language string) *AzureTTS {
	if voice == "" {
		voice = "en-US-AvaMultilingualNeural"
	}
	if language == "" {
		language = "en-US"
	}
	return &AzureTTS{
		apiKey:   apiKey,
		region:   region,
		voice:    voice,
		language: language,
	}
}

func (t *AzureTTS) Label() string { return "azure.TTS" }
func (t *AzureTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: true}
}
func (t *AzureTTS) SampleRate() int  { return 16000 }
func (t *AzureTTS) NumChannels() int { return 1 }

func (t *AzureTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	url := fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/v1", t.region)
	ssml := fmt.Sprintf(`<speak version='1.0' xml:lang='%s'><voice name='%s'>%s</voice></speak>`, t.language, t.voice, text)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBufferString(ssml))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", "raw-16khz-16bit-mono-pcm")
	req.Header.Set("Ocp-Apim-Subscription-Key", t.apiKey)

	logger.Logger.Debugw("Azure TTS Synthesize calling", "url", url, "voice", t.voice, "language", t.language, "text", text)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Logger.Errorw("Azure TTS request failed", err)
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		err := fmt.Errorf("azure tts error: %s", string(respBody))
		logger.Logger.Errorw("Azure TTS API error", err, "status", resp.Status)
		return nil, err
	}

	return &azureTTSChunkedStream{
		resp: resp,
	}, nil
}

func (t *AzureTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming azure tts not yet implemented")
}

type azureTTSChunkedStream struct {
	resp *http.Response
}

func (s *azureTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	// 16kHz, 16-bit Mono = 32000 bytes per second.
	// We want to send 20ms chunks = 640 bytes.
	// This ensures smooth playback even if the network read is jittery.
	chunkSize := 640
	buf := make([]byte, chunkSize)

	_, err := io.ReadFull(s.resp.Body, buf)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, io.EOF
		}
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf,
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: uint32(chunkSize / 2),
		},
	}, nil
}

func (s *azureTTSChunkedStream) Close() error {
	return s.resp.Body.Close()
}

func writeWavHeader(w io.Writer, audioSize int, sampleRate int, numChannels int) {
	// RIFF header
	w.Write([]byte("RIFF"))
	binary.Write(w, binary.LittleEndian, uint32(audioSize+36))
	w.Write([]byte("WAVE"))

	// fmt sub-chunk
	w.Write([]byte("fmt "))
	binary.Write(w, binary.LittleEndian, uint32(16)) // Subchunk1Size
	binary.Write(w, binary.LittleEndian, uint16(1))  // AudioFormat (PCM)
	binary.Write(w, binary.LittleEndian, uint16(numChannels))
	binary.Write(w, binary.LittleEndian, uint32(sampleRate))
	binary.Write(w, binary.LittleEndian, uint32(sampleRate*numChannels*2)) // ByteRate
	binary.Write(w, binary.LittleEndian, uint16(numChannels*2))            // BlockAlign
	binary.Write(w, binary.LittleEndian, uint16(16))                       // BitsPerSample

	// data sub-chunk
	w.Write([]byte("data"))
	binary.Write(w, binary.LittleEndian, uint32(audioSize))
}
