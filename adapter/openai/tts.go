package openai

import (
	"context"
	"io"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/sashabaranov/go-openai"
)

type OpenAITTS struct {
	client *openai.Client
	model  openai.SpeechModel
	voice  openai.SpeechVoice
}

func NewOpenAITTS(apiKey string, model openai.SpeechModel, voice openai.SpeechVoice) *OpenAITTS {
	if model == "" {
		model = openai.TTSModel1
	}
	if voice == "" {
		voice = openai.VoiceAlloy
	}
	return &OpenAITTS{
		client: openai.NewClient(apiKey),
		model:  model,
		voice:  voice,
	}
}

func (t *OpenAITTS) Label() string { return "openai.TTS" }
func (t *OpenAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *OpenAITTS) SampleRate() int { return 24000 }
func (t *OpenAITTS) NumChannels() int { return 1 }

func (t *OpenAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req := openai.CreateSpeechRequest{
		Model:          t.model,
		Input:          text,
		Voice:          t.voice,
		ResponseFormat: openai.SpeechResponseFormatMp3,
	}

	resp, err := t.client.CreateSpeech(ctx, req)
	if err != nil {
		return nil, err
	}

	return &openaiTTSChunkedStream{
		resp: resp,
	}, nil
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
