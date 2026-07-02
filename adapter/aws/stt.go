package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	client                            awsSTTClient
	sampleRate                        int32
	encoding                          types.MediaEncoding
	language                          types.LanguageCode
	vocabularyName                    string
	sessionID                         string
	vocabularyFilterMethod            types.VocabularyFilterMethod
	vocabularyFilterName              string
	showSpeakerLabel                  bool
	enableChannelIdentification       bool
	numberOfChannels                  int32
	enablePartialResultsStabilization bool
	partialResultsStability           types.PartialResultsStability
	languageModelName                 string
	identifyLanguage                  bool
	identifyMultipleLanguages         bool
	languageOptions                   string
	preferredLanguage                 types.LanguageCode
	vocabularyNames                   string
	vocabularyFilterNames             string
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
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithAWSSTTVocabularyName(name string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.vocabularyName = name
	}
}

func WithAWSSTTSessionID(sessionID string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.sessionID = sessionID
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
		if channels > 0 {
			s.numberOfChannels = channels
		}
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
	}
}

func WithAWSSTTVocabularyFilterNames(names string) AWSSTTOption {
	return func(s *AWSSTT) {
		s.vocabularyFilterNames = names
	}
}

func NewAWSSTT(ctx context.Context, region string, providerOpts ...AWSSTTOption) (*AWSSTT, error) {
	if _, err := newAWSSTTWithClient(nil, providerOpts...); err != nil {
		return nil, err
	}

	opts := []func(*config.LoadOptions) error{}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return newAWSSTTWithClient(awsSTTSDKClient{client: transcribestreaming.NewFromConfig(cfg)}, providerOpts...)
}

func newAWSSTTWithClient(client awsSTTClient, opts ...AWSSTTOption) (*AWSSTT, error) {
	provider := &AWSSTT{
		client:     client,
		sampleRate: 24000,
		encoding:   types.MediaEncodingPcm,
		language:   types.LanguageCodeEnUs,
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
func (s *AWSSTT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return 24000
	}
	return uint32(s.sampleRate)
}
func (s *AWSSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: "word", OfflineRecognize: false}
}

func (s *AWSSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language == "" {
		language = "en-US"
	}
	input := buildAWSStartStreamTranscriptionInput(s, language)
	stream, err := s.client.StartStreamTranscription(ctx, input)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("AWS Transcribe stream start failed: %v", err))
	}

	gs := &awsSTTStream{
		stream:                   stream,
		language:                 input.LanguageCode,
		identifyLanguage:         s.identifyLanguage,
		identifyMultipleLanguage: s.identifyMultipleLanguages,
		events:                   make(chan *stt.SpeechEvent, 10),
		errCh:                    make(chan error, 1),
	}
	go gs.readLoop()

	return gs, nil
}

func buildAWSStartStreamTranscriptionInput(s *AWSSTT, language string) *transcribestreaming.StartStreamTranscriptionInput {
	languageCode := s.language
	if language != "" {
		languageCode = types.LanguageCode(language)
	}
	input := &transcribestreaming.StartStreamTranscriptionInput{
		MediaEncoding:        s.encoding,
		MediaSampleRateHertz: aws.Int32(s.sampleRate),
	}
	if s.identifyLanguage {
		input.IdentifyLanguage = true
	} else if s.identifyMultipleLanguages {
		input.IdentifyMultipleLanguages = true
	} else {
		input.LanguageCode = languageCode
	}
	if s.vocabularyName != "" {
		input.VocabularyName = aws.String(s.vocabularyName)
	}
	if s.sessionID != "" {
		input.SessionId = aws.String(s.sessionID)
	}
	if s.vocabularyFilterMethod != "" {
		input.VocabularyFilterMethod = s.vocabularyFilterMethod
	}
	if s.vocabularyFilterName != "" {
		input.VocabularyFilterName = aws.String(s.vocabularyFilterName)
	}
	input.ShowSpeakerLabel = s.showSpeakerLabel
	input.EnableChannelIdentification = s.enableChannelIdentification
	if s.numberOfChannels > 0 {
		input.NumberOfChannels = aws.Int32(s.numberOfChannels)
	}
	input.EnablePartialResultsStabilization = s.enablePartialResultsStabilization
	if s.partialResultsStability != "" {
		input.PartialResultsStability = s.partialResultsStability
	}
	if s.languageModelName != "" {
		input.LanguageModelName = aws.String(s.languageModelName)
	}
	if s.languageOptions != "" {
		input.LanguageOptions = aws.String(s.languageOptions)
	}
	if s.preferredLanguage != "" {
		input.PreferredLanguage = s.preferredLanguage
	}
	if s.vocabularyNames != "" {
		input.VocabularyNames = aws.String(s.vocabularyNames)
	}
	if s.vocabularyFilterNames != "" {
		input.VocabularyFilterNames = aws.String(s.vocabularyFilterNames)
	}
	return input
}

func (s *AWSSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	// AWS Transcribe (non-streaming) uses jobs on S3. Since we don't have S3 upload configured,
	// offline recognize is unsupported natively via simple buffer upload.
	return nil, fmt.Errorf("offline recognize is not natively supported by AWSSTT via simple upload (S3 required). Use Stream instead")
}

type awsSTTStream struct {
	stream                   awsSTTEventStream
	language                 types.LanguageCode
	identifyLanguage         bool
	identifyMultipleLanguage bool
	events                   chan *stt.SpeechEvent
	errCh                    chan error
	closed                   bool
	speaking                 bool
	timingMu                 sync.Mutex
	startTimeOffset          float64
	startTime                float64
}

func (s *awsSTTStream) readLoop() {
	defer close(s.events)
	for {
		event := <-s.stream.Events()
		if event == nil {
			if err := s.stream.Err(); err != nil {
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
			for _, result := range v.Value.Transcript.Results {
				if result.StartTime == 0 {
					s.speaking = true
					s.events <- &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}
				}

				if result.EndTime > 0 {
					eventType := stt.SpeechEventInterimTranscript
					if !result.IsPartial {
						eventType = stt.SpeechEventFinalTranscript
					}

					s.events <- &stt.SpeechEvent{
						Type: eventType,
						Alternatives: []stt.SpeechData{
							awsSpeechDataFromResultOffset(result, s.currentStartTimeOffset(), string(s.language), s.identifyLanguage || s.identifyMultipleLanguage),
						},
					}
				}
				if !result.IsPartial {
					s.events <- &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
					s.speaking = false
				}
			}
		}
	}
}

func isHarmlessAWSSTTStreamCloseError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "complete signal was sent without the preceding empty frame") ||
		strings.HasPrefix(message, "Your request timed out") ||
		strings.Contains(message, "InvalidStateError")
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
	if s.closed {
		return io.ErrClosedPipe
	}
	if err := s.stream.Send(context.Background(), &types.AudioStreamMemberAudioEvent{
		Value: types.AudioEvent{
			AudioChunk: frame.Data,
		},
	}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("AWS Transcribe audio write failed: %v", err))
	}
	return nil
}

func (s *awsSTTStream) Flush() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if err := s.stream.Send(context.Background(), &types.AudioStreamMemberAudioEvent{
		Value: types.AudioEvent{
			AudioChunk: []byte{},
		},
	}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return llm.NewAPITimeoutError(err.Error())
		}
		return llm.NewAPIConnectionError(fmt.Sprintf("AWS Transcribe audio write failed: %v", err))
	}
	return nil
}

func (s *awsSTTStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	_ = s.stream.Send(context.Background(), &types.AudioStreamMemberAudioEvent{
		Value: types.AudioEvent{
			AudioChunk: []byte{},
		},
	})
	_ = s.stream.Close()
	return nil
}

func (s *awsSTTStream) Next() (*stt.SpeechEvent, error) {
	if s.closed {
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
