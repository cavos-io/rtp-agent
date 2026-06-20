package google

import (
	"context"
	"io"
	"strings"
	"sync"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/googleapis/gax-go/v2"
)

type GoogleTTS struct {
	mu      sync.Mutex
	streams map[*googleTTSSynthesizeStream]struct{}
	client  googleTTSClient
	voice   *texttospeechpb.VoiceSelectionParams
	model   string
	prompt  *string
	audio   *texttospeechpb.AudioConfig
}

type googleTTSClient interface {
	SynthesizeSpeech(ctx context.Context, req *texttospeechpb.SynthesizeSpeechRequest, opts ...gax.CallOption) (*texttospeechpb.SynthesizeSpeechResponse, error)
	StreamingSynthesize(ctx context.Context, opts ...gax.CallOption) (texttospeechpb.TextToSpeech_StreamingSynthesizeClient, error)
}

type GoogleTTSOption func(*googleTTSConfig)

type googleTTSConfig struct {
	language     string
	languageSet  bool
	voice        string
	voiceSet     bool
	model        string
	modelSet     bool
	prompt       *string
	promptSet    bool
	speakingRate float64
	rateSet      bool
	pitch        float64
	pitchSet     bool
	effects      []string
	effectsSet   bool
	volumeGainDB float64
	volumeSet    bool
}

func WithGoogleTTSLanguage(language string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if language != "" {
			cfg.language = language
			cfg.languageSet = true
		}
	}
}

func WithGoogleTTSVoice(voice string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if voice != "" {
			cfg.voice = voice
			cfg.voiceSet = true
		}
	}
}

func WithGoogleTTSModel(model string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if model != "" {
			cfg.model = model
			cfg.modelSet = true
		}
	}
}

func WithGoogleTTSPrompt(prompt string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		cfg.prompt = &prompt
		cfg.promptSet = true
	}
}

func WithGoogleTTSSpeakingRate(rate float64) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		cfg.speakingRate = rate
		cfg.rateSet = true
	}
}

func WithGoogleTTSPitch(pitch float64) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		cfg.pitch = pitch
		cfg.pitchSet = true
	}
}

func WithGoogleTTSEffectsProfileID(profileID string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if profileID != "" {
			cfg.effects = []string{profileID}
			cfg.effectsSet = true
		}
	}
}

func WithGoogleTTSVolumeGainDB(volumeGainDB float64) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		cfg.volumeGainDB = volumeGainDB
		cfg.volumeSet = true
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
		language:     "en-US",
		voice:        "Charon",
		model:        "gemini-2.5-flash-tts",
		speakingRate: 1.0,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return &GoogleTTS{
		streams: make(map[*googleTTSSynthesizeStream]struct{}),
		client:  client,
		voice:   googleTTSVoiceParams(cfg),
		model:   cfg.model,
		prompt:  cfg.prompt,
		audio: &texttospeechpb.AudioConfig{
			AudioEncoding:    texttospeechpb.AudioEncoding_PCM,
			SampleRateHertz:  24000,
			SpeakingRate:     cfg.speakingRate,
			Pitch:            cfg.pitch,
			EffectsProfileId: append([]string(nil), cfg.effects...),
			VolumeGainDb:     cfg.volumeGainDB,
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
	if t.model != "" {
		return t.model
	}
	return "Chirp3"
}
func (t *GoogleTTS) Provider() string { return "Google Cloud Platform" }

func (t *GoogleTTS) Close() error {
	t.mu.Lock()
	streams := make([]*googleTTSSynthesizeStream, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[*googleTTSSynthesizeStream]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *GoogleTTS) registerStream(stream *googleTTSSynthesizeStream) {
	t.mu.Lock()
	if t.streams == nil {
		t.streams = make(map[*googleTTSSynthesizeStream]struct{})
	}
	t.streams[stream] = struct{}{}
	t.mu.Unlock()
}

func (t *GoogleTTS) unregisterStream(stream *googleTTSSynthesizeStream) {
	t.mu.Lock()
	delete(t.streams, stream)
	t.mu.Unlock()
}

func (t *GoogleTTS) UpdateOptions(opts ...GoogleTTSOption) {
	cfg := googleTTSConfig{
		language:     t.voice.GetLanguageCode(),
		voice:        t.voice.GetName(),
		model:        t.Model(),
		prompt:       t.prompt,
		speakingRate: t.audio.GetSpeakingRate(),
		pitch:        t.audio.GetPitch(),
		effects:      append([]string(nil), t.audio.GetEffectsProfileId()...),
		volumeGainDB: t.audio.GetVolumeGainDb(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.languageSet || cfg.voiceSet || cfg.modelSet {
		t.voice = googleTTSVoiceParams(cfg)
	}
	if cfg.modelSet {
		t.model = cfg.model
	}
	if cfg.promptSet {
		t.prompt = cfg.prompt
	}
	if cfg.rateSet {
		t.audio.SpeakingRate = cfg.speakingRate
	}
	if cfg.pitchSet {
		t.audio.Pitch = cfg.pitch
	}
	if cfg.effectsSet {
		t.audio.EffectsProfileId = append([]string(nil), cfg.effects...)
	}
	if cfg.volumeSet {
		t.audio.VolumeGainDb = cfg.volumeGainDB
	}
}

func (t *GoogleTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{Text: text},
			Prompt:      t.prompt,
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
	stream := &googleTTSSynthesizeStream{
		owner:  t,
		ctx:    ctx,
		client: t.client,
		voice:  t.voice,
		prompt: t.prompt,
		audio:  t.audio,
	}
	stream.cond = sync.NewCond(&stream.mu)
	t.registerStream(stream)
	return stream, nil
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
	mu         sync.Mutex
	cond       *sync.Cond
	owner      *GoogleTTS
	ctx        context.Context
	client     googleTTSClient
	streams    []texttospeechpb.TextToSpeech_StreamingSynthesizeClient
	voice      *texttospeechpb.VoiceSelectionParams
	prompt     *string
	audio      *texttospeechpb.AudioConfig
	buffer     strings.Builder
	closed     bool
	inputEnded bool
}

func (s *googleTTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return io.ErrClosedPipe
	}
	_, err := s.buffer.WriteString(text)
	return err
}

func (s *googleTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	text := s.buffer.String()
	s.buffer.Reset()
	s.mu.Unlock()
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
					AudioEncoding:   texttospeechpb.AudioEncoding_PCM,
					SampleRateHertz: s.audio.GetSampleRateHertz(),
					SpeakingRate:    s.audio.GetSpeakingRate(),
				},
			},
		},
	}); err != nil {
		s.markClosed()
		_ = stream.CloseSend()
		return err
	}
	if err := stream.Send(&texttospeechpb.StreamingSynthesizeRequest{
		StreamingRequest: &texttospeechpb.StreamingSynthesizeRequest_Input{
			Input: &texttospeechpb.StreamingSynthesisInput{
				InputSource: &texttospeechpb.StreamingSynthesisInput_Text{Text: text},
				Prompt:      s.prompt,
			},
		},
	}); err != nil {
		s.markClosed()
		_ = stream.CloseSend()
		return err
	}
	if err := stream.CloseSend(); err != nil {
		s.markClosed()
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = stream.CloseSend()
		return io.ErrClosedPipe
	}
	s.streams = append(s.streams, stream)
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}

func (s *googleTTSSynthesizeStream) EndInput() error {
	if err := s.Flush(); err != nil {
		return err
	}
	s.mu.Lock()
	s.inputEnded = true
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}

func (s *googleTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	s.closed = true
	streams := append([]texttospeechpb.TextToSpeech_StreamingSynthesizeClient(nil), s.streams...)
	s.streams = nil
	s.cond.Broadcast()
	s.mu.Unlock()
	s.unregister()
	var closeErr error
	for _, stream := range streams {
		if err := stream.CloseSend(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (s *googleTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		s.mu.Lock()
		for len(s.streams) == 0 && !s.closed && !s.inputEnded {
			s.cond.Wait()
		}
		if len(s.streams) == 0 {
			s.mu.Unlock()
			return nil, io.EOF
		}
		stream := s.streams[0]
		s.mu.Unlock()

		resp, err := stream.Recv()
		if err == io.EOF {
			s.mu.Lock()
			if len(s.streams) > 0 && s.streams[0] == stream {
				s.streams = s.streams[1:]
			}
			s.mu.Unlock()
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
}

func (s *googleTTSSynthesizeStream) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	s.unregister()
}

func (s *googleTTSSynthesizeStream) unregister() {
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
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
