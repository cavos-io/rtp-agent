package google

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	speechv2 "cloud.google.com/go/speech/apiv2"
	speechv2pb "cloud.google.com/go/speech/apiv2/speechpb"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const googleSTTMaxSessionDuration = 240 * time.Second

type GoogleSTT struct {
	mu                     sync.Mutex
	streams                map[*googleSTTStream]struct{}
	client                 googleSpeechClient
	clientV2               googleSpeechV2Client
	newClient              func(context.Context) (googleSpeechClient, error)
	newClientV2            func(context.Context) (googleSpeechV2Client, error)
	closed                 bool
	model                  string
	language               string
	streaming              bool
	detectLanguage         bool
	interimResults         bool
	punctuate              bool
	spokenPunctuation      bool
	profanityFilter        bool
	voiceActivityEvents    bool
	sampleRate             int32
	minConfidence          float64
	enableWordTimeOffset   bool
	enableWordConfidence   bool
	speechStartTimeout     time.Duration
	speechEndTimeout       time.Duration
	location               string
	project                string
	endpointingSensitivity string
	keywords               []GoogleSTTKeyword
	adaptation             *speechpb.SpeechAdaptation
	adaptationV2           *speechv2pb.SpeechAdaptation
	denoiserConfig         *speechv2pb.DenoiserConfig
	alternativeLanguages   []string
}

type googleSpeechClient interface {
	StreamingRecognize(ctx context.Context, opts ...gax.CallOption) (speechpb.Speech_StreamingRecognizeClient, error)
	Recognize(ctx context.Context, req *speechpb.RecognizeRequest, opts ...gax.CallOption) (*speechpb.RecognizeResponse, error)
}

type googleSpeechV2Client interface {
	StreamingRecognize(ctx context.Context, opts ...gax.CallOption) (speechv2pb.Speech_StreamingRecognizeClient, error)
	Recognize(ctx context.Context, req *speechv2pb.RecognizeRequest, opts ...gax.CallOption) (*speechv2pb.RecognizeResponse, error)
}

type GoogleSTTOption func(*GoogleSTT)

type GoogleSTTKeyword struct {
	Value string
	Boost float32
}

func WithGoogleSTTModel(model string) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.model = model
	}
}

func WithGoogleSTTLanguage(language string) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.language = language
	}
}

func WithGoogleSTTStreaming(enabled bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.streaming = enabled
	}
}

func WithGoogleSTTDetectLanguage(enabled bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.detectLanguage = enabled
	}
}

func WithGoogleSTTInterimResults(enabled bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.interimResults = enabled
	}
}

func WithGoogleSTTPunctuate(punctuate bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.punctuate = punctuate
	}
}

func WithGoogleSTTSpokenPunctuation(spokenPunctuation bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.spokenPunctuation = spokenPunctuation
	}
}

func WithGoogleSTTProfanityFilter(profanityFilter bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.profanityFilter = profanityFilter
	}
}

func WithGoogleSTTVoiceActivityEvents(enabled bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.voiceActivityEvents = enabled
	}
}

func WithGoogleSTTSpeechStartTimeout(timeout time.Duration) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.speechStartTimeout = timeout
	}
}

func WithGoogleSTTSpeechEndTimeout(timeout time.Duration) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.speechEndTimeout = timeout
	}
}

func WithGoogleSTTEndpointingSensitivity(sensitivity string) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.endpointingSensitivity = sensitivity
	}
}

func WithGoogleSTTProject(project string) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.project = project
	}
}

func WithGoogleSTTWordConfidence(enabled bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.enableWordConfidence = enabled
	}
}

func WithGoogleSTTWordTimeOffsets(enabled bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.enableWordTimeOffset = enabled
	}
}

func WithGoogleSTTSampleRate(sampleRate int32) GoogleSTTOption {
	return func(s *GoogleSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithGoogleSTTMinConfidenceThreshold(threshold float64) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.minConfidence = threshold
	}
}

func WithGoogleSTTKeywords(keywords ...GoogleSTTKeyword) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.keywords = nil
		for _, keyword := range keywords {
			if keyword.Value == "" {
				continue
			}
			s.keywords = append(s.keywords, keyword)
		}
	}
}

func WithGoogleSTTAdaptation(adaptation *speechpb.SpeechAdaptation) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.adaptation = adaptation
		s.adaptationV2 = nil
	}
}

func WithGoogleSTTAdaptationV2(adaptation *speechv2pb.SpeechAdaptation) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.adaptation = nil
		s.adaptationV2 = adaptation
	}
}

func WithGoogleSTTDenoiserConfig(config *speechv2pb.DenoiserConfig) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.denoiserConfig = config
	}
}

func WithGoogleSTTAlternativeLanguages(languages ...string) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.alternativeLanguages = nil
		for _, language := range languages {
			if language == "" {
				continue
			}
			s.alternativeLanguages = append(s.alternativeLanguages, language)
		}
	}
}

func WithGoogleSTTLocation(location string) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.location = location
	}
}

// NewGoogleSTT creates a new STT client using Application Default Credentials,
// or by providing a path to a credentials JSON file.
func NewGoogleSTT(credentialsFile string, providerOpts ...GoogleSTTOption) (*GoogleSTT, error) {
	ctx := context.Background()
	provider := newGoogleSTTWithClient(nil, providerOpts...)
	if err := googleSTTValidateAdaptation(provider); err != nil {
		return nil, err
	}
	if provider.project == "" {
		project, err := googleProjectFromCredentialsFile(credentialsFile)
		if err != nil {
			return nil, err
		}
		provider.project = project
	}
	provider.newClient = func(ctx context.Context) (googleSpeechClient, error) {
		clientOpts, err := googleSTTClientOptions(credentialsFile, provider)
		if err != nil {
			return nil, err
		}
		return speech.NewClient(ctx, clientOpts...)
	}
	provider.newClientV2 = func(ctx context.Context) (googleSpeechV2Client, error) {
		clientOpts, err := googleSTTClientOptions(credentialsFile, provider)
		if err != nil {
			return nil, err
		}
		return speechv2.NewClient(ctx, clientOpts...)
	}

	client, err := provider.newClient(ctx)
	if err != nil {
		return nil, err
	}
	provider.client = client
	if googleSTTUsesV2(provider.model) {
		clientV2, err := provider.ensureClientV2(ctx)
		if err != nil {
			if closer, ok := client.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
			return nil, err
		}
		provider.clientV2 = clientV2
	}

	return provider, nil
}

func googleSTTClientOptions(credentialsFile string, provider *GoogleSTT) ([]option.ClientOption, error) {
	clientOpts, err := googleClientOptionsFromCredentialsFile(credentialsFile)
	if err != nil {
		return nil, err
	}
	if endpoint := googleSTTEndpoint(provider); endpoint != "" {
		clientOpts = append(clientOpts, option.WithEndpoint(endpoint))
	}
	return clientOpts, nil
}

func newGoogleSTTWithClient(client googleSpeechClient, opts ...GoogleSTTOption) *GoogleSTT {
	provider := &GoogleSTT{
		streams:              make(map[*googleSTTStream]struct{}),
		client:               client,
		model:                "latest_long",
		language:             "en-US",
		streaming:            true,
		detectLanguage:       true,
		interimResults:       true,
		punctuate:            true,
		sampleRate:           16000,
		minConfidence:        0.65,
		enableWordTimeOffset: true,
		location:             "global",
	}
	for _, opt := range opts {
		opt(provider)
	}
	googleSTTSanitizeEndpointing(provider)
	return provider
}

func googleSTTValidateAdaptation(s *GoogleSTT) error {
	if googleSTTUsesV2(s.model) {
		if s.adaptation != nil {
			return errors.New("adaptation must be cloud_speech_v2.SpeechAdaptation for v2 models")
		}
		return nil
	}
	if s.adaptationV2 != nil {
		return errors.New("adaptation must be resource_v1.SpeechAdaptation for v1 models")
	}
	return nil
}

func newGoogleSTTWithV2Client(client googleSpeechV2Client, opts ...GoogleSTTOption) *GoogleSTT {
	provider := newGoogleSTTWithClient(nil, opts...)
	provider.clientV2 = client
	return provider
}

func googleSTTEndpoint(s *GoogleSTT) string {
	if s.location == "" || s.location == "global" {
		return ""
	}
	return s.location + "-speech.googleapis.com"
}

func (s *GoogleSTT) Label() string           { return "google.STT" }
func (s *GoogleSTT) InputSampleRate() uint32 { return uint32(s.sampleRate) }
func (s *GoogleSTT) Capabilities() stt.STTCapabilities {
	alignedTranscript := ""
	if s.streaming && googleEnableWordTimeOffsets(s) {
		alignedTranscript = "word"
	}
	return stt.STTCapabilities{Streaming: s.streaming, InterimResults: s.streaming && s.interimResults, Diarization: false, AlignedTranscript: alignedTranscript, OfflineRecognize: true}
}

func (s *GoogleSTT) UpdateOptions(opts ...GoogleSTTOption) error {
	if len(opts) == 0 {
		return nil
	}
	s.mu.Lock()
	previous := googleSTTCaptureConfig(s)
	oldLanguage := s.language
	oldLocation := s.location
	oldAlternativeLanguages := append([]string(nil), s.alternativeLanguages...)
	for _, opt := range opts {
		opt(s)
	}
	googleSTTSanitizeEndpointing(s)
	if err := googleSTTValidateAdaptation(s); err != nil {
		previous.restore(s)
		s.mu.Unlock()
		return err
	}
	minConfidence := s.minConfidence
	language := s.language
	languageChanged := oldLanguage != language
	if oldLocation != s.location {
		if s.newClient != nil {
			s.client = nil
		}
		if s.newClientV2 != nil {
			s.clientV2 = nil
		}
	}
	if languageChanged && googleStringSlicesEqual(oldAlternativeLanguages, s.alternativeLanguages) {
		s.alternativeLanguages = nil
	}
	streams := make([]*googleSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()

	for _, stream := range streams {
		stream.updateConfig(minConfidence, language, languageChanged)
		if err := stream.reconnectForUpdatedConfig(); err != nil {
			stream.failWithError(err)
		}
	}
	return nil
}

type googleSTTConfigState struct {
	model                string
	language             string
	streaming            bool
	detectLanguage       bool
	interimResults       bool
	punctuate            bool
	spokenPunctuation    bool
	profanityFilter      bool
	voiceActivityEvents  bool
	sampleRate           int32
	minConfidence        float64
	wordTimeOffset       bool
	wordConfidence       bool
	speechStartTimeout   time.Duration
	speechEndTimeout     time.Duration
	location             string
	project              string
	endpointing          string
	keywords             []GoogleSTTKeyword
	adaptation           *speechpb.SpeechAdaptation
	adaptationV2         *speechv2pb.SpeechAdaptation
	denoiserConfig       *speechv2pb.DenoiserConfig
	alternativeLanguages []string
}

func googleSTTCaptureConfig(s *GoogleSTT) googleSTTConfigState {
	return googleSTTConfigState{
		model:                s.model,
		language:             s.language,
		streaming:            s.streaming,
		detectLanguage:       s.detectLanguage,
		interimResults:       s.interimResults,
		punctuate:            s.punctuate,
		spokenPunctuation:    s.spokenPunctuation,
		profanityFilter:      s.profanityFilter,
		voiceActivityEvents:  s.voiceActivityEvents,
		sampleRate:           s.sampleRate,
		minConfidence:        s.minConfidence,
		wordTimeOffset:       s.enableWordTimeOffset,
		wordConfidence:       s.enableWordConfidence,
		speechStartTimeout:   s.speechStartTimeout,
		speechEndTimeout:     s.speechEndTimeout,
		location:             s.location,
		project:              s.project,
		endpointing:          s.endpointingSensitivity,
		keywords:             append([]GoogleSTTKeyword(nil), s.keywords...),
		adaptation:           s.adaptation,
		adaptationV2:         s.adaptationV2,
		denoiserConfig:       s.denoiserConfig,
		alternativeLanguages: append([]string(nil), s.alternativeLanguages...),
	}
}

func (c googleSTTConfigState) restore(s *GoogleSTT) {
	s.model = c.model
	s.language = c.language
	s.streaming = c.streaming
	s.detectLanguage = c.detectLanguage
	s.interimResults = c.interimResults
	s.punctuate = c.punctuate
	s.spokenPunctuation = c.spokenPunctuation
	s.profanityFilter = c.profanityFilter
	s.voiceActivityEvents = c.voiceActivityEvents
	s.sampleRate = c.sampleRate
	s.minConfidence = c.minConfidence
	s.enableWordTimeOffset = c.wordTimeOffset
	s.enableWordConfidence = c.wordConfidence
	s.speechStartTimeout = c.speechStartTimeout
	s.speechEndTimeout = c.speechEndTimeout
	s.location = c.location
	s.project = c.project
	s.endpointingSensitivity = c.endpointing
	s.keywords = append([]GoogleSTTKeyword(nil), c.keywords...)
	s.adaptation = c.adaptation
	s.adaptationV2 = c.adaptationV2
	s.denoiserConfig = c.denoiserConfig
	s.alternativeLanguages = append([]string(nil), c.alternativeLanguages...)
}

func (s *GoogleSTT) Close() error {
	s.mu.Lock()
	s.closed = true
	streams := make([]*googleSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = make(map[*googleSTTStream]struct{})
	s.mu.Unlock()

	for _, stream := range streams {
		_ = stream.Close()
	}
	return nil
}

func (s *GoogleSTT) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *GoogleSTT) usesV2() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return googleSTTUsesV2(s.model)
}

func (s *GoogleSTT) registerStream(stream *googleSTTStream) bool {
	if s == nil || stream == nil {
		return false
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = stream.Close()
		return false
	}
	if s.streams == nil {
		s.streams = make(map[*googleSTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
	s.mu.Unlock()
	return true
}

func (s *GoogleSTT) unregisterStream(stream *googleSTTStream) {
	s.mu.Lock()
	delete(s.streams, stream)
	s.mu.Unlock()
}

func (s *GoogleSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	explicitLanguage := language != ""
	if language == "" {
		language = s.language
	}

	gs := &googleSTTStream{
		owner:                       s,
		ctx:                         ctx,
		sessionConnectedAt:          time.Now(),
		language:                    language,
		includeAlternativeLanguages: !explicitLanguage,
		minConfidence:               s.minConfidence,
		events:                      make(chan *stt.SpeechEvent, 10),
		errCh:                       make(chan error, 1),
	}
	if googleSTTUsesV2(s.model) {
		stream, err := s.newStreamingRecognizeStreamV2(ctx, language, !explicitLanguage)
		if err != nil {
			return nil, err
		}
		gs.streamV2 = stream
	} else {
		stream, err := s.newStreamingRecognizeStream(ctx, language, !explicitLanguage)
		if err != nil {
			return nil, err
		}
		gs.stream = stream
	}
	if s.isClosed() {
		gs.closeSend()
		return nil, io.ErrClosedPipe
	}

	if !s.registerStream(gs) {
		return nil, io.ErrClosedPipe
	}
	go gs.readLoop()

	return gs, nil
}

func (s *GoogleSTT) newStreamingRecognizeStream(ctx context.Context, language string, includeAlternativeLanguages bool) (speechpb.Speech_StreamingRecognizeClient, error) {
	client, err := s.ensureClient(ctx)
	if err != nil {
		return nil, googleSTTStreamError(err)
	}
	stream, err := client.StreamingRecognize(ctx)
	if err != nil {
		return nil, googleSTTStreamError(err)
	}
	err = stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				Config:                    googleRecognitionConfigWithAlternatives(s, language, includeAlternativeLanguages),
				InterimResults:            s.interimResults,
				EnableVoiceActivityEvents: s.voiceActivityEvents,
			},
		},
	})
	if err != nil {
		_ = stream.CloseSend()
		return nil, googleSTTStreamError(err)
	}
	return stream, nil
}

func (s *GoogleSTT) ensureClient(ctx context.Context) (googleSpeechClient, error) {
	s.mu.Lock()
	client := s.client
	newClient := s.newClient
	s.mu.Unlock()
	if client != nil {
		return client, nil
	}
	if newClient == nil {
		return nil, errors.New("google STT v1 client is not configured")
	}
	client, err := newClient(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.client == nil {
		s.client = client
	}
	client = s.client
	s.mu.Unlock()
	return client, nil
}

func (s *GoogleSTT) newStreamingRecognizeStreamV2(ctx context.Context, language string, includeAlternativeLanguages bool) (speechv2pb.Speech_StreamingRecognizeClient, error) {
	clientV2, err := s.ensureClientV2(ctx)
	if err != nil {
		return nil, googleSTTStreamError(err)
	}
	recognizer := googleSTTRecognizer(s)
	if recognizer == "" {
		return nil, googleSTTStreamError(errors.New("google STT v2 project is required via WithGoogleSTTProject"))
	}
	stream, err := clientV2.StreamingRecognize(ctx)
	if err != nil {
		return nil, googleSTTStreamError(err)
	}
	err = stream.Send(&speechv2pb.StreamingRecognizeRequest{
		Recognizer: recognizer,
		StreamingRequest: &speechv2pb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: googleStreamingRecognitionConfigV2(s, language, includeAlternativeLanguages),
		},
	})
	if err != nil {
		_ = stream.CloseSend()
		return nil, googleSTTStreamError(err)
	}
	return stream, nil
}

func (s *GoogleSTT) ensureClientV2(ctx context.Context) (googleSpeechV2Client, error) {
	s.mu.Lock()
	clientV2 := s.clientV2
	newClientV2 := s.newClientV2
	s.mu.Unlock()
	if clientV2 != nil {
		return clientV2, nil
	}
	if newClientV2 == nil {
		return nil, errors.New("google STT v2 client is not configured")
	}
	clientV2, err := newClientV2(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.clientV2 == nil {
		s.clientV2 = clientV2
	}
	clientV2 = s.clientV2
	s.mu.Unlock()
	return clientV2, nil
}

func (s *GoogleSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	explicitLanguage := language != ""
	if language == "" {
		language = s.language
	}

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	if googleSTTUsesV2(s.model) {
		clientV2, err := s.ensureClientV2(ctx)
		if err != nil {
			return nil, googleSTTStreamError(err)
		}
		recognizer := googleSTTRecognizer(s)
		if recognizer == "" {
			return nil, googleSTTStreamError(errors.New("google STT v2 project is required via WithGoogleSTTProject"))
		}
		resp, err := clientV2.Recognize(ctx, &speechv2pb.RecognizeRequest{
			Recognizer: recognizer,
			Config:     googleRecognitionConfigV2ForFrames(s, language, !explicitLanguage, frames),
			AudioSource: &speechv2pb.RecognizeRequest_Content{
				Content: buf.Bytes(),
			},
		})
		if err != nil {
			return nil, googleSTTStreamError(err)
		}
		return &stt.SpeechEvent{
			Type:         stt.SpeechEventFinalTranscript,
			Alternatives: googleSpeechDataFromRecognizeResultsV2(resp.GetResults()),
		}, nil
	}

	client, err := s.ensureClient(ctx)
	if err != nil {
		return nil, googleSTTStreamError(err)
	}
	resp, err := client.Recognize(ctx, &speechpb.RecognizeRequest{
		Config: googleRecognitionConfigForFrames(s, language, !explicitLanguage, frames),
		Audio: &speechpb.RecognitionAudio{
			AudioSource: &speechpb.RecognitionAudio_Content{
				Content: buf.Bytes(),
			},
		},
	})

	if err != nil {
		return nil, googleSTTStreamError(err)
	}

	return &stt.SpeechEvent{
		Type:         stt.SpeechEventFinalTranscript,
		Alternatives: googleSpeechDataFromRecognizeResults(resp.Results),
	}, nil
}

func googleRecognitionConfig(s *GoogleSTT, language string) *speechpb.RecognitionConfig {
	return googleRecognitionConfigWithAlternatives(s, language, true)
}

func googleRecognitionConfigWithAlternatives(s *GoogleSTT, language string, includeAlternativeLanguages bool) *speechpb.RecognitionConfig {
	return &speechpb.RecognitionConfig{
		Encoding:                   speechpb.RecognitionConfig_LINEAR16,
		SampleRateHertz:            s.sampleRate,
		AudioChannelCount:          1,
		LanguageCode:               language,
		AlternativeLanguageCodes:   googleAlternativeLanguageCodes(s, includeAlternativeLanguages),
		EnableWordTimeOffsets:      googleEnableWordTimeOffsets(s),
		EnableWordConfidence:       s.enableWordConfidence,
		EnableAutomaticPunctuation: s.punctuate,
		EnableSpokenPunctuation:    wrapperspb.Bool(s.spokenPunctuation),
		ProfanityFilter:            s.profanityFilter,
		Model:                      s.model,
		Adaptation:                 googleSpeechAdaptation(s),
	}
}

func googleRecognitionConfigForFrames(s *GoogleSTT, language string, includeAlternativeLanguages bool, frames []*model.AudioFrame) *speechpb.RecognitionConfig {
	config := googleRecognitionConfigWithAlternatives(s, language, includeAlternativeLanguages)
	haveSampleRate := false
	haveChannels := false
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate > 0 && !haveSampleRate {
			config.SampleRateHertz = int32(frame.SampleRate)
			haveSampleRate = true
		}
		if frame.NumChannels > 0 && !haveChannels {
			config.AudioChannelCount = int32(frame.NumChannels)
			haveChannels = true
		}
		if haveSampleRate && haveChannels {
			break
		}
	}
	return config
}

func googleRecognitionConfigV2ForFrames(s *GoogleSTT, language string, includeAlternativeLanguages bool, frames []*model.AudioFrame) *speechv2pb.RecognitionConfig {
	config := &speechv2pb.RecognitionConfig{
		DecodingConfig: &speechv2pb.RecognitionConfig_ExplicitDecodingConfig{
			ExplicitDecodingConfig: &speechv2pb.ExplicitDecodingConfig{
				Encoding:          speechv2pb.ExplicitDecodingConfig_LINEAR16,
				SampleRateHertz:   s.sampleRate,
				AudioChannelCount: 1,
			},
		},
		LanguageCodes:  googleLanguageCodesV2(s, language, includeAlternativeLanguages),
		Model:          s.model,
		Adaptation:     googleSpeechAdaptationV2(s),
		DenoiserConfig: s.denoiserConfig,
		Features: &speechv2pb.RecognitionFeatures{
			EnableAutomaticPunctuation: s.punctuate,
			EnableWordTimeOffsets:      googleEnableWordTimeOffsets(s),
			EnableSpokenPunctuation:    s.spokenPunctuation,
			EnableWordConfidence:       s.enableWordConfidence,
			ProfanityFilter:            s.profanityFilter,
		},
	}
	decoding := config.GetExplicitDecodingConfig()
	haveSampleRate := false
	haveChannels := false
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate > 0 && !haveSampleRate {
			decoding.SampleRateHertz = int32(frame.SampleRate)
			haveSampleRate = true
		}
		if frame.NumChannels > 0 && !haveChannels {
			decoding.AudioChannelCount = int32(frame.NumChannels)
			haveChannels = true
		}
		if haveSampleRate && haveChannels {
			break
		}
	}
	return config
}

func googleStreamingRecognitionConfigV2(s *GoogleSTT, language string, includeAlternativeLanguages bool) *speechv2pb.StreamingRecognitionConfig {
	return &speechv2pb.StreamingRecognitionConfig{
		Config: &speechv2pb.RecognitionConfig{
			DecodingConfig: &speechv2pb.RecognitionConfig_ExplicitDecodingConfig{
				ExplicitDecodingConfig: &speechv2pb.ExplicitDecodingConfig{
					Encoding:          speechv2pb.ExplicitDecodingConfig_LINEAR16,
					SampleRateHertz:   s.sampleRate,
					AudioChannelCount: 1,
				},
			},
			LanguageCodes:  googleLanguageCodesV2(s, language, includeAlternativeLanguages),
			Model:          s.model,
			Adaptation:     googleSpeechAdaptationV2(s),
			DenoiserConfig: s.denoiserConfig,
			Features: &speechv2pb.RecognitionFeatures{
				EnableAutomaticPunctuation: s.punctuate,
				EnableWordTimeOffsets:      googleEnableWordTimeOffsets(s),
				EnableSpokenPunctuation:    s.spokenPunctuation,
				EnableWordConfidence:       s.enableWordConfidence,
				ProfanityFilter:            s.profanityFilter,
			},
		},
		StreamingFeatures: googleStreamingRecognitionFeaturesV2(s),
	}
}

func googleStreamingRecognitionFeaturesV2(s *GoogleSTT) *speechv2pb.StreamingRecognitionFeatures {
	timeout := googleVoiceActivityTimeoutV2(s)
	return &speechv2pb.StreamingRecognitionFeatures{
		InterimResults:            s.interimResults,
		EnableVoiceActivityEvents: s.voiceActivityEvents || timeout != nil,
		VoiceActivityTimeout:      timeout,
		EndpointingSensitivity:    googleEndpointingSensitivityV2(s.model, s.endpointingSensitivity),
	}
}

func googleLanguageCodesV2(s *GoogleSTT, language string, includeAlternativeLanguages bool) []string {
	languages := []string{language}
	languages = append(languages, googleAlternativeLanguageCodes(s, includeAlternativeLanguages)...)
	return languages
}

func googleVoiceActivityTimeoutV2(s *GoogleSTT) *speechv2pb.StreamingRecognitionFeatures_VoiceActivityTimeout {
	if s.speechStartTimeout <= 0 && s.speechEndTimeout <= 0 {
		return nil
	}
	timeout := &speechv2pb.StreamingRecognitionFeatures_VoiceActivityTimeout{}
	if s.speechStartTimeout > 0 {
		timeout.SpeechStartTimeout = durationpb.New(s.speechStartTimeout)
	}
	if s.speechEndTimeout > 0 {
		timeout.SpeechEndTimeout = durationpb.New(s.speechEndTimeout)
	}
	return timeout
}

func googleEndpointingSensitivityV2(model string, value string) speechv2pb.StreamingRecognitionFeatures_EndpointingSensitivity {
	if model != "chirp_3" {
		return speechv2pb.StreamingRecognitionFeatures_ENDPOINTING_SENSITIVITY_UNSPECIFIED
	}
	if enum, ok := speechv2pb.StreamingRecognitionFeatures_EndpointingSensitivity_value[value]; ok {
		return speechv2pb.StreamingRecognitionFeatures_EndpointingSensitivity(enum)
	}
	return speechv2pb.StreamingRecognitionFeatures_ENDPOINTING_SENSITIVITY_UNSPECIFIED
}

func googleSTTRecognizer(s *GoogleSTT) string {
	if s == nil || s.project == "" {
		return ""
	}
	location := s.location
	if location == "" {
		location = "global"
	}
	return "projects/" + s.project + "/locations/" + location + "/recognizers/_"
}

func googleAlternativeLanguageCodes(s *GoogleSTT, include bool) []string {
	if s == nil || !include || !s.detectLanguage {
		return nil
	}
	return append([]string(nil), s.alternativeLanguages...)
}

func googleStringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func googleSpeechAdaptation(s *GoogleSTT) *speechpb.SpeechAdaptation {
	if s == nil {
		return nil
	}
	if s.adaptation != nil {
		return s.adaptation
	}
	if len(s.keywords) == 0 {
		return nil
	}
	phrases := make([]*speechpb.PhraseSet_Phrase, 0, len(s.keywords))
	for _, keyword := range s.keywords {
		if keyword.Value == "" {
			continue
		}
		phrases = append(phrases, &speechpb.PhraseSet_Phrase{
			Value: keyword.Value,
			Boost: keyword.Boost,
		})
	}
	if len(phrases) == 0 {
		return nil
	}
	return &speechpb.SpeechAdaptation{
		PhraseSets: []*speechpb.PhraseSet{{
			Name:    "keywords",
			Phrases: phrases,
		}},
	}
}

func googleSpeechAdaptationV2(s *GoogleSTT) *speechv2pb.SpeechAdaptation {
	if s == nil {
		return nil
	}
	if s.adaptationV2 != nil {
		return s.adaptationV2
	}
	if len(s.keywords) == 0 {
		return nil
	}
	phrases := make([]*speechv2pb.PhraseSet_Phrase, 0, len(s.keywords))
	for _, keyword := range s.keywords {
		if keyword.Value == "" {
			continue
		}
		phrases = append(phrases, &speechv2pb.PhraseSet_Phrase{
			Value: keyword.Value,
			Boost: keyword.Boost,
		})
	}
	if len(phrases) == 0 {
		return nil
	}
	return &speechv2pb.SpeechAdaptation{
		PhraseSets: []*speechv2pb.SpeechAdaptation_AdaptationPhraseSet{{
			Value: &speechv2pb.SpeechAdaptation_AdaptationPhraseSet_InlinePhraseSet{
				InlinePhraseSet: &speechv2pb.PhraseSet{
					DisplayName: "keywords",
					Phrases:     phrases,
				},
			},
		}},
	}
}

func googleEnableWordTimeOffsets(s *GoogleSTT) bool {
	if s.model == "chirp_3" {
		return false
	}
	return s.enableWordTimeOffset
}

func googleSTTUsesV2(model string) bool {
	switch model {
	case "telephony", "chirp_2", "chirp_3":
		return true
	default:
		return false
	}
}

func googleSTTSanitizeEndpointing(s *GoogleSTT) {
	if s == nil || s.model == "chirp_3" {
		return
	}
	s.endpointingSensitivity = ""
}

func googleSpeechDataFromAlternative(alt *speechpb.SpeechRecognitionAlternative) stt.SpeechData {
	return googleSpeechDataFromAlternativeOffset(alt, 0)
}

func googleSpeechDataFromAlternativeOffset(alt *speechpb.SpeechRecognitionAlternative, startTimeOffset float64) stt.SpeechData {
	if alt == nil {
		return stt.SpeechData{}
	}

	data := stt.SpeechData{
		Text:       alt.GetTranscript(),
		Confidence: float64(alt.GetConfidence()),
		Words:      googleTimedStringsOffset(alt.GetWords(), startTimeOffset),
	}
	googleApplySpeechDataTiming(&data, alt.GetWords(), startTimeOffset)
	return data
}

func googleSpeechDataFromRecognizeResults(results []*speechpb.SpeechRecognitionResult) []stt.SpeechData {
	if len(results) == 0 {
		return []stt.SpeechData{}
	}
	var text string
	var confidence float64
	var count int
	var firstWords []*speechpb.WordInfo
	var lastWords []*speechpb.WordInfo
	for _, result := range results {
		if len(result.GetAlternatives()) == 0 {
			continue
		}
		alt := result.GetAlternatives()[0]
		text += alt.GetTranscript()
		confidence += float64(alt.GetConfidence())
		if count == 0 {
			firstWords = alt.GetWords()
		}
		lastWords = alt.GetWords()
		count++
	}
	if count == 0 {
		return []stt.SpeechData{}
	}
	data := stt.SpeechData{
		Language:   googleRecognizeResultLanguage(results),
		Text:       text,
		Confidence: confidence / float64(count),
		Words:      googleTimedStrings(firstWords),
	}
	googleApplyRecognizeSpeechDataTiming(&data, firstWords, lastWords)
	return []stt.SpeechData{data}
}

func googleSpeechDataFromRecognizeResultsV2(results []*speechv2pb.SpeechRecognitionResult) []stt.SpeechData {
	if len(results) == 0 {
		return []stt.SpeechData{}
	}
	var text string
	var confidence float64
	var count int
	var firstWords []*speechv2pb.WordInfo
	var lastWords []*speechv2pb.WordInfo
	for _, result := range results {
		if len(result.GetAlternatives()) == 0 {
			continue
		}
		alt := result.GetAlternatives()[0]
		text += alt.GetTranscript()
		confidence += float64(alt.GetConfidence())
		if count == 0 {
			firstWords = alt.GetWords()
		}
		lastWords = alt.GetWords()
		count++
	}
	if count == 0 {
		return []stt.SpeechData{}
	}
	data := stt.SpeechData{
		Language:   googleRecognizeResultLanguageV2(results),
		Text:       text,
		Confidence: confidence / float64(count),
		Words:      googleTimedStringsOffsetV2(firstWords, 0),
	}
	googleApplyRecognizeSpeechDataTimingV2(&data, firstWords, lastWords)
	return []stt.SpeechData{data}
}

func googleRecognizeResultLanguage(results []*speechpb.SpeechRecognitionResult) string {
	if len(results) == 0 {
		return ""
	}
	return results[0].GetLanguageCode()
}

func googleRecognizeResultLanguageV2(results []*speechv2pb.SpeechRecognitionResult) string {
	if len(results) == 0 {
		return ""
	}
	return results[0].GetLanguageCode()
}

func googleApplyRecognizeSpeechDataTiming(data *stt.SpeechData, firstWords []*speechpb.WordInfo, lastWords []*speechpb.WordInfo) {
	if data == nil || len(firstWords) == 0 || len(lastWords) == 0 {
		return
	}
	data.StartTime = firstWords[0].GetStartTime().AsDuration().Seconds()
	data.EndTime = lastWords[len(lastWords)-1].GetEndTime().AsDuration().Seconds()
}

func googleApplyRecognizeSpeechDataTimingV2(data *stt.SpeechData, firstWords []*speechv2pb.WordInfo, lastWords []*speechv2pb.WordInfo) {
	if data == nil || len(firstWords) == 0 || len(lastWords) == 0 {
		return
	}
	data.StartTime = firstWords[0].GetStartOffset().AsDuration().Seconds()
	data.EndTime = lastWords[len(lastWords)-1].GetEndOffset().AsDuration().Seconds()
}

func googleTimedStrings(words []*speechpb.WordInfo) []stt.TimedString {
	return googleTimedStringsOffset(words, 0)
}

func googleTimedStringsOffset(words []*speechpb.WordInfo, startTimeOffset float64) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}

	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:            word.GetWord(),
			StartTime:       word.GetStartTime().AsDuration().Seconds() + startTimeOffset,
			EndTime:         word.GetEndTime().AsDuration().Seconds() + startTimeOffset,
			StartTimeOffset: startTimeOffset,
			Confidence:      float64(word.GetConfidence()),
			SpeakerID:       googleSpeakerID(word),
		})
	}
	return timed
}

func googleSpeakerID(word *speechpb.WordInfo) string {
	return word.GetSpeakerLabel()
}

type googleSTTStream struct {
	mu                          sync.Mutex
	owner                       *GoogleSTT
	ctx                         context.Context
	stream                      speechpb.Speech_StreamingRecognizeClient
	streamV2                    speechv2pb.Speech_StreamingRecognizeClient
	sessionConnectedAt          time.Time
	language                    string
	includeAlternativeLanguages bool
	minConfidence               float64
	events                      chan *stt.SpeechEvent
	errCh                       chan error
	pendingErr                  error
	closed                      bool
	inputClosed                 bool
	callerClosedInput           bool
	audioPushed                 bool
	pushedSampleRate            uint32
	inputAudio                  googleSTTInputAudioNormalizer
	timingMu                    sync.Mutex
	startTimeOffset             float64
	startTime                   float64
}

type googleSTTInputAudioNormalizer struct {
	sampleRate  uint32
	numChannels uint32
	targetRate  uint32
	remainder   uint64
	lastSample  []byte
}

func (n *googleSTTInputAudioNormalizer) normalize(frame *model.AudioFrame, targetRate uint32) (*model.AudioFrame, error) {
	if frame == nil || targetRate == 0 || frame.SampleRate == targetRate {
		n.reset()
		return frame, nil
	}
	if frame.SampleRate == 0 {
		return nil, errors.New("cannot resample audio with zero sample rate")
	}
	if frame.NumChannels == 0 {
		return nil, errors.New("cannot resample audio with zero channels")
	}
	if len(frame.Data)%2 != 0 {
		return nil, errors.New("cannot resample non-16-bit PCM audio")
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel == 0 {
		samplesPerChannel = uint32(len(frame.Data)) / frame.NumChannels / 2
	}
	expectedBytes := int(samplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, errors.New("audio frame data is shorter than declared sample count")
	}
	if n.sampleRate != frame.SampleRate || n.numChannels != frame.NumChannels || n.targetRate != targetRate {
		n.sampleRate = frame.SampleRate
		n.numChannels = frame.NumChannels
		n.targetRate = targetRate
		n.remainder = 0
		n.lastSample = nil
	}
	if samplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        targetRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: 0,
			ParticipantID:     frame.ParticipantID,
		}, nil
	}

	scaledSamples := uint64(samplesPerChannel)*uint64(targetRate) + n.remainder
	outSamples := uint32(scaledSamples / uint64(frame.SampleRate))
	n.remainder = scaledSamples % uint64(frame.SampleRate)
	out := make([]byte, int(outSamples*frame.NumChannels*2))
	channelCount := int(frame.NumChannels)
	for outIdx := uint32(0); outIdx < outSamples; outIdx++ {
		srcIdx := uint32(uint64(outIdx) * uint64(frame.SampleRate) / uint64(targetRate))
		if srcIdx >= samplesPerChannel {
			srcIdx = samplesPerChannel - 1
		}
		for ch := 0; ch < channelCount; ch++ {
			inOffset := (int(srcIdx)*channelCount + ch) * 2
			outOffset := (int(outIdx)*channelCount + ch) * 2
			copy(out[outOffset:outOffset+2], frame.Data[inOffset:inOffset+2])
		}
	}
	if n.remainder > 0 {
		offset := int((samplesPerChannel - 1) * frame.NumChannels * 2)
		n.lastSample = append(n.lastSample[:0], frame.Data[offset:offset+int(frame.NumChannels*2)]...)
	} else {
		n.lastSample = nil
	}

	return &model.AudioFrame{
		Data:              out,
		SampleRate:        targetRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: outSamples,
		ParticipantID:     frame.ParticipantID,
	}, nil
}

func (n *googleSTTInputAudioNormalizer) flush() *model.AudioFrame {
	if n == nil || n.remainder == 0 || len(n.lastSample) == 0 || n.targetRate == 0 || n.numChannels == 0 {
		return nil
	}
	frame := &model.AudioFrame{
		Data:              append([]byte(nil), n.lastSample...),
		SampleRate:        n.targetRate,
		NumChannels:       n.numChannels,
		SamplesPerChannel: 1,
	}
	n.remainder = 0
	n.lastSample = nil
	return frame
}

func (n *googleSTTInputAudioNormalizer) reset() {
	n.sampleRate = 0
	n.numChannels = 0
	n.targetRate = 0
	n.remainder = 0
	n.lastSample = nil
}

func (s *googleSTTStream) readLoop() {
	if s.usesV2() {
		s.readLoopV2()
		return
	}
	defer close(s.events)
	var lastUsageEventTime float64
	var usageStream speechpb.Speech_StreamingRecognizeClient
	var speechStarted bool
	for {
		stream := s.currentStream()
		if stream != usageStream {
			lastUsageEventTime = 0
			usageStream = stream
		}
		resp, err := stream.Recv()
		if err != nil {
			if s.currentStream() != stream {
				continue
			}
			if s.shouldRestartAfterConflict(err) {
				restarted, restartErr := s.restartStreamWithError(stream)
				if restarted {
					lastUsageEventTime = 0
					continue
				}
				if restartErr != nil {
					err = restartErr
				}
			}
			if s.isClosed() {
				s.terminate()
				return
			}
			if err != io.EOF {
				s.errCh <- googleSTTStreamError(err)
			}
			s.terminate()
			return
		}
		if s.isClosed() {
			s.terminate()
			return
		}

		switch resp.GetSpeechEventType() {
		case speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_BEGIN:
			s.events <- &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}
			speechStarted = true
		case speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_END:
			s.events <- &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
			speechStarted = false
		}

		if resp.GetSpeechEventType() == speechpb.StreamingRecognizeResponse_SPEECH_EVENT_UNSPECIFIED {
			if len(resp.Results) > 0 {
				data, eventType, ok := googleSpeechDataFromStreamingResultsOffset(resp.Results, s.currentMinConfidence(), s.currentStartTimeOffset())
				if !ok {
					continue
				}
				s.events <- &stt.SpeechEvent{
					Type:         eventType,
					Alternatives: []stt.SpeechData{data},
				}
				if eventType == stt.SpeechEventFinalTranscript && s.shouldRestartAfterMaxSession(stream) {
					if speechStarted {
						s.events <- &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
						speechStarted = false
					}
					restarted, restartErr := s.restartStreamWithError(stream)
					if restarted {
						lastUsageEventTime = 0
						continue
					}
					if restartErr != nil {
						s.errCh <- googleSTTStreamError(restartErr)
						s.terminate()
						return
					}
				}
			}
		}

		if audioDuration := googleStreamingAudioDuration(resp, lastUsageEventTime); audioDuration > 0 {
			s.events <- &stt.SpeechEvent{
				Type:      stt.SpeechEventRecognitionUsage,
				RequestID: strconv.FormatInt(resp.GetRequestId(), 10),
				RecognitionUsage: &stt.RecognitionUsage{
					AudioDuration: audioDuration,
				},
			}
			lastUsageEventTime += audioDuration
		}
	}
}

func (s *googleSTTStream) readLoopV2() {
	defer close(s.events)
	var speechStarted bool
	var lastUsageEventTime float64
	var usageStream speechv2pb.Speech_StreamingRecognizeClient
	for {
		stream := s.currentStreamV2()
		if stream != usageStream {
			lastUsageEventTime = 0
			usageStream = stream
		}
		resp, err := stream.Recv()
		if err != nil {
			if s.currentStreamV2() != stream {
				continue
			}
			if s.shouldRestartAfterConflict(err) {
				restarted, restartErr := s.restartStreamV2WithError(stream)
				if restarted {
					lastUsageEventTime = 0
					continue
				}
				if restartErr != nil {
					err = restartErr
				}
			}
			if s.isClosed() {
				s.terminate()
				return
			}
			if err != io.EOF {
				s.errCh <- googleSTTStreamError(err)
			}
			s.terminate()
			return
		}
		if s.isClosed() {
			s.terminate()
			return
		}

		switch resp.GetSpeechEventType() {
		case speechv2pb.StreamingRecognizeResponse_SPEECH_ACTIVITY_BEGIN:
			s.events <- &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}
			speechStarted = true
		case speechv2pb.StreamingRecognizeResponse_SPEECH_ACTIVITY_END:
			s.events <- &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
			speechStarted = false
		}

		if resp.GetSpeechEventType() == speechv2pb.StreamingRecognizeResponse_SPEECH_EVENT_TYPE_UNSPECIFIED {
			if len(resp.Results) > 0 {
				data, eventType, ok := googleSpeechDataFromStreamingResultsV2(resp.Results, s.currentMinConfidence(), s.currentStartTimeOffset())
				if !ok {
					continue
				}
				s.events <- &stt.SpeechEvent{
					Type:         eventType,
					Alternatives: []stt.SpeechData{data},
				}
				if eventType == stt.SpeechEventFinalTranscript && s.shouldRestartAfterMaxSessionV2(stream) {
					if speechStarted {
						s.events <- &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
						speechStarted = false
					}
					restarted, restartErr := s.restartStreamV2WithError(stream)
					if restarted {
						lastUsageEventTime = 0
						continue
					}
					if restartErr != nil {
						s.errCh <- googleSTTStreamError(restartErr)
						s.terminate()
						return
					}
				}
			}
		}

		if audioDuration := googleStreamingAudioDurationV2(resp, lastUsageEventTime); audioDuration > 0 {
			s.events <- &stt.SpeechEvent{
				Type:      stt.SpeechEventRecognitionUsage,
				RequestID: googleStreamingRequestIDV2(resp),
				RecognitionUsage: &stt.RecognitionUsage{
					AudioDuration: audioDuration,
				},
			}
			lastUsageEventTime += audioDuration
		}
	}
}

func (s *googleSTTStream) currentStream() speechpb.Speech_StreamingRecognizeClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream
}

func (s *googleSTTStream) currentStreamV2() speechv2pb.Speech_StreamingRecognizeClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamV2
}

func (s *googleSTTStream) usesV2() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamV2 != nil
}

func (s *googleSTTStream) shouldRestartAfterConflict(err error) bool {
	if status.Code(err) != codes.AlreadyExists {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && !s.inputClosed
}

func (s *googleSTTStream) shouldRestartAfterMaxSession(stream speechpb.Speech_StreamingRecognizeClient) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && !s.inputClosed && s.stream == stream && !s.sessionConnectedAt.IsZero() && time.Since(s.sessionConnectedAt) > googleSTTMaxSessionDuration
}

func (s *googleSTTStream) shouldRestartAfterMaxSessionV2(stream speechv2pb.Speech_StreamingRecognizeClient) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && !s.inputClosed && s.streamV2 == stream && !s.sessionConnectedAt.IsZero() && time.Since(s.sessionConnectedAt) > googleSTTMaxSessionDuration
}

func (s *googleSTTStream) restartStreamWithError(old speechpb.Speech_StreamingRecognizeClient) (bool, error) {
	_ = old.CloseSend()
	stream, err := s.owner.newStreamingRecognizeStream(s.ctx, s.language, s.includeAlternativeLanguages)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.closed || s.inputClosed || s.stream != old {
		s.mu.Unlock()
		_ = stream.CloseSend()
		return false, nil
	}
	s.stream = stream
	s.sessionConnectedAt = time.Now()
	s.audioPushed = false
	s.mu.Unlock()
	return true, nil
}

func (s *googleSTTStream) restartFromV2ToV1WithError(old speechv2pb.Speech_StreamingRecognizeClient) (bool, error) {
	_ = old.CloseSend()
	stream, err := s.owner.newStreamingRecognizeStream(s.ctx, s.language, s.includeAlternativeLanguages)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.closed || s.inputClosed || s.streamV2 != old {
		s.mu.Unlock()
		_ = stream.CloseSend()
		return false, nil
	}
	s.stream = stream
	s.streamV2 = nil
	s.sessionConnectedAt = time.Now()
	s.audioPushed = false
	s.mu.Unlock()
	return true, nil
}

func (s *googleSTTStream) restartStreamV2WithError(old speechv2pb.Speech_StreamingRecognizeClient) (bool, error) {
	_ = old.CloseSend()
	stream, err := s.owner.newStreamingRecognizeStreamV2(s.ctx, s.language, s.includeAlternativeLanguages)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.closed || s.inputClosed || s.streamV2 != old {
		s.mu.Unlock()
		_ = stream.CloseSend()
		return false, nil
	}
	s.streamV2 = stream
	s.sessionConnectedAt = time.Now()
	s.audioPushed = false
	s.mu.Unlock()
	return true, nil
}

func (s *googleSTTStream) restartFromV1ToV2WithError(old speechpb.Speech_StreamingRecognizeClient) (bool, error) {
	_ = old.CloseSend()
	stream, err := s.owner.newStreamingRecognizeStreamV2(s.ctx, s.language, s.includeAlternativeLanguages)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.closed || s.inputClosed || s.stream != old {
		s.mu.Unlock()
		_ = stream.CloseSend()
		return false, nil
	}
	s.stream = nil
	s.streamV2 = stream
	s.sessionConnectedAt = time.Now()
	s.audioPushed = false
	s.mu.Unlock()
	return true, nil
}

func (s *googleSTTStream) reconnectForUpdatedConfig() error {
	wantV2 := s.owner != nil && s.owner.usesV2()
	haveV2 := s.usesV2()
	if wantV2 && haveV2 {
		_, err := s.restartStreamV2WithError(s.currentStreamV2())
		return err
	}
	if wantV2 {
		_, err := s.restartFromV1ToV2WithError(s.currentStream())
		return err
	}
	if haveV2 {
		_, err := s.restartFromV2ToV1WithError(s.currentStreamV2())
		return err
	}
	_, err := s.restartStreamWithError(s.currentStream())
	return err
}

func (s *googleSTTStream) failWithError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.inputClosed = true
	stream := s.stream
	streamV2 := s.streamV2
	s.mu.Unlock()

	s.errCh <- googleSTTStreamError(err)
	if streamV2 != nil {
		_ = streamV2.CloseSend()
	} else {
		_ = stream.CloseSend()
	}
	s.unregister()
}

func (s *googleSTTStream) terminate() {
	s.mu.Lock()
	if s.inputClosed {
		s.mu.Unlock()
		s.unregister()
		return
	}
	s.inputClosed = true
	s.mu.Unlock()
	s.closeSend()
	s.unregister()
}

func (s *googleSTTStream) closeSend() {
	s.mu.Lock()
	stream := s.stream
	streamV2 := s.streamV2
	s.mu.Unlock()
	if streamV2 != nil {
		_ = streamV2.CloseSend()
		return
	}
	if stream != nil {
		_ = stream.CloseSend()
	}
}

func googleSTTStreamError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return llm.NewAPITimeoutError(err.Error())
	}
	if st, ok := status.FromError(err); ok {
		return llm.NewAPIStatusErrorWithRetryable(st.Message(), int(st.Code()), "", st.Message(), googleSTTStatusRetryable(st.Code()))
	}
	return err
}

func googleSTTStatusRetryable(code codes.Code) bool {
	switch code {
	case codes.InvalidArgument, codes.NotFound, codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition, codes.OutOfRange:
		return false
	default:
		return true
	}
}

func googleStreamingAudioDuration(resp *speechpb.StreamingRecognizeResponse, lastUsageEventTime float64) float64 {
	if resp == nil {
		return 0
	}
	var total float64
	if resp.GetTotalBilledTime() != nil {
		total = resp.GetTotalBilledTime().AsDuration().Seconds()
	} else if resp.GetSpeechEventTime() != nil {
		total = resp.GetSpeechEventTime().AsDuration().Seconds()
	}
	return total - lastUsageEventTime
}

func googleStreamingAudioDurationV2(resp *speechv2pb.StreamingRecognizeResponse, lastUsageEventTime float64) float64 {
	if resp == nil {
		return 0
	}
	var total float64
	if resp.GetMetadata().GetTotalBilledDuration() != nil {
		total = resp.GetMetadata().GetTotalBilledDuration().AsDuration().Seconds()
	} else if resp.GetSpeechEventOffset() != nil {
		total = resp.GetSpeechEventOffset().AsDuration().Seconds()
	}
	return total - lastUsageEventTime
}

func googleStreamingRequestIDV2(resp *speechv2pb.StreamingRecognizeResponse) string {
	if resp == nil || resp.GetMetadata() == nil {
		return ""
	}
	return resp.GetMetadata().GetRequestId()
}

func googleSpeechDataFromStreamingResultsOffset(results []*speechpb.StreamingRecognitionResult, minConfidence float64, startTimeOffset float64) (stt.SpeechData, stt.SpeechEventType, bool) {
	if len(results) == 0 {
		return stt.SpeechData{}, "", false
	}
	if data, handled, ok := googleFinalSpeechDataFromStreamingResults(results, startTimeOffset); handled {
		if !ok {
			return stt.SpeechData{}, "", false
		}
		eventType := stt.SpeechEventInterimTranscript
		if results[0].GetIsFinal() {
			eventType = stt.SpeechEventFinalTranscript
		}
		return data, eventType, true
	}
	var text string
	var confidence float64
	var count int
	resultCount := len(results)
	var words []*speechpb.WordInfo
	for _, result := range results {
		if len(result.GetAlternatives()) == 0 {
			continue
		}
		alt := result.GetAlternatives()[0]
		text += alt.GetTranscript()
		confidence += float64(alt.GetConfidence())
		words = append(words, alt.GetWords()...)
		count++
	}
	if count == 0 || text == "" {
		return stt.SpeechData{}, "", false
	}
	confidence = confidence / float64(resultCount)
	if confidence < minConfidence {
		return stt.SpeechData{}, "", false
	}
	data := stt.SpeechData{
		Language:   googleStreamingResultLanguage(results[0]),
		Text:       text,
		Confidence: confidence,
		Words:      googleTimedStringsOffset(words, startTimeOffset),
	}
	googleApplySpeechDataTiming(&data, words, startTimeOffset)
	return data, stt.SpeechEventInterimTranscript, true
}

func googleSpeechDataFromStreamingResultsV2(results []*speechv2pb.StreamingRecognitionResult, minConfidence float64, startTimeOffset float64) (stt.SpeechData, stt.SpeechEventType, bool) {
	if len(results) == 0 {
		return stt.SpeechData{}, "", false
	}
	if data, handled, ok := googleFinalSpeechDataFromStreamingResultsV2(results, startTimeOffset); handled {
		if !ok {
			return stt.SpeechData{}, "", false
		}
		eventType := stt.SpeechEventInterimTranscript
		if results[0].GetIsFinal() {
			eventType = stt.SpeechEventFinalTranscript
		}
		return data, eventType, true
	}
	var text string
	var confidence float64
	var count int
	resultCount := len(results)
	var words []*speechv2pb.WordInfo
	for _, result := range results {
		if len(result.GetAlternatives()) == 0 {
			continue
		}
		alt := result.GetAlternatives()[0]
		text += alt.GetTranscript()
		confidence += float64(alt.GetConfidence())
		words = append(words, alt.GetWords()...)
		count++
	}
	if count == 0 || text == "" {
		return stt.SpeechData{}, "", false
	}
	confidence = confidence / float64(resultCount)
	if confidence < minConfidence {
		return stt.SpeechData{}, "", false
	}
	data := stt.SpeechData{
		Language:   googleStreamingResultLanguageV2(results[0]),
		Text:       text,
		Confidence: confidence,
		Words:      googleTimedStringsOffsetV2(words, startTimeOffset),
	}
	googleApplySpeechDataTimingV2(&data, words, startTimeOffset)
	return data, stt.SpeechEventInterimTranscript, true
}

func googleFinalSpeechDataFromStreamingResults(results []*speechpb.StreamingRecognitionResult, startTimeOffset float64) (stt.SpeechData, bool, bool) {
	for _, result := range results {
		if !result.GetIsFinal() || len(result.GetAlternatives()) == 0 {
			continue
		}
		alt := result.GetAlternatives()[0]
		words := alt.GetWords()
		if alt.GetTranscript() == "" {
			return stt.SpeechData{}, true, false
		}
		data := stt.SpeechData{
			Language:   googleStreamingResultLanguage(result),
			Text:       alt.GetTranscript(),
			Confidence: float64(alt.GetConfidence()),
			Words:      googleTimedStringsOffset(words, startTimeOffset),
		}
		googleApplySpeechDataTiming(&data, words, startTimeOffset)
		return data, true, true
	}
	return stt.SpeechData{}, false, false
}

func googleFinalSpeechDataFromStreamingResultsV2(results []*speechv2pb.StreamingRecognitionResult, startTimeOffset float64) (stt.SpeechData, bool, bool) {
	for _, result := range results {
		if !result.GetIsFinal() || len(result.GetAlternatives()) == 0 {
			continue
		}
		alt := result.GetAlternatives()[0]
		words := alt.GetWords()
		if alt.GetTranscript() == "" {
			return stt.SpeechData{}, true, false
		}
		data := stt.SpeechData{
			Language:   googleStreamingResultLanguageV2(result),
			Text:       alt.GetTranscript(),
			Confidence: float64(alt.GetConfidence()),
			Words:      googleTimedStringsOffsetV2(words, startTimeOffset),
		}
		googleApplySpeechDataTimingV2(&data, words, startTimeOffset)
		return data, true, true
	}
	return stt.SpeechData{}, false, false
}

func googleStreamingResultLanguage(result *speechpb.StreamingRecognitionResult) string {
	if result == nil {
		return ""
	}
	return result.GetLanguageCode()
}

func googleStreamingResultLanguageV2(result *speechv2pb.StreamingRecognitionResult) string {
	if result == nil {
		return ""
	}
	return result.GetLanguageCode()
}

func googleApplySpeechDataTiming(data *stt.SpeechData, words []*speechpb.WordInfo, startTimeOffset float64) {
	if data == nil {
		return
	}
	if len(words) == 0 {
		if data.Text != "" {
			data.StartTime = startTimeOffset
			data.EndTime = startTimeOffset
		}
		return
	}
	data.StartTime = words[0].GetStartTime().AsDuration().Seconds() + startTimeOffset
	data.EndTime = words[len(words)-1].GetEndTime().AsDuration().Seconds() + startTimeOffset
}

func googleApplySpeechDataTimingV2(data *stt.SpeechData, words []*speechv2pb.WordInfo, startTimeOffset float64) {
	if data == nil {
		return
	}
	if len(words) == 0 {
		if data.Text != "" {
			data.StartTime = startTimeOffset
			data.EndTime = startTimeOffset
		}
		return
	}
	data.StartTime = words[0].GetStartOffset().AsDuration().Seconds() + startTimeOffset
	data.EndTime = words[len(words)-1].GetEndOffset().AsDuration().Seconds() + startTimeOffset
}

func googleTimedStringsOffsetV2(words []*speechv2pb.WordInfo, startTimeOffset float64) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}

	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:            word.GetWord(),
			StartTime:       word.GetStartOffset().AsDuration().Seconds() + startTimeOffset,
			EndTime:         word.GetEndOffset().AsDuration().Seconds() + startTimeOffset,
			StartTimeOffset: startTimeOffset,
			Confidence:      float64(word.GetConfidence()),
			SpeakerID:       word.GetSpeakerLabel(),
		})
	}
	return timed
}

func (s *googleSTTStream) StartTimeOffset() float64 {
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	return s.startTimeOffset
}

func (s *googleSTTStream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	s.startTimeOffset = offset
}

func (s *googleSTTStream) StartTime() float64 {
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	return s.startTime
}

func (s *googleSTTStream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	s.startTime = startTime
}

func (s *googleSTTStream) currentStartTimeOffset() float64 {
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	return s.startTimeOffset
}

func (s *googleSTTStream) currentMinConfidence() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.minConfidence
}

func (s *googleSTTStream) updateConfig(minConfidence float64, language string, languageChanged bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.minConfidence = minConfidence
	if languageChanged {
		s.language = language
		s.includeAlternativeLanguages = true
	}
}

func (s *googleSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	if s.closed || s.inputClosed {
		s.mu.Unlock()
		if s.callerClosedInput {
			return errors.New("stream input ended")
		}
		return io.ErrClosedPipe
	}
	if frame != nil && frame.SampleRate != 0 {
		if s.pushedSampleRate != 0 && s.pushedSampleRate != frame.SampleRate {
			return errors.New("the sample rate of the input frames must be consistent")
		}
		s.pushedSampleRate = frame.SampleRate
		normalized, err := s.inputAudio.normalize(frame, uint32(s.owner.sampleRate))
		if err != nil {
			s.mu.Unlock()
			return err
		}
		frame = normalized
	}
	s.mu.Unlock()
	return s.sendAudioFrame(frame)
}

func (s *googleSTTStream) sendAudioFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	if s.closed || s.inputClosed {
		s.mu.Unlock()
		if s.callerClosedInput {
			return errors.New("stream input ended")
		}
		return io.ErrClosedPipe
	}
	streamV2 := s.streamV2
	stream := s.stream
	s.mu.Unlock()

	if streamV2 != nil {
		if err := streamV2.Send(&speechv2pb.StreamingRecognizeRequest{
			StreamingRequest: &speechv2pb.StreamingRecognizeRequest_Audio{
				Audio: bytes.Clone(frame.Data),
			},
		}); err != nil {
			s.mu.Lock()
			if !s.closed {
				s.closed = true
				_ = streamV2.CloseSend()
				s.unregister()
			}
			s.mu.Unlock()
			return googleSTTStreamError(err)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed || s.inputClosed {
			if s.callerClosedInput {
				return errors.New("stream input ended")
			}
			return io.ErrClosedPipe
		}
		s.audioPushed = true
		return nil
	}
	if err := stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
			AudioContent: bytes.Clone(frame.Data),
		},
	}); err != nil {
		s.mu.Lock()
		if !s.closed {
			s.closed = true
			_ = stream.CloseSend()
			s.unregister()
		}
		s.mu.Unlock()
		return googleSTTStreamError(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputClosed {
		if s.callerClosedInput {
			return errors.New("stream input ended")
		}
		return io.ErrClosedPipe
	}
	s.audioPushed = true
	return nil
}

func (s *googleSTTStream) Flush() error {
	s.mu.Lock()
	if s.closed || s.inputClosed {
		s.mu.Unlock()
		if s.callerClosedInput {
			return errors.New("stream input ended")
		}
		return io.ErrClosedPipe
	}
	if tail := s.inputAudio.flush(); tail != nil {
		s.mu.Unlock()
		return s.sendAudioFrame(tail)
	}
	s.mu.Unlock()
	return nil
}

func (s *googleSTTStream) EndInput() error {
	if err := s.Flush(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputClosed {
		if s.callerClosedInput {
			return errors.New("stream input ended")
		}
		return io.ErrClosedPipe
	}
	s.inputClosed = true
	s.callerClosedInput = true
	if s.streamV2 != nil {
		_ = s.streamV2.CloseSend()
	} else {
		_ = s.stream.CloseSend()
	}
	return nil
}

func (s *googleSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.inputClosed = true
	s.callerClosedInput = true
	if s.streamV2 != nil {
		_ = s.streamV2.CloseSend()
	} else {
		_ = s.stream.CloseSend()
	}
	s.unregister()
	return nil
}

func (s *googleSTTStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *googleSTTStream) Next() (*stt.SpeechEvent, error) {
	if s.isClosed() {
		return nil, io.EOF
	}
	if s.pendingErr != nil {
		if event, ok := s.nextQueuedEvent(); ok {
			return event, nil
		}
		err := s.pendingErr
		s.pendingErr = nil
		return nil, err
	}
	if event, ok := s.nextQueuedEvent(); ok {
		return event, nil
	}
	select {
	case event, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return event, nil
	case err := <-s.errCh:
		if event, ok := s.nextQueuedEvent(); ok {
			s.pendingErr = err
			return event, nil
		}
		return nil, err
	}
}

func (s *googleSTTStream) nextQueuedEvent() (*stt.SpeechEvent, bool) {
	select {
	case event, ok := <-s.events:
		if ok {
			return event, true
		}
	default:
	}
	return nil, false
}

func (s *googleSTTStream) unregister() {
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
}
