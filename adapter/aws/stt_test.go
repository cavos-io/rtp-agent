package aws

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestAWSSpeechDataFromAlternativePreservesPronunciationItems(t *testing.T) {
	alt := types.Alternative{
		Transcript: awsconfig.String("hello world"),
		Items: []types.Item{
			{
				Type:       types.ItemTypePronunciation,
				Content:    awsconfig.String("hello"),
				StartTime:  0.1,
				EndTime:    0.3,
				Confidence: awsconfig.Float64(0.94),
				Speaker:    awsconfig.String("spk_0"),
			},
			{
				Type:    types.ItemTypePunctuation,
				Content: awsconfig.String(","),
			},
			{
				Type:       types.ItemTypePronunciation,
				Content:    awsconfig.String("world"),
				StartTime:  0.4,
				EndTime:    0.8,
				Confidence: awsconfig.Float64(0.91),
				Speaker:    awsconfig.String("spk_1"),
			},
		},
	}

	data := awsSpeechDataFromAlternative(alt)
	if data.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", data.Text)
	}
	if data.Confidence != 0.94 {
		t.Fatalf("confidence = %v, want first pronunciation confidence", data.Confidence)
	}
	if len(data.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(data.Words))
	}
	if got := data.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0.94 || got.SpeakerID != "spk_0" {
		t.Fatalf("first word = %+v, want hello timing with speaker", got)
	}
	if got := data.Words[1]; got.Text != "world" || got.StartTime != 0.4 || got.EndTime != 0.8 || got.Confidence != 0.91 || got.SpeakerID != "spk_1" {
		t.Fatalf("second word = %+v, want world timing with speaker", got)
	}

	punctuationOnly := awsSpeechDataFromAlternative(types.Alternative{
		Transcript: awsconfig.String(""),
		Items: []types.Item{{
			Type:    types.ItemTypePunctuation,
			Content: awsconfig.String("."),
		}},
	})
	if punctuationOnly.Confidence != 0 {
		t.Fatalf("punctuation-only confidence = %v, want reference zero confidence", punctuationOnly.Confidence)
	}
}

func TestAWSSTTCapabilitiesAdvertiseWordAlignment(t *testing.T) {
	provider := &AWSSTT{}

	if provider.Label() != "aws.STT" {
		t.Fatalf("Label = %q, want aws.STT", provider.Label())
	}
	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}

func TestAWSSTTStreamInputDefaultsMatchReference(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.LanguageCode != types.LanguageCodeEnUs {
		t.Fatalf("language = %q, want en-US", input.LanguageCode)
	}
	if input.MediaEncoding != types.MediaEncodingPcm {
		t.Fatalf("media encoding = %q, want pcm", input.MediaEncoding)
	}
	if input.MediaSampleRateHertz == nil || *input.MediaSampleRateHertz != 24000 {
		t.Fatalf("sample rate = %v, want 24000", input.MediaSampleRateHertz)
	}
}

func TestAWSSTTExposesReferenceInputSampleRate(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	if got := provider.InputSampleRate(); got != 24000 {
		t.Fatalf("InputSampleRate = %d, want reference sample rate 24000", got)
	}
}

func TestAWSSTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil, WithAWSSTTSampleRate(8000))
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	if got := provider.InputSampleRate(); got != 8000 {
		t.Fatalf("InputSampleRate = %d, want configured sample rate 8000", got)
	}
}

func TestAWSSTTStreamInputUsesProviderOptions(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTSampleRate(8000),
		WithAWSSTTVocabularyName("support_terms"),
		WithAWSSTTShowSpeakerLabel(true),
		WithAWSSTTEnablePartialResultsStabilization(true),
		WithAWSSTTPartialResultsStability(types.PartialResultsStabilityHigh),
		WithAWSSTTLanguageModelName("support-model"),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "id-ID")

	if input.LanguageCode != types.LanguageCodeIdId {
		t.Fatalf("language = %q, want id-ID", input.LanguageCode)
	}
	if input.MediaSampleRateHertz == nil || *input.MediaSampleRateHertz != 8000 {
		t.Fatalf("sample rate = %v, want 8000", input.MediaSampleRateHertz)
	}
	if input.VocabularyName == nil || *input.VocabularyName != "support_terms" {
		t.Fatalf("vocabulary name = %v, want support_terms", input.VocabularyName)
	}
	if !input.ShowSpeakerLabel {
		t.Fatal("show speaker label = false, want true")
	}
	if !input.EnablePartialResultsStabilization {
		t.Fatal("partial stabilization = false, want true")
	}
	if input.PartialResultsStability != types.PartialResultsStabilityHigh {
		t.Fatalf("partial stability = %q, want high", input.PartialResultsStability)
	}
	if input.LanguageModelName == nil || *input.LanguageModelName != "support-model" {
		t.Fatalf("language model = %v, want support-model", input.LanguageModelName)
	}
}

func TestAWSSTTStreamInputOmitsLanguageWhenIdentifyingLanguage(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTIdentifyLanguage(true),
		WithAWSSTTLanguageOptions("en-US,id-ID"),
		WithAWSSTTPreferredLanguage(types.LanguageCodeIdId),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "en-US")

	if input.LanguageCode != "" {
		t.Fatalf("language = %q, want empty when identifying language", input.LanguageCode)
	}
	if !input.IdentifyLanguage {
		t.Fatal("identify language = false, want true")
	}
	if input.LanguageOptions == nil || *input.LanguageOptions != "en-US,id-ID" {
		t.Fatalf("language options = %v, want en-US,id-ID", input.LanguageOptions)
	}
	if input.PreferredLanguage != types.LanguageCodeIdId {
		t.Fatalf("preferred language = %q, want id-ID", input.PreferredLanguage)
	}
}

func TestAWSSTTStreamInputUsesReferenceAdvancedOptions(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTSessionID("session-123"),
		WithAWSSTTVocabularyFilterMethod(types.VocabularyFilterMethodMask),
		WithAWSSTTVocabularyFilterName("pii-filter"),
		WithAWSSTTEnableChannelIdentification(true),
		WithAWSSTTNumberOfChannels(2),
		WithAWSSTTIdentifyMultipleLanguages(true),
		WithAWSSTTLanguageOptions("en-US,es-US"),
		WithAWSSTTPreferredLanguage(types.LanguageCodeEsUs),
		WithAWSSTTVocabularyNames("support_terms,product_terms"),
		WithAWSSTTVocabularyFilterNames("pii-filter,brand-filter"),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "en-US")

	if input.LanguageCode != "" {
		t.Fatalf("language = %q, want empty when identifying multiple languages", input.LanguageCode)
	}
	if !input.IdentifyMultipleLanguages {
		t.Fatal("identify multiple languages = false, want true")
	}
	if input.SessionId == nil || *input.SessionId != "session-123" {
		t.Fatalf("session ID = %v, want session-123", input.SessionId)
	}
	if input.VocabularyFilterMethod != types.VocabularyFilterMethodMask {
		t.Fatalf("vocabulary filter method = %q, want mask", input.VocabularyFilterMethod)
	}
	if input.VocabularyFilterName == nil || *input.VocabularyFilterName != "pii-filter" {
		t.Fatalf("vocabulary filter name = %v, want pii-filter", input.VocabularyFilterName)
	}
	if !input.EnableChannelIdentification {
		t.Fatal("channel identification = false, want true")
	}
	if input.NumberOfChannels == nil || *input.NumberOfChannels != 2 {
		t.Fatalf("number of channels = %v, want 2", input.NumberOfChannels)
	}
	if input.LanguageOptions == nil || *input.LanguageOptions != "en-US,es-US" {
		t.Fatalf("language options = %v, want en-US,es-US", input.LanguageOptions)
	}
	if input.PreferredLanguage != types.LanguageCodeEsUs {
		t.Fatalf("preferred language = %q, want es-US", input.PreferredLanguage)
	}
	if input.VocabularyNames == nil || *input.VocabularyNames != "support_terms,product_terms" {
		t.Fatalf("vocabulary names = %v, want support/product terms", input.VocabularyNames)
	}
	if input.VocabularyFilterNames == nil || *input.VocabularyFilterNames != "pii-filter,brand-filter" {
		t.Fatalf("vocabulary filter names = %v, want pii/brand filters", input.VocabularyFilterNames)
	}
}

func TestAWSSTTRejectsMutuallyExclusiveLanguageDetection(t *testing.T) {
	_, err := newAWSSTTWithClient(nil,
		WithAWSSTTIdentifyLanguage(true),
		WithAWSSTTIdentifyMultipleLanguages(true),
	)

	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("newAWSSTTWithClient error = %v, want mutual exclusion error", err)
	}
}

func TestNewAWSSTTRejectsMutuallyExclusiveLanguageDetectionBeforeConfigLoad(t *testing.T) {
	_, err := NewAWSSTT(context.Background(), "us-east-1",
		WithAWSSTTIdentifyLanguage(true),
		WithAWSSTTIdentifyMultipleLanguages(true),
	)

	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("NewAWSSTT error = %v, want mutual exclusion error", err)
	}
}

func TestAWSSTTStreamStartsClientWithReferenceInput(t *testing.T) {
	reader := newFakeAWSSTTReader()
	writer := &fakeAWSSTTWriter{}
	eventStream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = writer
	})
	client := &fakeAWSSTTClient{stream: eventStream}
	provider, err := newAWSSTTWithClient(client, WithAWSSTTSampleRate(16000))
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream error = %v, want nil", err)
	}
	defer stream.Close()

	if client.input == nil {
		t.Fatal("client input = nil, want StartStreamTranscription input")
	}
	if client.input.LanguageCode != types.LanguageCodeIdId {
		t.Fatalf("language code = %q, want id-ID", client.input.LanguageCode)
	}
	if client.input.MediaSampleRateHertz == nil || *client.input.MediaSampleRateHertz != 16000 {
		t.Fatalf("sample rate = %v, want 16000", client.input.MediaSampleRateHertz)
	}
	if _, ok := stream.(*awsSTTStream); !ok {
		t.Fatalf("stream = %T, want *awsSTTStream", stream)
	}
}

func TestAWSSTTStreamReturnsClientError(t *testing.T) {
	client := &fakeAWSSTTClient{err: errors.New("start failed")}
	provider, err := newAWSSTTWithClient(client)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	_, err = provider.Stream(context.Background(), "en-US")

	if err == nil || !strings.Contains(err.Error(), "start failed") {
		t.Fatalf("Stream error = %v, want client error", err)
	}
}

func TestAWSSTTRecognizeReportsUnsupportedOfflineMode(t *testing.T) {
	provider := &AWSSTT{}

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "en-US")

	if err == nil || !strings.Contains(err.Error(), "offline recognize is not natively supported") {
		t.Fatalf("Recognize error = %v, want unsupported offline recognize error", err)
	}
}

func TestAWSSTTStreamMapsTranscriptEventsAndEOF(t *testing.T) {
	reader := newFakeAWSSTTReader()
	writer := &fakeAWSSTTWriter{}
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = writer
	})
	providerStream := &awsSTTStream{
		stream: stream,
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
	}

	go providerStream.readLoop()
	reader.events <- &types.TranscriptResultStreamMemberTranscriptEvent{
		Value: types.TranscriptEvent{
			Transcript: &types.Transcript{
				Results: []types.Result{
					{
						IsPartial: false,
						Alternatives: []types.Alternative{
							{
								Transcript: awsconfig.String("hello"),
								Items: []types.Item{
									{
										Type:       types.ItemTypePronunciation,
										Content:    awsconfig.String("hello"),
										StartTime:  0.1,
										EndTime:    0.2,
										Confidence: awsconfig.Float64(0.9),
									},
								},
							},
						},
					},
				},
			},
		},
	}
	close(reader.events)

	event, err := providerStream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want transcript event", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %q, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 || event.Alternatives[0].Text != "hello" {
		t.Fatalf("alternatives = %#v, want hello transcript", event.Alternatives)
	}
	if len(event.Alternatives[0].Words) != 1 || event.Alternatives[0].Words[0].Text != "hello" {
		t.Fatalf("words = %#v, want hello word timing", event.Alternatives[0].Words)
	}

	_, err = providerStream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next EOF error = %v, want io.EOF", err)
	}
}

func TestAWSSTTStreamPushCloseAndNextError(t *testing.T) {
	reader := newFakeAWSSTTReader()
	writer := &fakeAWSSTTWriter{}
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = writer
	})
	providerStream := &awsSTTStream{
		stream: stream,
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}

	if err := providerStream.PushFrame(&model.AudioFrame{Data: []byte("pcm")}); err != nil {
		t.Fatalf("PushFrame error = %v, want nil", err)
	}
	if string(writer.lastChunk) != "pcm" {
		t.Fatalf("last audio chunk = %q, want pcm", string(writer.lastChunk))
	}
	if err := providerStream.Flush(); err != nil {
		t.Fatalf("Flush error = %v, want nil", err)
	}
	providerStream.errCh <- errors.New("stream failed")
	if _, err := providerStream.Next(); err == nil || !strings.Contains(err.Error(), "stream failed") {
		t.Fatalf("Next error = %v, want stream failed", err)
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
	if !writer.closed || !reader.closed {
		t.Fatalf("closed writer/reader = %v/%v, want true/true", writer.closed, reader.closed)
	}
	if err := providerStream.PushFrame(&model.AudioFrame{Data: []byte("after-close")}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after close error = %v, want ErrClosedPipe", err)
	}
}

type fakeAWSSTTWriter struct {
	lastChunk []byte
	closed    bool
	err       error
}

func (w *fakeAWSSTTWriter) Send(_ context.Context, event types.AudioStream) error {
	if w.err != nil {
		return w.err
	}
	audioEvent, ok := event.(*types.AudioStreamMemberAudioEvent)
	if !ok {
		return nil
	}
	w.lastChunk = append([]byte(nil), audioEvent.Value.AudioChunk...)
	return nil
}

func (w *fakeAWSSTTWriter) Close() error {
	w.closed = true
	return nil
}

func (w *fakeAWSSTTWriter) Err() error {
	return w.err
}

type fakeAWSSTTReader struct {
	events chan types.TranscriptResultStream
	closed bool
	err    error
}

func newFakeAWSSTTReader() *fakeAWSSTTReader {
	return &fakeAWSSTTReader{events: make(chan types.TranscriptResultStream, 1)}
}

func (r *fakeAWSSTTReader) Events() <-chan types.TranscriptResultStream {
	return r.events
}

func (r *fakeAWSSTTReader) Close() error {
	r.closed = true
	return nil
}

func (r *fakeAWSSTTReader) Err() error {
	return r.err
}

type fakeAWSSTTClient struct {
	input  *transcribestreaming.StartStreamTranscriptionInput
	stream awsSTTEventStream
	err    error
}

func (c *fakeAWSSTTClient) StartStreamTranscription(_ context.Context, input *transcribestreaming.StartStreamTranscriptionInput) (awsSTTEventStream, error) {
	c.input = input
	if c.err != nil {
		return nil, c.err
	}
	return c.stream, nil
}
