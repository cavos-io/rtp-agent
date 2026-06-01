package openai

import (
	"context"
	"io"

	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
	"github.com/sashabaranov/go-openai"
)

type OpenAITTS struct {
	client         *openai.Client
	model          openai.SpeechModel
	voice          openai.SpeechVoice
	speed          float64
	instructions   string
	responseFormat openai.SpeechResponseFormat
}

type OpenAITTSOption func(*OpenAITTS)

func WithOpenAITTSModel(model openai.SpeechModel) OpenAITTSOption {
	return func(t *OpenAITTS) {
		if model != "" {
			t.model = model
		}
	}
}

func WithOpenAITTSVoice(voice openai.SpeechVoice) OpenAITTSOption {
	return func(t *OpenAITTS) {
		if voice != "" {
			t.voice = voice
		}
	}
}

func WithOpenAITTSSpeed(speed float64) OpenAITTSOption {
	return func(t *OpenAITTS) {
		t.speed = speed
	}
}

func WithOpenAITTSInstructions(instructions string) OpenAITTSOption {
	return func(t *OpenAITTS) {
		t.instructions = instructions
	}
}

func WithOpenAITTSResponseFormat(format openai.SpeechResponseFormat) OpenAITTSOption {
	return func(t *OpenAITTS) {
		t.responseFormat = format
	}
}

func NewOpenAITTS(apiKey string, model openai.SpeechModel, voice openai.SpeechVoice, opts ...OpenAITTSOption) *OpenAITTS {
	return newOpenAITTS(openai.NewClient(apiKey), model, voice, opts...)
}

func NewOpenAITTSWithConfig(config openai.ClientConfig, model openai.SpeechModel, voice openai.SpeechVoice, opts ...OpenAITTSOption) *OpenAITTS {
	return newOpenAITTS(openai.NewClientWithConfig(config), model, voice, opts...)
}

func newOpenAITTS(client *openai.Client, model openai.SpeechModel, voice openai.SpeechVoice, opts ...OpenAITTSOption) *OpenAITTS {
	if model == "" {
		model = openai.TTSModelGPT4oMini
	}
	if voice == "" {
		voice = openai.VoiceAsh
	}
	provider := &OpenAITTS{
		client:         client,
		model:          model,
		voice:          voice,
		speed:          1.0,
		responseFormat: openai.SpeechResponseFormatMp3,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.speed == 0 {
		provider.speed = 1.0
	}
	if provider.responseFormat == "" {
		provider.responseFormat = openai.SpeechResponseFormatMp3
	}
	return provider
}

func (t *OpenAITTS) UpdateOptions(opts ...OpenAITTSOption) {
	for _, opt := range opts {
		opt(t)
	}
}

func (t *OpenAITTS) Label() string { return "openai.TTS" }
func (t *OpenAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *OpenAITTS) SampleRate() int  { return 24000 }
func (t *OpenAITTS) NumChannels() int { return 1 }

func (t *OpenAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req := buildOpenAITTSSpeechRequest(t, text)

	resp, err := t.client.CreateSpeech(ctx, req)
	if err != nil {
		return nil, err
	}

	return &openaiTTSChunkedStream{
		resp: resp,
	}, nil
}

func buildOpenAITTSSpeechRequest(t *OpenAITTS, text string) openai.CreateSpeechRequest {
	return openai.CreateSpeechRequest{
		Model:          t.model,
		Input:          text,
		Voice:          t.voice,
		Instructions:   t.instructions,
		ResponseFormat: t.responseFormat,
		Speed:          t.speed,
	}
}

func (t *OpenAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	// OpenAI does not have a native streaming API for TTS via standard REST.
	return nil, io.ErrUnexpectedEOF
}

type openaiTTSChunkedStream struct {
	resp io.ReadCloser
}

func (s *openaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Read(buf)
	if err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        24000, // Common for TTS, though MP3 needs decoding
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *openaiTTSChunkedStream) Close() error {
	return s.resp.Close()
}
