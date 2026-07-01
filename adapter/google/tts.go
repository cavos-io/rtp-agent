package google

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GoogleTTS struct {
	mu      sync.Mutex
	streams map[*googleTTSSynthesizeStream]struct{}
	client  googleTTSClient
	closed  bool
	voice   *texttospeechpb.VoiceSelectionParams
	model   string
	prompt  *string
	audio   *texttospeechpb.AudioConfig
	custom  *texttospeechpb.CustomPronunciations
	ssml    bool
	markup  bool
}

type googleTTSClient interface {
	SynthesizeSpeech(ctx context.Context, req *texttospeechpb.SynthesizeSpeechRequest, opts ...gax.CallOption) (*texttospeechpb.SynthesizeSpeechResponse, error)
	StreamingSynthesize(ctx context.Context, opts ...gax.CallOption) (texttospeechpb.TextToSpeech_StreamingSynthesizeClient, error)
}

type GoogleTTSOption func(*googleTTSConfig)

type googleTTSConfig struct {
	language     string
	languageSet  bool
	gender       texttospeechpb.SsmlVoiceGender
	genderSet    bool
	voice        string
	voiceSet     bool
	cloneKey     string
	cloneKeySet  bool
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
	sampleRate   int32
	sampleSet    bool
	custom       *texttospeechpb.CustomPronunciations
	customSet    bool
	enableSSML   bool
	useMarkup    bool
}

func WithGoogleTTSLanguage(language string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if language != "" {
			cfg.language = language
			cfg.languageSet = true
		}
	}
}

func WithGoogleTTSGender(gender string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		cfg.gender = googleTTSSSMLGender(gender)
		cfg.genderSet = true
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

func WithGoogleTTSVoiceCloneKey(key string) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if key != "" {
			cfg.cloneKey = key
			cfg.cloneKeySet = true
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

func WithGoogleTTSSampleRate(sampleRate int32) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		if sampleRate > 0 {
			cfg.sampleRate = sampleRate
			cfg.sampleSet = true
		}
	}
}

func WithGoogleTTSCustomPronunciations(custom *texttospeechpb.CustomPronunciations) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		cfg.custom = custom
		cfg.customSet = true
	}
}

func WithGoogleTTSSSML(enabled bool) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		cfg.enableSSML = enabled
	}
}

func WithGoogleTTSMarkup(enabled bool) GoogleTTSOption {
	return func(cfg *googleTTSConfig) {
		cfg.useMarkup = enabled
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
		gender:       texttospeechpb.SsmlVoiceGender_NEUTRAL,
		voice:        "Charon",
		model:        "gemini-2.5-flash-tts",
		speakingRate: 1.0,
		sampleRate:   24000,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.cloneKeySet && !cfg.modelSet && !cfg.promptSet {
		cfg.model = "chirp_3"
	}

	return &GoogleTTS{
		streams: make(map[*googleTTSSynthesizeStream]struct{}),
		client:  client,
		voice:   googleTTSVoiceParams(cfg),
		model:   cfg.model,
		prompt:  cfg.prompt,
		custom:  cfg.custom,
		ssml:    cfg.enableSSML,
		markup:  cfg.useMarkup,
		audio: &texttospeechpb.AudioConfig{
			AudioEncoding:    texttospeechpb.AudioEncoding_PCM,
			SampleRateHertz:  cfg.sampleRate,
			SpeakingRate:     cfg.speakingRate,
			Pitch:            cfg.pitch,
			EffectsProfileId: append([]string(nil), cfg.effects...),
			VolumeGainDb:     cfg.volumeGainDB,
		},
	}
}

func (t *GoogleTTS) Label() string { return "google.TTS" }
func (t *GoogleTTS) Capabilities() tts.TTSCapabilities {
	if t != nil && t.ssml {
		return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
	}
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *GoogleTTS) SampleRate() int  { return int(t.audio.GetSampleRateHertz()) }
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
	t.closed = true
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

func (t *GoogleTTS) isClosed() bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *GoogleTTS) registerStream(stream *googleTTSSynthesizeStream) bool {
	if t == nil || stream == nil {
		return false
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = stream.Close()
		return false
	}
	if t.streams == nil {
		t.streams = make(map[*googleTTSSynthesizeStream]struct{})
	}
	t.streams[stream] = struct{}{}
	t.mu.Unlock()
	return true
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
		gender:       t.voice.GetSsmlGender(),
		cloneKey:     t.voice.GetVoiceClone().GetVoiceCloningKey(),
		model:        t.Model(),
		prompt:       t.prompt,
		speakingRate: t.audio.GetSpeakingRate(),
		pitch:        t.audio.GetPitch(),
		effects:      append([]string(nil), t.audio.GetEffectsProfileId()...),
		volumeGainDB: t.audio.GetVolumeGainDb(),
		sampleRate:   t.audio.GetSampleRateHertz(),
		custom:       t.custom,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.languageSet || cfg.genderSet || cfg.voiceSet || cfg.cloneKeySet || cfg.modelSet {
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
	if cfg.sampleSet {
		t.audio.SampleRateHertz = cfg.sampleRate
	}
	if cfg.customSet {
		t.custom = cfg.custom
	}
}

func (t *GoogleTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.ssml && t.markup {
		return nil, errors.New("SSML support is not available for markup input")
	}
	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input:       googleTTSSynthesisInput(text, t.prompt, t.custom, t.ssml, t.markup),
		Voice:       t.voice,
		AudioConfig: t.audio,
	}

	resp, err := t.client.SynthesizeSpeech(ctx, req)
	if err != nil {
		return nil, googleTTSSynthesisError(err)
	}

	return &googleTTSChunkedStream{
		data:       resp.AudioContent,
		inputText:  text,
		sampleRate: t.audio.GetSampleRateHertz(),
	}, nil
}

func googleTTSSynthesisInput(text string, prompt *string, custom *texttospeechpb.CustomPronunciations, ssml bool, markup bool) *texttospeechpb.SynthesisInput {
	input := &texttospeechpb.SynthesisInput{
		Prompt:               prompt,
		CustomPronunciations: custom,
	}
	if markup {
		input.InputSource = &texttospeechpb.SynthesisInput_Markup{Markup: text}
	} else if ssml {
		input.InputSource = &texttospeechpb.SynthesisInput_Ssml{Ssml: "<speak>" + text + "</speak>"}
	} else {
		input.InputSource = &texttospeechpb.SynthesisInput_Text{Text: text}
	}
	return input
}

func googleTTSSynthesisError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return llm.NewAPITimeoutError(err.Error())
	}
	if st, ok := status.FromError(err); ok {
		return llm.NewAPIStatusErrorWithRetryable(st.Message(), int(st.Code()), "", st.Message(), googleTTSStatusRetryable(st.Code()))
	}
	return err
}

func googleTTSStatusRetryable(code codes.Code) bool {
	switch code {
	case codes.InvalidArgument, codes.NotFound, codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition, codes.OutOfRange:
		return false
	default:
		return true
	}
}

func (t *GoogleTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.ssml && t.markup {
		return nil, errors.New("SSML support is not available for markup input")
	}
	if t.ssml {
		return nil, errors.New("SSML support is not available for streaming synthesis")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &googleTTSSynthesizeStream{
		cancel: cancel,
		owner:  t,
		ctx:    streamCtx,
		client: t.client,
		voice:  t.voice,
		prompt: t.prompt,
		audio:  googleCloneAudioConfig(t.audio),
		custom: t.custom,
		markup: t.markup,
	}
	stream.cond = sync.NewCond(&stream.mu)
	if !t.registerStream(stream) {
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func googleCloneAudioConfig(config *texttospeechpb.AudioConfig) *texttospeechpb.AudioConfig {
	if config == nil {
		return nil
	}
	clone := *config
	clone.EffectsProfileId = append([]string(nil), config.GetEffectsProfileId()...)
	return &clone
}

type googleTTSChunkedStream struct {
	data           []byte
	offset         int
	headerStripped bool
	finalSent      bool
	emittedAudio   bool
	inputText      string
	sampleRate     int32
}

func (s *googleTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if !s.headerStripped && len(s.data) >= 44 {
		// Google TTS LINEAR16 usually returns a WAV with a 44-byte header.
		// Verify RIFF and WAVE tags.
		if string(s.data[0:4]) == "RIFF" && string(s.data[8:12]) == "WAVE" {
			s.data = s.data[44:]
		}
		s.headerStripped = true
	}

	if s.offset >= len(s.data) {
		if strings.TrimSpace(s.inputText) != "" && !s.emittedAudio && !s.finalSent {
			s.finalSent = true
			return nil, llm.NewAPIError(fmt.Sprintf("no audio frames were pushed for text: %s", s.inputText), nil, true)
		}
		return s.emitFinal()
	}

	chunkSize := 4096
	end := s.offset + chunkSize
	if end > len(s.data) {
		end = len(s.data)
	}

	chunk := s.data[s.offset:end]
	s.offset = end
	s.emittedAudio = true
	sampleRate := s.sampleRate
	if sampleRate == 0 {
		sampleRate = 24000
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              chunk,
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(chunk) / 2),
		},
	}, nil
}

func (s *googleTTSChunkedStream) emitFinal() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	s.finalSent = true
	return &tts.SynthesizedAudio{IsFinal: true}, nil
}

func (s *googleTTSChunkedStream) Close() error {
	s.finalSent = true
	return nil
}

type googleTTSSynthesizeStream struct {
	mu         sync.Mutex
	cond       *sync.Cond
	cancel     context.CancelFunc
	owner      *GoogleTTS
	ctx        context.Context
	client     googleTTSClient
	streams    []texttospeechpb.TextToSpeech_StreamingSynthesizeClient
	active     texttospeechpb.TextToSpeech_StreamingSynthesizeClient
	segments   map[texttospeechpb.TextToSpeech_StreamingSynthesizeClient]*googleTTSSegmentState
	voice      *texttospeechpb.VoiceSelectionParams
	prompt     *string
	audio      *texttospeechpb.AudioConfig
	custom     *texttospeechpb.CustomPronunciations
	markup     bool
	buffer     strings.Builder
	closed     bool
	inputEnded bool
	sentInput  bool
	flushed    int
}

type googleTTSSegmentState struct {
	text         strings.Builder
	emittedAudio bool
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
	if _, err := s.buffer.WriteString(text); err != nil {
		return err
	}
	for {
		tokens := tokenize.NewBasicSentenceTokenizer().Tokenize(s.buffer.String(), "")
		if len(tokens) <= 1 {
			return nil
		}
		sentence := tokens[0]
		if err := s.sendTextLocked(sentence); err != nil {
			s.closeActiveLocked()
			s.markClosedLocked()
			return err
		}
		current := s.buffer.String()
		tokenIdx := strings.Index(current, sentence)
		if tokenIdx < 0 {
			s.buffer.Reset()
			s.buffer.WriteString(strings.TrimSpace(strings.TrimPrefix(current, sentence)))
			continue
		}
		s.buffer.Reset()
		s.buffer.WriteString(strings.TrimLeftFunc(current[tokenIdx+len(sentence):], func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r'
		}))
	}
}

func (s *googleTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputEnded {
		return io.ErrClosedPipe
	}
	text := s.buffer.String()
	s.buffer.Reset()
	if text != "" {
		text = strings.Join(tokenize.NewBasicSentenceTokenizer().Tokenize(text, ""), " ")
		if err := s.sendTextLocked(text); err != nil {
			s.closeActiveLocked()
			s.markClosedLocked()
			return err
		}
	}
	if s.active == nil {
		return nil
	}
	if err := s.active.CloseSend(); err != nil {
		s.markClosedLocked()
		return googleTTSSynthesisError(err)
	}
	s.active = nil
	s.flushed++
	return nil
}

func (s *googleTTSSynthesizeStream) closeActiveLocked() {
	if s.active == nil {
		return
	}
	_ = s.active.CloseSend()
	s.active = nil
}

func (s *googleTTSSynthesizeStream) sendTextLocked(text string) error {
	if text == "" {
		return nil
	}
	stream, err := s.ensureActiveStreamLocked()
	if err != nil {
		return googleTTSSynthesisError(err)
	}
	if err := stream.Send(&texttospeechpb.StreamingSynthesizeRequest{
		StreamingRequest: &texttospeechpb.StreamingSynthesizeRequest_Input{
			Input: googleTTSStreamingInput(text, s.nextPromptLocked(), s.markup),
		},
	}); err != nil {
		return googleTTSSynthesisError(err)
	}
	if state := s.segments[stream]; state != nil {
		state.text.WriteString(text)
	}
	return nil
}

func googleTTSStreamingInput(text string, prompt *string, markup bool) *texttospeechpb.StreamingSynthesisInput {
	input := &texttospeechpb.StreamingSynthesisInput{Prompt: prompt}
	if markup {
		input.InputSource = &texttospeechpb.StreamingSynthesisInput_Markup{Markup: text}
	} else {
		input.InputSource = &texttospeechpb.StreamingSynthesisInput_Text{Text: text}
	}
	return input
}

func (s *googleTTSSynthesizeStream) nextPromptLocked() *string {
	if s.sentInput {
		return nil
	}
	s.sentInput = true
	return s.prompt
}

func (s *googleTTSSynthesizeStream) ensureActiveStreamLocked() (texttospeechpb.TextToSpeech_StreamingSynthesizeClient, error) {
	if s.active != nil {
		return s.active, nil
	}
	stream, err := s.client.StreamingSynthesize(s.ctx)
	if err != nil {
		return nil, err
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
				CustomPronunciations: s.custom,
			},
		},
	}); err != nil {
		_ = stream.CloseSend()
		return nil, err
	}
	s.active = stream
	s.sentInput = false
	s.streams = append(s.streams, stream)
	if s.segments == nil {
		s.segments = make(map[texttospeechpb.TextToSpeech_StreamingSynthesizeClient]*googleTTSSegmentState)
	}
	s.segments[stream] = &googleTTSSegmentState{}
	s.cond.Broadcast()
	return stream, nil
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
	s.active = nil
	s.cond.Broadcast()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.unregister()
	for _, stream := range streams {
		_ = stream.CloseSend()
	}
	return nil
}

func (s *googleTTSSynthesizeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *googleTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, io.EOF
		}
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
		if err != nil && s.isClosed() {
			return nil, io.EOF
		}
		if err == io.EOF {
			s.mu.Lock()
			if len(s.streams) > 0 && s.streams[0] == stream {
				s.streams = s.streams[1:]
			}
			segment := s.segments[stream]
			delete(s.segments, stream)
			if segment != nil && strings.TrimSpace(segment.text.String()) != "" && !segment.emittedAudio {
				s.markClosedLocked()
				s.mu.Unlock()
				return nil, llm.NewAPIError(fmt.Sprintf("no audio frames were pushed for text: %s", segment.text.String()), nil, true)
			}
			s.mu.Unlock()
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		if err != nil {
			_ = stream.CloseSend()
			s.mu.Lock()
			if len(s.streams) > 0 && s.streams[0] == stream {
				s.streams = s.streams[1:]
			}
			delete(s.segments, stream)
			s.markClosedLocked()
			s.mu.Unlock()
			return nil, googleTTSSynthesisError(err)
		}
		data := resp.GetAudioContent()
		if len(data) == 0 {
			continue
		}
		s.mu.Lock()
		if segment := s.segments[stream]; segment != nil {
			segment.emittedAudio = true
		}
		s.mu.Unlock()
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

func (s *googleTTSSynthesizeStream) markClosedLocked() {
	s.closed = true
	s.cond.Broadcast()
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
		SsmlGender:   cfg.gender,
	}
	if cfg.cloneKey != "" {
		voice.Name = ""
		voice.VoiceClone = &texttospeechpb.VoiceCloneParams{VoiceCloningKey: cfg.cloneKey}
	}
	if cfg.model != "chirp_3" {
		voice.ModelName = cfg.model
	}
	return voice
}

func googleTTSSSMLGender(gender string) texttospeechpb.SsmlVoiceGender {
	switch gender {
	case "male":
		return texttospeechpb.SsmlVoiceGender_MALE
	case "female":
		return texttospeechpb.SsmlVoiceGender_FEMALE
	default:
		return texttospeechpb.SsmlVoiceGender_NEUTRAL
	}
}
