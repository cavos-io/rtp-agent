package google

import (
	"context"
	"io"
	"strings"

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
	StreamingSynthesize(ctx context.Context, opts ...gax.CallOption) (texttospeechpb.TextToSpeech_StreamingSynthesizeClient, error)
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
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
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
	return &googleTTSSynthesizeStream{
		ctx:    ctx,
		client: t.client,
		voice:  t.voice,
		audio:  t.audio,
	}, nil
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

type googleTTSSynthesizeStream struct {
	ctx     context.Context
	client  googleTTSClient
	streams []texttospeechpb.TextToSpeech_StreamingSynthesizeClient
	voice   *texttospeechpb.VoiceSelectionParams
	audio   *texttospeechpb.AudioConfig
	buffer  strings.Builder
	closed  bool
}

func (s *googleTTSSynthesizeStream) PushText(text string) error {
	if s.closed {
		return io.ErrClosedPipe
	}
	_, err := s.buffer.WriteString(text)
	return err
}

func (s *googleTTSSynthesizeStream) Flush() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	text := s.buffer.String()
	s.buffer.Reset()
	if text == "" {
		return nil
	}
	stream, err := s.client.StreamingSynthesize(s.ctx)
	if err != nil {
		return err
	}
	if err := stream.Send(&texttospeechpb.StreamingSynthesizeRequest{
		StreamingRequest: &texttospeechpb.StreamingSynthesizeRequest_StreamingConfig{
			StreamingConfig: &texttospeechpb.StreamingSynthesizeConfig{
				Voice: s.voice,
				StreamingAudioConfig: &texttospeechpb.StreamingAudioConfig{
					AudioEncoding:   s.audio.GetAudioEncoding(),
					SampleRateHertz: s.audio.GetSampleRateHertz(),
				},
			},
		},
	}); err != nil {
		return err
	}
	if err := stream.Send(&texttospeechpb.StreamingSynthesizeRequest{
		StreamingRequest: &texttospeechpb.StreamingSynthesizeRequest_Input{
			Input: &texttospeechpb.StreamingSynthesisInput{
				InputSource: &texttospeechpb.StreamingSynthesisInput_Text{Text: text},
			},
		},
	}); err != nil {
		return err
	}
	if err := stream.CloseSend(); err != nil {
		return err
	}
	s.streams = append(s.streams, stream)
	return nil
}

func (s *googleTTSSynthesizeStream) Close() error {
	s.closed = true
	var closeErr error
	for _, stream := range s.streams {
		if err := stream.CloseSend(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	s.streams = nil
	return closeErr
}

func (s *googleTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	for len(s.streams) > 0 {
		resp, err := s.streams[0].Recv()
		if err == io.EOF {
			s.streams = s.streams[1:]
			continue
		}
		if err != nil {
			return nil, err
		}
		data := resp.GetAudioContent()
		return &tts.SynthesizedAudio{
			Frame: &model.AudioFrame{
				Data:              data,
				SampleRate:        uint32(s.audio.GetSampleRateHertz()),
				NumChannels:       1,
				SamplesPerChannel: uint32(len(data) / 2),
			},
		}, nil
	}
	return nil, io.EOF
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
