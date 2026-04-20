package openai

import (
	"context"
	"io"

<<<<<<< HEAD
	"github.com/cavos-io/rtp-agent/core/audio"
=======
>>>>>>> origin/main
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/sashabaranov/go-openai"
)

type OpenAITTS struct {
	client     *openai.Client
	model      openai.SpeechModel
	voice      openai.SpeechVoice
	sampleRate int
}

type OpenAITTSOptions struct {
	SampleRate int
}

func NewOpenAITTS(apiKey string, model openai.SpeechModel, voice openai.SpeechVoice, opts ...OpenAITTSOptions) *OpenAITTS {
	if model == "" {
		model = openai.TTSModel1
	}
	if voice == "" {
		voice = openai.VoiceAlloy
	}
	
	sampleRate := 24000
	if len(opts) > 0 && opts[0].SampleRate > 0 {
		sampleRate = opts[0].SampleRate
	}

	return &OpenAITTS{
		client:     openai.NewClient(apiKey),
		model:      model,
		voice:      voice,
		sampleRate: sampleRate,
	}
}

func (t *OpenAITTS) Label() string { return "openai.TTS" }
func (t *OpenAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *OpenAITTS) SampleRate() int  { return t.sampleRate }
func (t *OpenAITTS) NumChannels() int { return 1 }

func (t *OpenAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req := openai.CreateSpeechRequest{
		Model: t.model,
		Input: text,
		Voice: t.voice,
		// Console audio output expects raw 16-bit PCM bytes.
		ResponseFormat: openai.SpeechResponseFormatPcm,
	}

	resp, err := t.client.CreateSpeech(ctx, req)
	if err != nil {
		return nil, err
	}

	return &openaiTTSChunkedStream{
		resp:       resp,
		sampleRate: t.sampleRate,
	}, nil
}

func (t *OpenAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	// OpenAI does not have a native streaming API for TTS via standard REST.
	return nil, io.ErrUnexpectedEOF
}

type openaiTTSChunkedStream struct {
	resp       io.ReadCloser
	pending    []byte
	sampleRate int
}

func (s *openaiTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Read(buf)
	if n == 0 {
		if err != nil {
			if err == io.EOF {
				return nil, io.EOF
			}
			return nil, err
		}
		return nil, io.ErrNoProgress
	}

	data := append(s.pending, buf[:n]...)
	s.pending = nil

	// Keep byte alignment for int16 PCM samples.
	if len(data)%2 != 0 {
		s.pending = []byte{data[len(data)-1]}
		data = data[:len(data)-1]
	}

	if len(data) == 0 {
		if err == io.EOF {
			return nil, io.EOF
		}
		return s.Next()
	}

	if s.sampleRate != 24000 {
		pcm16 := audio.BytesToInt16(data)
		resampled := audio.ResampleLinear(pcm16, 24000, s.sampleRate)
		data = audio.Int16ToBytes(resampled)
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              data,
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(data) / 2),
		},
	}, nil
}

func (s *openaiTTSChunkedStream) Close() error {
	return s.resp.Close()
}

