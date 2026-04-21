package google

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
	"google.golang.org/genai"
)

const (
	defaultGeminiTTSModel      = "gemini-2.5-flash-preview-tts"
	defaultGeminiTTSVoice      = "Aoede"
	defaultGeminiTTSSampleRate = 24000
	geminiAudioChunkSize       = 4096
)

type GeminiTTS struct {
	client       *genai.Client
	model        string
	voiceName    string
	instructions string
}

func NewGeminiTTS(apiKey, model, voiceName, instructions string) (*GeminiTTS, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("gemini tts: api key is required")
	}
	if strings.TrimSpace(model) == "" {
		model = defaultGeminiTTSModel
	}
	if strings.TrimSpace(voiceName) == "" {
		voiceName = defaultGeminiTTSVoice
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, err
	}

	return &GeminiTTS{
		client:       client,
		model:        model,
		voiceName:    voiceName,
		instructions: strings.TrimSpace(instructions),
	}, nil
}

func (t *GeminiTTS) Label() string { return "google.beta.GeminiTTS" }

func (t *GeminiTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}

func (t *GeminiTTS) SampleRate() int { return defaultGeminiTTSSampleRate }

func (t *GeminiTTS) NumChannels() int { return 1 }

func (t *GeminiTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{"AUDIO"},
		SpeechConfig: &genai.SpeechConfig{
			VoiceConfig: &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: t.voiceName},
			},
		},
	}
	if t.instructions != "" {
		config.SystemInstruction = genai.NewContentFromText(t.instructions, genai.RoleUser)
	}

	resp, err := t.client.Models.GenerateContent(ctx, t.model, genai.Text(text), config)
	if err != nil {
		return nil, err
	}

	audio, mimeType, err := extractGeminiAudio(resp)
	if err != nil {
		return nil, err
	}

	return &geminiTTSChunkedStream{data: audio, mimeType: mimeType}, nil
}

func (t *GeminiTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("streaming tts input not yet implemented for gemini tts")
}

type geminiTTSChunkedStream struct {
	data           []byte
	offset         int
	mimeType       string
	headerStripped bool
}

func (s *geminiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if !s.headerStripped && strings.Contains(strings.ToLower(s.mimeType), "wav") && len(s.data) > 44 {
		if string(s.data[0:4]) == "RIFF" && string(s.data[8:12]) == "WAVE" {
			s.data = s.data[44:]
		}
		s.headerStripped = true
	}

	if s.offset >= len(s.data) {
		return nil, io.EOF
	}

	end := s.offset + geminiAudioChunkSize
	if end > len(s.data) {
		end = len(s.data)
	}

	chunk := s.data[s.offset:end]
	s.offset = end

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              chunk,
			SampleRate:        defaultGeminiTTSSampleRate,
			NumChannels:       1,
			SamplesPerChannel: uint32(len(chunk) / 2),
		},
	}, nil
}

func (s *geminiTTSChunkedStream) Close() error { return nil }

func extractGeminiAudio(resp *genai.GenerateContentResponse) ([]byte, string, error) {
	if resp == nil {
		return nil, "", fmt.Errorf("gemini tts: nil response")
	}
	for _, candidate := range resp.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part == nil || part.InlineData == nil || len(part.InlineData.Data) == 0 {
				continue
			}
			mimeType := strings.TrimSpace(part.InlineData.MIMEType)
			if mimeType == "" || strings.HasPrefix(strings.ToLower(mimeType), "audio/") {
				return part.InlineData.Data, mimeType, nil
			}
		}
	}
	return nil, "", fmt.Errorf("gemini tts: response contained no audio data")
}
