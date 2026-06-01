package aws

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
)

type AWSSTT struct {
	client                            *transcribestreaming.Client
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

func WithAWSSTTShowSpeakerLabel(show bool) AWSSTTOption {
	return func(s *AWSSTT) {
		s.showSpeakerLabel = show
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
		if identify {
			s.identifyMultipleLanguages = false
		}
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

func NewAWSSTT(ctx context.Context, region string, providerOpts ...AWSSTTOption) (*AWSSTT, error) {
	opts := []func(*config.LoadOptions) error{}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return newAWSSTTWithClient(transcribestreaming.NewFromConfig(cfg), providerOpts...), nil
}

func newAWSSTTWithClient(client *transcribestreaming.Client, opts ...AWSSTTOption) *AWSSTT {
	provider := &AWSSTT{
		client:     client,
		sampleRate: 24000,
		encoding:   types.MediaEncodingPcm,
		language:   types.LanguageCodeEnUs,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *AWSSTT) Label() string { return "aws.STT" }
func (s *AWSSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: "word", OfflineRecognize: false}
}

func (s *AWSSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language == "" {
		language = "en-US"
	}
	out, err := s.client.StartStreamTranscription(ctx, buildAWSStartStreamTranscriptionInput(s, language))
	if err != nil {
		return nil, err
	}

	gs := &awsSTTStream{
		stream: out.GetStream(),
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
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
	stream *transcribestreaming.StartStreamTranscriptionEventStream
	events chan *stt.SpeechEvent
	errCh  chan error
	closed bool
}

func (s *awsSTTStream) readLoop() {
	defer close(s.events)
	for {
		event := <-s.stream.Events()
		if event == nil {
			if err := s.stream.Err(); err != nil {
				if err != io.EOF {
					s.errCh <- err
				}
			}
			return
		}

		switch v := event.(type) {
		case *types.TranscriptResultStreamMemberTranscriptEvent:
			for _, result := range v.Value.Transcript.Results {
				if len(result.Alternatives) == 0 {
					continue
				}

				alt := result.Alternatives[0]
				eventType := stt.SpeechEventInterimTranscript
				if !result.IsPartial {
					eventType = stt.SpeechEventFinalTranscript
				}

				s.events <- &stt.SpeechEvent{
					Type: eventType,
					Alternatives: []stt.SpeechData{
						awsSpeechDataFromAlternative(alt),
					},
				}
			}
		}
	}
}

func awsSpeechDataFromAlternative(alt types.Alternative) stt.SpeechData {
	return stt.SpeechData{
		Text:       aws.ToString(alt.Transcript),
		Confidence: 1.0, // Confidence is not uniformly provided at the top level.
		Words:      awsTimedStrings(alt.Items),
	}
}

func awsTimedStrings(items []types.Item) []stt.TimedString {
	if len(items) == 0 {
		return nil
	}

	words := make([]stt.TimedString, 0, len(items))
	for _, item := range items {
		if item.Type != types.ItemTypePronunciation {
			continue
		}
		words = append(words, stt.TimedString{
			Text:       aws.ToString(item.Content),
			StartTime:  item.StartTime,
			EndTime:    item.EndTime,
			Confidence: aws.ToFloat64(item.Confidence),
			SpeakerID:  aws.ToString(item.Speaker),
		})
	}
	return words
}

func (s *awsSTTStream) PushFrame(frame *model.AudioFrame) error {
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.stream.Send(context.Background(), &types.AudioStreamMemberAudioEvent{
		Value: types.AudioEvent{
			AudioChunk: frame.Data,
		},
	})
}

func (s *awsSTTStream) Flush() error {
	return nil
}

func (s *awsSTTStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.stream.Close()
}

func (s *awsSTTStream) Next() (*stt.SpeechEvent, error) {
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
		return nil, err
	}
}
