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
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const googleSTTMaxSessionDuration = 240 * time.Second

type GoogleSTT struct {
	mu                   sync.Mutex
	streams              map[*googleSTTStream]struct{}
	client               googleSpeechClient
	closed               bool
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
	enableWordTimeOffset bool
	enableWordConfidence bool
	speechStartTimeout   time.Duration
	speechEndTimeout     time.Duration
	location             string
	keywords             []GoogleSTTKeyword
	adaptation           *speechpb.SpeechAdaptation
	alternativeLanguages []string
}

type googleSpeechClient interface {
	StreamingRecognize(ctx context.Context, opts ...gax.CallOption) (speechpb.Speech_StreamingRecognizeClient, error)
	Recognize(ctx context.Context, req *speechpb.RecognizeRequest, opts ...gax.CallOption) (*speechpb.RecognizeResponse, error)
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
	clientOpts, err := googleClientOptionsFromCredentialsFile(credentialsFile)
	if err != nil {
		return nil, err
	}
	if endpoint := googleSTTEndpoint(provider); endpoint != "" {
		clientOpts = append(clientOpts, option.WithEndpoint(endpoint))
	}

	client, err := speech.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}
	provider.client = client

	return provider, nil
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
	return stt.STTCapabilities{Streaming: s.streaming, InterimResults: true, Diarization: false, AlignedTranscript: alignedTranscript, OfflineRecognize: true}
}

func (s *GoogleSTT) UpdateOptions(opts ...GoogleSTTOption) {
	s.mu.Lock()
	oldLanguage := s.language
	oldAlternativeLanguages := append([]string(nil), s.alternativeLanguages...)
	for _, opt := range opts {
		opt(s)
	}
	minConfidence := s.minConfidence
	language := s.language
	languageChanged := oldLanguage != language
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

	stream, err := s.newStreamingRecognizeStream(ctx, language, !explicitLanguage)
	if err != nil {
		return nil, err
	}
	if s.isClosed() {
		_ = stream.CloseSend()
		return nil, io.ErrClosedPipe
	}

	gs := &googleSTTStream{
		owner:                       s,
		ctx:                         ctx,
		stream:                      stream,
		sessionConnectedAt:          time.Now(),
		language:                    language,
		includeAlternativeLanguages: !explicitLanguage,
		minConfidence:               s.minConfidence,
		events:                      make(chan *stt.SpeechEvent, 10),
		errCh:                       make(chan error, 1),
	}
	if !s.registerStream(gs) {
		return nil, io.ErrClosedPipe
	}
	go gs.readLoop()

	return gs, nil
}

func (s *GoogleSTT) newStreamingRecognizeStream(ctx context.Context, language string, includeAlternativeLanguages bool) (speechpb.Speech_StreamingRecognizeClient, error) {
	stream, err := s.client.StreamingRecognize(ctx)
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

	resp, err := s.client.Recognize(ctx, &speechpb.RecognizeRequest{
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
		Alternatives: googleSpeechDataFromRecognizeResults(resp.Results, language),
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

func googleEnableWordTimeOffsets(s *GoogleSTT) bool {
	if s.model == "chirp_3" {
		return false
	}
	return s.enableWordTimeOffset
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

func googleSpeechDataFromRecognizeResults(results []*speechpb.SpeechRecognitionResult, language string) []stt.SpeechData {
	if len(results) == 0 {
		return []stt.SpeechData{}
	}
	var text string
	var confidence float64
	var count int
	var firstWords []*speechpb.WordInfo
	var timingWords []*speechpb.WordInfo
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
		timingWords = append(timingWords, alt.GetWords()...)
		count++
	}
	if count == 0 {
		return []stt.SpeechData{}
	}
	data := stt.SpeechData{
		Language:   googleRecognizeResultLanguage(results, language),
		Text:       text,
		Confidence: confidence / float64(count),
		Words:      googleTimedStrings(firstWords),
	}
	googleApplySpeechDataTiming(&data, timingWords, 0)
	return []stt.SpeechData{data}
}

func googleRecognizeResultLanguage(results []*speechpb.SpeechRecognitionResult, fallback string) string {
	if len(results) == 0 || results[0].GetLanguageCode() == "" {
		return fallback
	}
	return results[0].GetLanguageCode()
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
	sessionConnectedAt          time.Time
	language                    string
	includeAlternativeLanguages bool
	minConfidence               float64
	events                      chan *stt.SpeechEvent
	errCh                       chan error
	pendingErr                  error
	closed                      bool
	inputClosed                 bool
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
			if err != io.EOF {
				s.errCh <- googleSTTStreamError(err)
			}
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
			if data, eventType, ok := googleSpeechDataFromStreamingResultsOffset(resp.Results, s.currentMinConfidence(), s.currentStartTimeOffset(), s.language); ok {
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

func (s *googleSTTStream) currentStream() speechpb.Speech_StreamingRecognizeClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream
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

func (s *googleSTTStream) reconnectForUpdatedConfig() error {
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
	s.mu.Unlock()

	s.errCh <- googleSTTStreamError(err)
	_ = stream.CloseSend()
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
	_ = s.stream.CloseSend()
	s.unregister()
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

func googleSpeechDataFromStreamingResultsOffset(results []*speechpb.StreamingRecognitionResult, minConfidence float64, startTimeOffset float64, language string) (stt.SpeechData, stt.SpeechEventType, bool) {
	if len(results) == 0 {
		return stt.SpeechData{}, "", false
	}
	if data, handled, ok := googleFinalSpeechDataFromStreamingResults(results, startTimeOffset, language); handled {
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
		Language:   googleStreamingResultLanguage(results[0], language),
		Text:       text,
		Confidence: confidence,
		Words:      googleTimedStringsOffset(words, startTimeOffset),
	}
	googleApplySpeechDataTiming(&data, words, startTimeOffset)
	return data, stt.SpeechEventInterimTranscript, true
}

func googleFinalSpeechDataFromStreamingResults(results []*speechpb.StreamingRecognitionResult, startTimeOffset float64, language string) (stt.SpeechData, bool, bool) {
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
			Language:   googleStreamingResultLanguage(result, language),
			Text:       alt.GetTranscript(),
			Confidence: float64(alt.GetConfidence()),
			Words:      googleTimedStringsOffset(words, startTimeOffset),
		}
		googleApplySpeechDataTiming(&data, words, startTimeOffset)
		return data, true, true
	}
	return stt.SpeechData{}, false, false
}

func googleStreamingResultLanguage(result *speechpb.StreamingRecognitionResult, fallback string) string {
	if result == nil || result.GetLanguageCode() == "" {
		return fallback
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
	defer s.mu.Unlock()
	if s.closed || s.inputClosed {
		return io.ErrClosedPipe
	}
	if frame != nil && frame.SampleRate != 0 {
		if s.pushedSampleRate != 0 && s.pushedSampleRate != frame.SampleRate {
			return errors.New("the sample rate of the input frames must be consistent")
		}
		s.pushedSampleRate = frame.SampleRate
		normalized, err := s.inputAudio.normalize(frame, uint32(s.owner.sampleRate))
		if err != nil {
			return err
		}
		frame = normalized
	}
	return s.sendAudioFrameLocked(frame)
}

func (s *googleSTTStream) sendAudioFrameLocked(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	if err := s.stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
			AudioContent: frame.Data,
		},
	}); err != nil {
		s.closed = true
		_ = s.stream.CloseSend()
		s.unregister()
		return googleSTTStreamError(err)
	}
	s.audioPushed = true
	return nil
}

func (s *googleSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputClosed {
		return io.ErrClosedPipe
	}
	if tail := s.inputAudio.flush(); tail != nil {
		return s.sendAudioFrameLocked(tail)
	}
	return nil
}

func (s *googleSTTStream) EndInput() error {
	if err := s.Flush(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputClosed {
		return io.ErrClosedPipe
	}
	s.inputClosed = true
	_ = s.stream.CloseSend()
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
	_ = s.stream.CloseSend()
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
