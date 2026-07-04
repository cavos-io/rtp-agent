package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
)

type AWSSTT struct {
	mu                                sync.Mutex
	client                            awsSTTClient
	credentials                       AWSCredentials
	credentialsSet                    bool
	sampleRate                        int32
	encoding                          types.MediaEncoding
	language                          types.LanguageCode
	vocabularyName                    string
	vocabularyNameSet                 bool
	sessionID                         string
	sessionIDSet                      bool
	vocabularyFilterMethod            types.VocabularyFilterMethod
	vocabularyFilterName              string
	vocabularyFilterNameSet           bool
	showSpeakerLabel                  bool
	enableChannelIdentification       bool
	numberOfChannels                  int32
	numberOfChannelsSet               bool
	enablePartialResultsStabilization bool
	partialResultsStability           types.PartialResultsStability
	languageModelName                 string
	languageModelNameSet              bool
	identifyLanguage                  bool
	identifyMultipleLanguages         bool
	languageOptions                   string
	languageOptionsSet                bool
	preferredLanguage                 types.LanguageCode
	vocabularyNames                   string
	vocabularyNamesSet                bool
	vocabularyFilterNames             string
	vocabularyFilterNamesSet          bool
	streams                           map[*awsSTTStream]struct{}
	closed                            bool
}

type AWSSTTOption func(*AWSSTT)

type awsSTTClient interface {
	StartStreamTranscription(ctx context.Context, input *transcribestreaming.StartStreamTranscriptionInput) (awsSTTEventStream, error)
}

type awsSTTSDKClient struct {
	client *transcribestreaming.Client
}

func (c awsSTTSDKClient) StartStreamTranscription(ctx context.Context, input *transcribestreaming.StartStreamTranscriptionInput) (awsSTTEventStream, error) {
	out, err := c.client.StartStreamTranscription(ctx, input)
	if err != nil {
		return nil, err
	}
	return out.GetStream(), nil
}

type awsSTTEventStream interface {
	Send(context.Context, types.AudioStream) error
	Events() <-chan types.TranscriptResultStream
	Close() error
	Err() error
}

func WithAWSSTTSampleRate(sampleRate int32) AWSSTTOption {
	return func(s *AWSSTT) {
		s.sampleRate = sampleRate
	}
}

func WithAWSSTTCredentials(creds AWSCredentials) AWSSTTOption {
	return func(s *AWSSTT) {
		if creds.valid() {
			s.credentials = creds
			s.credentialsSet = true
		}
	}
}

func WithAWSSTTLanguage(language types.LanguageCode) AWSSTTOption {
	return func(s *AWSSTT) {
		if language != "" {
			s.language = language
		}
	}
}

func WithAWSSTTVocabularyName(name string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.vocabularyName = name
		s.vocabularyNameSet = true
	}
}

func WithAWSSTTSessionID(sessionID string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.sessionID = sessionID
		s.sessionIDSet = true
	}
}

func WithAWSSTTVocabularyFilterMethod(method types.VocabularyFilterMethod) AWSSTTOption {
	return func(s *AWSSTT) {
		s.vocabularyFilterMethod = method
	}
}

func WithAWSSTTVocabularyFilterName(name string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.vocabularyFilterName = name
		s.vocabularyFilterNameSet = true
	}
}

func WithAWSSTTShowSpeakerLabel(show bool) AWSSTTOption {
	return func(s *AWSSTT) {
		s.showSpeakerLabel = show
	}
}

func WithAWSSTTEnableChannelIdentification(enable bool) AWSSTTOption {
	return func(s *AWSSTT) {
		s.enableChannelIdentification = enable
	}
}

func WithAWSSTTNumberOfChannels(channels int32) AWSSTTOption {
	return func(s *AWSSTT) {
		s.numberOfChannels = channels
		s.numberOfChannelsSet = true
	}
}

func WithAWSSTTEnablePartialResultsStabilization(enable bool) AWSSTTOption {
	return func(s *AWSSTT) {
		s.enablePartialResultsStabilization = enable
	}
}

func WithAWSSTTPartialResultsStability(stability types.PartialResultsStability) AWSSTTOption {
	return func(s *AWSSTT) {
		s.partialResultsStability = stability
	}
}

func WithAWSSTTLanguageModelName(name string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.languageModelName = name
		s.languageModelNameSet = true
	}
}

func WithAWSSTTIdentifyLanguage(identify bool) AWSSTTOption {
	return func(s *AWSSTT) {
		s.identifyLanguage = identify
	}
}

func WithAWSSTTIdentifyMultipleLanguages(identify bool) AWSSTTOption {
	return func(s *AWSSTT) {
		s.identifyMultipleLanguages = identify
	}
}

func WithAWSSTTLanguageOptions(options string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.languageOptions = options
		s.languageOptionsSet = true
	}
}

func WithAWSSTTPreferredLanguage(language types.LanguageCode) AWSSTTOption {
	return func(s *AWSSTT) {
		s.preferredLanguage = language
	}
}

func WithAWSSTTVocabularyNames(names string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.vocabularyNames = names
		s.vocabularyNamesSet = true
	}
}

func WithAWSSTTVocabularyFilterNames(names string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.vocabularyFilterNames = names
		s.vocabularyFilterNamesSet = true
	}
}

func NewAWSSTT(ctx context.Context, region string, providerOpts ...AWSSTTOption) (*AWSSTT, error) {
	provider, err := newAWSSTTWithClient(nil, providerOpts...)
	if err != nil {
		return nil, err
	}

	opts := []func(*config.LoadOptions) error{config.WithRegion(awsSTTRegionOrDefault(region))}
	if opt := awsCredentialsLoadOption(provider.credentials, provider.credentialsSet); opt != nil {
		opts = append(opts, opt)
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	provider.client = awsSTTSDKClient{client: transcribestreaming.NewFromConfig(cfg)}
	return provider, nil
}

func awsSTTRegionOrDefault(region string) string {
	if region != "" {
		return region
	}
	if envRegion := os.Getenv("AWS_REGION"); envRegion != "" {
		return envRegion
	}
	return defaultAWSRegion
}

func newAWSSTTWithClient(client awsSTTClient, opts ...AWSSTTOption) (*AWSSTT, error) {
	provider := &AWSSTT{
		client:     client,
		sampleRate: 24000,
		encoding:   types.MediaEncodingPcm,
		language:   types.LanguageCodeEnUs,
		streams:    make(map[*awsSTTStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.identifyLanguage && provider.identifyMultipleLanguages {
		return nil, fmt.Errorf("identify_language and identify_multiple_languages are mutually exclusive. Set only one to true")
	}
	return provider, nil
}

func (s *AWSSTT) Label() string { return "aws.STT" }
func (s *AWSSTT) Model() string {
	if s == nil || !s.languageModelNameSet {
		return "unknown"
	}
	return s.languageModelName
}
func (s *AWSSTT) Provider() string { return "Amazon Transcribe" }
func (s *AWSSTT) Language() string {
	if s == nil {
		return ""
	}
	return string(s.language)
}
func (s *AWSSTT) InputSampleRate() uint32 {
	if s == nil {
		return 24000
	}
	return uint32(s.sampleRate)
}
func (s *AWSSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: "word", OfflineRecognize: false}
}

func (s *AWSSTT) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	streams := make([]*awsSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = make(map[*awsSTTStream]struct{})
	s.mu.Unlock()

	for _, stream := range streams {
		_ = stream.Close()
	}
	return nil
}

func (s *AWSSTT) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *AWSSTT) registerStream(stream *awsSTTStream) bool {
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
		s.streams = make(map[*awsSTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
	s.mu.Unlock()
	return true
}

func (s *AWSSTT) unregisterStream(stream *awsSTTStream) {
	if s == nil || stream == nil {
		return
	}
	s.mu.Lock()
	delete(s.streams, stream)
	s.mu.Unlock()
}

func (s *AWSSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if s.client == nil {
		return nil, llm.NewAPIConnectionError("aws transcribe client is not configured")
	}
	input := buildAWSStartStreamTranscriptionInput(s, language)
	stream, err := s.client.StartStreamTranscription(ctx, input)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("AWS Transcribe stream start failed: %v", err))
	}

	eventStream := newAWSRealtimeQueuedStream[*stt.SpeechEvent]()
	gs := &awsSTTStream{
		provider:                 s,
		stream:                   stream,
		restart:                  func() (awsSTTEventStream, error) { return s.client.StartStreamTranscription(ctx, input) },
		language:                 input.LanguageCode,
		identifyLanguage:         s.identifyLanguage,
		identifyMultipleLanguage: s.identifyMultipleLanguages,
		events:                   eventStream.Chan(),
		eventStream:              eventStream,
		errCh:                    make(chan error, 1),
	}
	if !s.registerStream(gs) {
		return nil, io.ErrClosedPipe
	}
	go gs.readLoop()

	return gs, nil
}

func buildAWSStartStreamTranscriptionInput(s *AWSSTT, language string) *transcribestreaming.StartStreamTranscriptionInput {
	languageCode := s.language
	input := &transcribestreaming.StartStreamTranscriptionInput{
		MediaEncoding:        s.encoding,
		MediaSampleRateHertz: aws.Int32(s.sampleRate),
	}
	if s.identifyLanguage {
		input.IdentifyLanguage = true
		applyAWSSTTLanguageDetectionOptions(input, s)
	} else if s.identifyMultipleLanguages {
		input.IdentifyMultipleLanguages = true
		applyAWSSTTLanguageDetectionOptions(input, s)
	} else {
		input.LanguageCode = languageCode
	}
	if s.vocabularyNameSet {
		input.VocabularyName = aws.String(s.vocabularyName)
	}
	if s.sessionIDSet {
		input.SessionId = aws.String(s.sessionID)
	}
	if s.vocabularyFilterMethod != "" {
		input.VocabularyFilterMethod = s.vocabularyFilterMethod
	}
	if s.vocabularyFilterNameSet {
		input.VocabularyFilterName = aws.String(s.vocabularyFilterName)
	}
	input.ShowSpeakerLabel = s.showSpeakerLabel
	input.EnableChannelIdentification = s.enableChannelIdentification
	if s.numberOfChannelsSet {
		input.NumberOfChannels = aws.Int32(s.numberOfChannels)
	}
	input.EnablePartialResultsStabilization = s.enablePartialResultsStabilization
	if s.partialResultsStability != "" {
		input.PartialResultsStability = s.partialResultsStability
	}
	if s.languageModelNameSet {
		input.LanguageModelName = aws.String(s.languageModelName)
	}
	return input
}

func applyAWSSTTLanguageDetectionOptions(input *transcribestreaming.StartStreamTranscriptionInput, s *AWSSTT) {
	if s.languageOptionsSet {
		input.LanguageOptions = aws.String(s.languageOptions)
	}
	if s.preferredLanguage != "" {
		input.PreferredLanguage = s.preferredLanguage
	}
	if s.vocabularyNamesSet {
		input.VocabularyNames = aws.String(s.vocabularyNames)
	}
	if s.vocabularyFilterNamesSet {
		input.VocabularyFilterNames = aws.String(s.vocabularyFilterNames)
	}
}

func (s *AWSSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	// AWS Transcribe (non-streaming) uses jobs on S3. Since we don't have S3 upload configured,
	// offline recognize is unsupported natively via simple buffer upload.
	return nil, fmt.Errorf("offline recognize is not natively supported by AWSSTT via simple upload (S3 required). Use Stream instead")
}

type awsSTTStream struct {
	provider                 *AWSSTT
	stream                   awsSTTEventStream
	restart                  func() (awsSTTEventStream, error)
	language                 types.LanguageCode
	identifyLanguage         bool
	identifyMultipleLanguage bool
	events                   chan *stt.SpeechEvent
	eventStream              *awsRealtimeQueuedStream[*stt.SpeechEvent]
	errCh                    chan error
	streamMu                 sync.Mutex
	closed                   bool
	closedCh                 chan struct{}
	inputEnded               bool
	inputEndedCh             chan struct{}
	pushedSampleRate         uint32
	speaking                 bool
	timingMu                 sync.Mutex
	startTimeOffset          float64
	startTime                float64
}

func (s *awsSTTStream) readLoop() {
	defer func() {
		if s.provider != nil {
			s.provider.unregisterStream(s)
		}
	}()
	defer s.closeSpeechEvents()
	for {
		stream := s.currentStream()
		if stream == nil {
			return
		}
		event := <-stream.Events()
		if event == nil {
			if err := stream.Err(); err != nil {
				if isAWSSTTRequestTimeout(err) {
					closeAWSSTTEventStream(stream)
					if s.restartAfterTimeout() {
						continue
					}
					return
				}
				if err != io.EOF && !isHarmlessAWSSTTStreamCloseError(err) {
					if errors.Is(err, context.DeadlineExceeded) {
						s.errCh <- llm.NewAPITimeoutError(err.Error())
						return
					}
					s.errCh <- llm.NewAPIConnectionError(fmt.Sprintf("AWS Transcribe stream failed: %v", err))
				}
			}
			return
		}

		switch v := event.(type) {
		case *types.TranscriptResultStreamMemberTranscriptEvent:
			if v.Value.Transcript == nil || len(v.Value.Transcript.Results) == 0 {
				continue
			}
			for _, result := range v.Value.Transcript.Results {
				if result.StartTime == 0 {
					s.speaking = true
					if !s.sendSpeechEvent(&stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}) {
						return
					}
				}

				if result.EndTime > 0 {
					eventType := stt.SpeechEventInterimTranscript
					if !result.IsPartial {
						eventType = stt.SpeechEventFinalTranscript
					}

					if !s.sendSpeechEvent(&stt.SpeechEvent{
						Type: eventType,
						Alternatives: []stt.SpeechData{
							awsSpeechDataFromResultOffset(result, s.currentStartTimeOffset(), string(s.language), s.identifyLanguage || s.identifyMultipleLanguage),
						},
					}) {
						return
					}
				}
				if !result.IsPartial {
					if !s.sendSpeechEvent(&stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}) {
						return
					}
					s.speaking = false
				}
			}
		}
	}
}

func isHarmlessAWSSTTStreamCloseError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "complete signal was sent without the preceding empty frame") ||
		strings.Contains(message, "InvalidStateError")
}

func isAWSSTTRequestTimeout(err error) bool {
	return strings.HasPrefix(err.Error(), "Your request timed out")
}

func closeAWSSTTEventStream(stream awsSTTEventStream) {
	if stream == nil {
		return
	}
	_ = stream.Send(context.Background(), &types.AudioStreamMemberAudioEvent{
		Value: types.AudioEvent{
			AudioChunk: []byte{},
		},
	})
	_ = stream.Close()
}

func closeAWSSTTEventStreamInput(stream awsSTTEventStream) error {
	if stream == nil {
		return nil
	}
	if sdkStream, ok := stream.(*transcribestreaming.StartStreamTranscriptionEventStream); ok && sdkStream.Writer != nil {
		return sdkStream.Writer.Close()
	}
	return stream.Close()
}

func (s *awsSTTStream) currentStream() awsSTTEventStream {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	return s.stream
}

func (s *awsSTTStream) restartAfterTimeout() bool {
	if s.restart == nil || s.isClosed() {
		return false
	}
	stream, err := s.restart()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			s.errCh <- llm.NewAPITimeoutError(err.Error())
			return false
		}
		s.errCh <- llm.NewAPIConnectionError(fmt.Sprintf("AWS Transcribe stream restart failed: %v", err))
		return false
	}
	s.streamMu.Lock()
	if s.closed {
		s.streamMu.Unlock()
		_ = stream.Close()
		return false
	}
	inputEnded := s.inputEnded
	s.stream = stream
	s.streamMu.Unlock()
	if inputEnded {
		_ = stream.Send(context.Background(), &types.AudioStreamMemberAudioEvent{
			Value: types.AudioEvent{
				AudioChunk: []byte{},
			},
		})
		_ = closeAWSSTTEventStreamInput(stream)
	}
	return true
}

func awsSpeechDataFromResultOffset(result types.Result, startTimeOffset float64, fallbackLanguage string, includeSourceLanguages bool) stt.SpeechData {
	if len(result.Alternatives) == 0 {
		return stt.SpeechData{
			Language:        awsResultLanguage(result, fallbackLanguage),
			StartTime:       result.StartTime + startTimeOffset,
			EndTime:         result.EndTime + startTimeOffset,
			SourceLanguages: awsResultSourceLanguages(result, includeSourceLanguages),
		}
	}
	data := awsSpeechDataFromAlternativeOffset(result.Alternatives[0], startTimeOffset)
	data.Language = awsResultLanguage(result, fallbackLanguage)
	data.StartTime = result.StartTime + startTimeOffset
	data.EndTime = result.EndTime + startTimeOffset
	data.SourceLanguages = awsResultSourceLanguages(result, includeSourceLanguages)
	return data
}

func awsResultLanguage(result types.Result, fallbackLanguage string) string {
	if result.LanguageCode != "" {
		return string(result.LanguageCode)
	}
	if fallbackLanguage != "" {
		return fallbackLanguage
	}
	return string(types.LanguageCodeEnUs)
}

func awsResultSourceLanguages(result types.Result, include bool) []string {
	if !include || result.LanguageCode == "" {
		return nil
	}
	return []string{string(result.LanguageCode)}
}

func awsSpeechDataFromAlternative(alt types.Alternative) stt.SpeechData {
	return awsSpeechDataFromAlternativeOffset(alt, 0)
}

func awsSpeechDataFromAlternativeOffset(alt types.Alternative, startTimeOffset float64) stt.SpeechData {
	return stt.SpeechData{
		Text:       aws.ToString(alt.Transcript),
		Confidence: awsAlternativeConfidence(alt.Items),
		Words:      awsTimedStringsOffset(alt.Items, startTimeOffset),
	}
}

func awsAlternativeConfidence(items []types.Item) float64 {
	if len(items) == 0 {
		return 0
	}
	return aws.ToFloat64(items[0].Confidence)
}

func awsTimedStringsOffset(items []types.Item, startTimeOffset float64) []stt.TimedString {
	if len(items) == 0 {
		return nil
	}

	words := make([]stt.TimedString, 0, len(items))
	for _, item := range items {
		words = append(words, stt.TimedString{
			Text:            aws.ToString(item.Content),
			StartTime:       item.StartTime + startTimeOffset,
			EndTime:         item.EndTime + startTimeOffset,
			StartTimeOffset: startTimeOffset,
			Confidence:      aws.ToFloat64(item.Confidence),
			SpeakerID:       aws.ToString(item.Speaker),
		})
	}
	return words
}

func (s *awsSTTStream) StartTimeOffset() float64 {
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	return s.startTimeOffset
}

func (s *awsSTTStream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	s.startTimeOffset = offset
}

func (s *awsSTTStream) StartTime() float64 {
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	return s.startTime
}

func (s *awsSTTStream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	s.startTime = startTime
}

func (s *awsSTTStream) currentStartTimeOffset() float64 {
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	return s.startTimeOffset
}

func (s *awsSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.streamMu.Lock()
	stream := s.stream
	closed := s.closed
	inputEnded := s.inputEnded
	if closed || inputEnded {
		s.streamMu.Unlock()
		if closed {
			return io.ErrClosedPipe
		}
		return fmt.Errorf("stream input ended")
	}
	if frame != nil && frame.SampleRate != 0 {
		if s.pushedSampleRate != 0 && s.pushedSampleRate != frame.SampleRate {
			s.streamMu.Unlock()
			return fmt.Errorf("the sample rate of the input frames must be consistent")
		}
		s.pushedSampleRate = frame.SampleRate
	}
	s.streamMu.Unlock()
	if frame == nil {
		return nil
	}
	if stream == nil {
		return llm.NewAPIConnectionError("AWS Transcribe stream is not initialized")
	}
	ctx, cancel := s.writeContext()
	defer cancel()
	if err := stream.Send(ctx, &types.AudioStreamMemberAudioEvent{
		Value: types.AudioEvent{
			AudioChunk: frame.Data,
		},
	}); err != nil {
		if s.isClosed() && (errors.Is(err, context.Canceled) || errors.Is(err, io.ErrClosedPipe)) {
			return nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("AWS Transcribe audio write failed: %v", err))
	}
	return nil
}

func (s *awsSTTStream) writeContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		select {
		case <-s.inputClosedSignal():
			cancel()
		case <-done:
		}
	}()
	return ctx, func() {
		close(done)
		cancel()
	}
}

func (s *awsSTTStream) Flush() error {
	if s.isClosed() {
		return io.ErrClosedPipe
	}
	if s.isInputEnded() {
		return fmt.Errorf("stream input ended")
	}
	return nil
}

func (s *awsSTTStream) EndInput() error {
	stream, err := s.endInputStream()
	if err != nil {
		return err
	}
	if stream == nil {
		return nil
	}
	_ = stream.Send(context.Background(), &types.AudioStreamMemberAudioEvent{
		Value: types.AudioEvent{
			AudioChunk: []byte{},
		},
	})
	_ = closeAWSSTTEventStreamInput(stream)
	return nil
}

func (s *awsSTTStream) Close() error {
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	stream, closed, inputAlreadyEnded := s.closeStream()
	if closed {
		return nil
	}
	if stream == nil {
		return nil
	}
	if !inputAlreadyEnded {
		_ = stream.Send(context.Background(), &types.AudioStreamMemberAudioEvent{
			Value: types.AudioEvent{
				AudioChunk: []byte{},
			},
		})
	}
	_ = stream.Close()
	return nil
}

func (s *awsSTTStream) sendSpeechEvent(event *stt.SpeechEvent) bool {
	if s.eventStream != nil {
		return s.eventStream.Send(event)
	}
	select {
	case s.events <- event:
		return true
	case <-s.closedSignal():
		return false
	}
}

func (s *awsSTTStream) closeSpeechEvents() {
	if s.eventStream != nil {
		s.eventStream.Close()
		return
	}
	close(s.events)
}

func (s *awsSTTStream) closeStream() (awsSTTEventStream, bool, bool) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.closed {
		return nil, true, s.inputEnded
	}
	wasInputEnded := s.inputEnded
	s.closed = true
	s.inputEnded = true
	if s.closedCh == nil {
		s.closedCh = make(chan struct{})
	}
	if s.inputEndedCh == nil {
		s.inputEndedCh = make(chan struct{})
	}
	close(s.closedCh)
	if !wasInputEnded {
		close(s.inputEndedCh)
	}
	return s.stream, false, wasInputEnded
}

func (s *awsSTTStream) endInputStream() (awsSTTEventStream, error) {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.inputEnded {
		return nil, fmt.Errorf("stream input ended")
	}
	if s.closed {
		return nil, io.ErrClosedPipe
	}
	s.inputEnded = true
	if s.inputEndedCh == nil {
		s.inputEndedCh = make(chan struct{})
	}
	close(s.inputEndedCh)
	return s.stream, nil
}

func (s *awsSTTStream) isClosed() bool {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	return s.closed
}

func (s *awsSTTStream) isInputEnded() bool {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	return s.inputEnded
}

func (s *awsSTTStream) closedSignal() <-chan struct{} {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.closedCh == nil {
		s.closedCh = make(chan struct{})
		if s.closed {
			close(s.closedCh)
		}
	}
	return s.closedCh
}

func (s *awsSTTStream) inputClosedSignal() <-chan struct{} {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.inputEndedCh == nil {
		s.inputEndedCh = make(chan struct{})
		if s.inputEnded || s.closed {
			close(s.inputEndedCh)
		}
	}
	return s.inputEndedCh
}

func (s *awsSTTStream) Next() (*stt.SpeechEvent, error) {
	if s.isClosed() {
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
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
		if event, ok := s.nextQueuedSpeechEvent(); ok {
			return event, nil
		}
		return nil, err
	}
}

func (s *awsSTTStream) nextQueuedSpeechEvent() (*stt.SpeechEvent, bool) {
	if s.eventStream != nil {
		if event, ok := s.eventStream.TryPopQueued(); ok {
			return event, true
		}
	}
	select {
	case event, ok := <-s.events:
		if ok {
			return event, true
		}
	default:
	}
	return nil, false
}
