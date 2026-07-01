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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type GoogleSTT struct {
	mu                   sync.Mutex
	streams              map[*googleSTTStream]struct{}
	client               googleSpeechClient
	closed               bool
	model                string
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
}

type googleSpeechClient interface {
	StreamingRecognize(ctx context.Context, opts ...gax.CallOption) (speechpb.Speech_StreamingRecognizeClient, error)
	Recognize(ctx context.Context, req *speechpb.RecognizeRequest, opts ...gax.CallOption) (*speechpb.RecognizeResponse, error)
}

type GoogleSTTOption func(*GoogleSTT)

func WithGoogleSTTModel(model string) GoogleSTTOption {
	return func(s *GoogleSTT) {
		if model != "" {
			s.model = model
		}
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
		if timeout > 0 {
			s.speechStartTimeout = timeout
		}
	}
}

func WithGoogleSTTSpeechEndTimeout(timeout time.Duration) GoogleSTTOption {
	return func(s *GoogleSTT) {
		if timeout > 0 {
			s.speechEndTimeout = timeout
		}
	}
}

func WithGoogleSTTWordConfidence(enabled bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.enableWordConfidence = enabled
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
		if threshold >= 0 {
			s.minConfidence = threshold
		}
	}
}

// NewGoogleSTT creates a new STT client using Application Default Credentials,
// or by providing a path to a credentials JSON file.
func NewGoogleSTT(credentialsFile string, providerOpts ...GoogleSTTOption) (*GoogleSTT, error) {
	ctx := context.Background()
	clientOpts, err := googleClientOptionsFromCredentialsFile(credentialsFile)
	if err != nil {
		return nil, err
	}

	client, err := speech.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}

	return newGoogleSTTWithClient(client, providerOpts...), nil
}

func newGoogleSTTWithClient(client googleSpeechClient, opts ...GoogleSTTOption) *GoogleSTT {
	provider := &GoogleSTT{
		streams:              make(map[*googleSTTStream]struct{}),
		client:               client,
		model:                "latest_long",
		punctuate:            true,
		sampleRate:           16000,
		minConfidence:        0.65,
		enableWordTimeOffset: true,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *GoogleSTT) Label() string           { return "google.STT" }
func (s *GoogleSTT) InputSampleRate() uint32 { return uint32(s.sampleRate) }
func (s *GoogleSTT) Capabilities() stt.STTCapabilities {
	alignedTranscript := ""
	if googleEnableWordTimeOffsets(s) {
		alignedTranscript = "word"
	}
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: alignedTranscript, OfflineRecognize: true}
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
	if language == "" {
		language = "en-US"
	}

	stream, err := s.client.StreamingRecognize(ctx)
	if err != nil {
		return nil, googleSTTStreamError(err)
	}
	if s.isClosed() {
		_ = stream.CloseSend()
		return nil, io.ErrClosedPipe
	}

	err = stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				Config:                    googleRecognitionConfig(s, language),
				InterimResults:            true,
				EnableVoiceActivityEvents: s.voiceActivityEvents || s.speechStartTimeout > 0 || s.speechEndTimeout > 0,
				VoiceActivityTimeout:      googleVoiceActivityTimeout(s),
			},
		},
	})

	if err != nil {
		_ = stream.CloseSend()
		return nil, googleSTTStreamError(err)
	}
	if s.isClosed() {
		_ = stream.CloseSend()
		return nil, io.ErrClosedPipe
	}

	gs := &googleSTTStream{
		owner:         s,
		stream:        stream,
		language:      language,
		minConfidence: s.minConfidence,
		events:        make(chan *stt.SpeechEvent, 10),
		errCh:         make(chan error, 1),
	}
	if !s.registerStream(gs) {
		return nil, io.ErrClosedPipe
	}
	go gs.readLoop()

	return gs, nil
}

func (s *GoogleSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if language == "" {
		language = "en-US"
	}

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	resp, err := s.client.Recognize(ctx, &speechpb.RecognizeRequest{
		Config: googleRecognitionConfig(s, language),
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
	return &speechpb.RecognitionConfig{
		Encoding:                   speechpb.RecognitionConfig_LINEAR16,
		SampleRateHertz:            s.sampleRate,
		LanguageCode:               language,
		EnableWordTimeOffsets:      googleEnableWordTimeOffsets(s),
		EnableWordConfidence:       s.enableWordConfidence,
		EnableAutomaticPunctuation: s.punctuate,
		EnableSpokenPunctuation:    wrapperspb.Bool(s.spokenPunctuation),
		ProfanityFilter:            s.profanityFilter,
		Model:                      s.model,
	}
}

func googleEnableWordTimeOffsets(s *GoogleSTT) bool {
	if s.model == "chirp_3" {
		return false
	}
	return s.enableWordTimeOffset
}

func googleVoiceActivityTimeout(s *GoogleSTT) *speechpb.StreamingRecognitionConfig_VoiceActivityTimeout {
	if s.speechStartTimeout <= 0 && s.speechEndTimeout <= 0 {
		return nil
	}
	timeout := &speechpb.StreamingRecognitionConfig_VoiceActivityTimeout{}
	if s.speechStartTimeout > 0 {
		timeout.SpeechStartTimeout = durationpb.New(s.speechStartTimeout)
	}
	if s.speechEndTimeout > 0 {
		timeout.SpeechEndTimeout = durationpb.New(s.speechEndTimeout)
	}
	return timeout
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
	mu               sync.Mutex
	owner            *GoogleSTT
	stream           speechpb.Speech_StreamingRecognizeClient
	language         string
	minConfidence    float64
	events           chan *stt.SpeechEvent
	errCh            chan error
	closed           bool
	pushedSampleRate uint32
	timingMu         sync.Mutex
	startTimeOffset  float64
	startTime        float64
}

func (s *googleSTTStream) readLoop() {
	defer close(s.events)
	var lastUsageEventTime float64
	for {
		resp, err := s.stream.Recv()
		if err != nil {
			if err != io.EOF {
				s.errCh <- googleSTTStreamError(err)
			}
			return
		}

		switch resp.GetSpeechEventType() {
		case speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_BEGIN:
			s.events <- &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}
		case speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_END:
			s.events <- &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
		}

		if resp.GetSpeechEventType() == speechpb.StreamingRecognizeResponse_SPEECH_EVENT_UNSPECIFIED {
			if data, eventType, ok := googleSpeechDataFromStreamingResultsOffset(resp.Results, s.minConfidence, s.currentStartTimeOffset(), s.language); ok {
				s.events <- &stt.SpeechEvent{
					Type:         eventType,
					Alternatives: []stt.SpeechData{data},
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
	case codes.InvalidArgument, codes.NotFound, codes.AlreadyExists, codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition, codes.OutOfRange:
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
	if data, ok := googleFinalSpeechDataFromStreamingResults(results, startTimeOffset, language); ok {
		return data, stt.SpeechEventFinalTranscript, true
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

func googleFinalSpeechDataFromStreamingResults(results []*speechpb.StreamingRecognitionResult, startTimeOffset float64, language string) (stt.SpeechData, bool) {
	for _, result := range results {
		if !result.GetIsFinal() || len(result.GetAlternatives()) == 0 {
			continue
		}
		alt := result.GetAlternatives()[0]
		words := alt.GetWords()
		data := stt.SpeechData{
			Language:   googleStreamingResultLanguage(result, language),
			Text:       alt.GetTranscript(),
			Confidence: float64(alt.GetConfidence()),
			Words:      googleTimedStringsOffset(words, startTimeOffset),
		}
		googleApplySpeechDataTiming(&data, words, startTimeOffset)
		return data, true
	}
	return stt.SpeechData{}, false
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

func (s *googleSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if frame != nil && frame.SampleRate != 0 {
		if s.pushedSampleRate != 0 && s.pushedSampleRate != frame.SampleRate {
			return errors.New("the sample rate of the input frames must be consistent")
		}
		s.pushedSampleRate = frame.SampleRate
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
	return nil
}

func (s *googleSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
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
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		return nil, err
	}
}

func (s *googleSTTStream) unregister() {
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
}
