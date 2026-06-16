package google

import (
	"context"
	"fmt"
	"io"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/googleapis/gax-go/v2"
)

type GoogleTTS struct {
	client googleTTSClient
	voice  *texttospeechpb.VoiceSelectionParams
	audio  *texttospeechpb.AudioConfig
}

type googleTTSClient interface {
	SynthesizeSpeech(ctx context.Context, req *texttospeechpb.SynthesizeSpeechRequest, opts ...gax.CallOption) (*texttospeechpb.SynthesizeSpeechResponse, error)
}

type GoogleTTSOption func(*googleTTSConfig)

type googleTTSConfig struct {
	language string
	voice    string
	model    string
}

func WithGoogleTTSLanguage(language string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if language != "" {
			cfg.language = language
		}
	}
}

func WithGoogleTTSVoice(voice string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if voice != "" {
			cfg.voice = voice
		}
	}
}

func WithGoogleTTSModel(model string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if model != "" {
			cfg.model = model
		}
	}
}

// NewGoogleTTS creates a new TTS client using Application Default Credentials,
// or by providing a path to a credentials JSON file.
func NewGoogleTTS(credentialsFile string, ttsOpts ...GoogleTTSOption) (*GoogleTTS, error) {
	ctx := context.Background()
	clientOpts, err := googleClientOptionsFromCredentialsFile(credentialsFile)
	if err != nil {
		return nil, err
	}

	client, err := texttospeech.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}

	return newGoogleTTSWithClient(client, ttsOpts...), nil
}

func newGoogleTTSWithClient(client googleTTSClient, opts ...GoogleTTSOption) *GoogleTTS {
	cfg := googleTTSConfig{
		language: "en-US",
		voice:    "Charon",
		model:    "gemini-2.5-flash-tts",
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return &GoogleTTS{
		client: client,
		voice:  googleTTSVoiceParams(cfg),
		audio: &texttospeechpb.AudioConfig{
			AudioEncoding:   texttospeechpb.AudioEncoding_LINEAR16,
			SampleRateHertz: 24000,
		},
	}
}

func (t *GoogleTTS) Label() string { return "google.TTS" }
func (t *GoogleTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *GoogleTTS) SampleRate() int  { return 24000 }
func (t *GoogleTTS) NumChannels() int { return 1 }
func (t *GoogleTTS) Model() string {
	if model := t.voice.GetModelName(); model != "" {
		return model
	}
	return "Chirp3"
}
func (t *GoogleTTS) Provider() string { return "Google Cloud Platform" }

func (t *GoogleTTS) UpdateOptions(opts ...GoogleTTSOption) {
	cfg := googleTTSConfig{
		language: t.voice.GetLanguageCode(),
		voice:    t.voice.GetName(),
		model:    t.Model(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	t.voice = googleTTSVoiceParams(cfg)
}

func (t *GoogleTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{Text: text},
		},
		Voice:       t.voice,
		AudioConfig: t.audio,
	}

	resp, err := t.client.SynthesizeSpeech(ctx, req)
	if err != nil {
		return nil, err
	}

	return &googleTTSChunkedStream{
		data: resp.AudioContent,
	}, nil
}

func (t *GoogleTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	// Google Cloud TTS doesn't support a streaming input API natively in the same way as STT.
	// You provide text and it streams audio back via StreamingSynthesize,
	// but the text is sent in one go per request.
	return nil, fmt.Errorf("streaming tts input not yet implemented for google cloud")
}

type googleTTSChunkedStream struct {
	data           []byte
	offset         int
	headerStripped bool
}

func (s *googleTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if !s.headerStripped && len(s.data) > 44 {
		// Google TTS LINEAR16 usually returns a WAV with a 44-byte header.
		// Verify RIFF and WAVE tags.
		if string(s.data[0:4]) == "RIFF" && string(s.data[8:12]) == "WAVE" {
			s.data = s.data[44:]
		}
		s.headerStripped = true
	}

	if s.offset >= len(s.data) {
		return nil, io.EOF
	}

	chunkSize := 4096
	end := s.offset + chunkSize
	if end > len(s.data) {
		end = len(s.data)
	}

	chunk := s.data[s.offset:end]
	s.offset = end

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              chunk,
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: uint32(len(chunk) / 2),
		},
	}, nil
}

func (s *googleTTSChunkedStream) Close() error {
	return nil
}

func googleTTSVoiceParams(cfg googleTTSConfig) *texttospeechpb.VoiceSelectionParams {
	voice := &texttospeechpb.VoiceSelectionParams{
		LanguageCode: cfg.language,
		Name:         cfg.voice,
	}
	if cfg.model != "chirp_3" {
		voice.ModelName = cfg.model
	}
	return voice
}
