package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

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

func TestAWSSTTStreamLanguageIdentificationSetsSourceLanguages(t *testing.T) {
	reader := newFakeAWSSTTReader()
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = &fakeAWSSTTWriter{}
	})
	client := &fakeAWSSTTClient{stream: stream}
	provider, err := newAWSSTTWithClient(client, WithAWSSTTIdentifyLanguage(true))
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}
	providerStream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}

	reader.events <- &types.TranscriptResultStreamMemberTranscriptEvent{
		Value: types.TranscriptEvent{
			Transcript: &types.Transcript{
				Results: []types.Result{
					{
						IsPartial:    false,
						StartTime:    0.1,
						EndTime:      0.3,
						LanguageCode: types.LanguageCodeEsUs,
						Alternatives: []types.Alternative{{
							Transcript: awsconfig.String("hola"),
						}},
					},
				},
			},
		},
	}
	close(reader.events)

	event, err := providerStream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final transcript", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %q, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Language != "es-US" {
		t.Fatalf("language = %q, want es-US", alt.Language)
	}
	if len(alt.SourceLanguages) != 1 || alt.SourceLanguages[0] != "es-US" {
		t.Fatalf("source languages = %#v, want [es-US]", alt.SourceLanguages)
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
						StartTime: 0.0,
						EndTime:   0.2,
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
		t.Fatalf("Next error = %v, want start-of-speech event", err)
	}
	if event.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("event type = %q, want start_of_speech", event.Type)
	}

	event, err = providerStream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want transcript event", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %q, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 || event.Alternatives[0].Text != "hello" {
		t.Fatalf("alternatives = %#v, want hello transcript", event.Alternatives)
	}
	if event.Alternatives[0].StartTime != 0.0 || event.Alternatives[0].EndTime != 0.2 {
		t.Fatalf("alternative timing = %v-%v, want 0-0.2", event.Alternatives[0].StartTime, event.Alternatives[0].EndTime)
	}
	if len(event.Alternatives[0].Words) != 1 || event.Alternatives[0].Words[0].Text != "hello" {
		t.Fatalf("words = %#v, want hello word timing", event.Alternatives[0].Words)
	}
	if event.Alternatives[0].Language != "en-US" {
		t.Fatalf("language = %q, want en-US", event.Alternatives[0].Language)
	}

	end, err := providerStream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want end-of-speech event", err)
	}
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("end event type = %q, want end_of_speech", end.Type)
	}

	_, err = providerStream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next EOF error = %v, want io.EOF", err)
	}
}

func TestAWSSTTStreamEmitsReferenceStartOfSpeechOncePerResultSequence(t *testing.T) {
	reader := newFakeAWSSTTReader()
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = &fakeAWSSTTWriter{}
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
						IsPartial: true,
						StartTime: 0.0,
						EndTime:   0.2,
						Alternatives: []types.Alternative{{
							Transcript: awsconfig.String("hel"),
						}},
					},
					{
						IsPartial: false,
						StartTime: 0.0,
						EndTime:   0.4,
						Alternatives: []types.Alternative{{
							Transcript: awsconfig.String("hello"),
						}},
					},
				},
			},
		},
	}
	close(reader.events)

	wantTypes := []stt.SpeechEventType{
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	}
	for i, want := range wantTypes {
		event, err := providerStream.Next()
		if err != nil {
			t.Fatalf("Next[%d] error = %v, want %s", i, err, want)
		}
		if event.Type != want {
			t.Fatalf("event[%d] type = %q, want %q", i, event.Type, want)
		}
	}
	_, err := providerStream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next EOF error = %v, want io.EOF", err)
	}
}

func TestAWSSTTStreamAppliesReferenceStartTimeOffset(t *testing.T) {
	reader := newFakeAWSSTTReader()
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = &fakeAWSSTTWriter{}
	})
	providerStream := &awsSTTStream{
		stream: stream,
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
	}
	timing, ok := interface{}(providerStream).(stt.StreamTiming)
	if !ok {
		t.Fatal("aws STT stream does not implement stt.StreamTiming")
	}
	timing.SetStartTimeOffset(2.5)
	timing.SetStartTime(123.5)
	if timing.StartTimeOffset() != 2.5 || timing.StartTime() != 123.5 {
		t.Fatalf("timing = offset %v start %v, want reference values", timing.StartTimeOffset(), timing.StartTime())
	}

	go providerStream.readLoop()
	reader.events <- &types.TranscriptResultStreamMemberTranscriptEvent{
		Value: types.TranscriptEvent{
			Transcript: &types.Transcript{
				Results: []types.Result{
					{
						IsPartial: false,
						StartTime: 0.1,
						EndTime:   0.4,
						Alternatives: []types.Alternative{
							{
								Transcript: awsconfig.String("hello"),
								Items: []types.Item{
									{
										Type:       types.ItemTypePronunciation,
										Content:    awsconfig.String("hello"),
										StartTime:  0.1,
										EndTime:    0.4,
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
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.StartTime != 2.6 || alt.EndTime != 2.9 {
		t.Fatalf("transcript timing = %v-%v, want reference start_time_offset applied", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 1 || alt.Words[0].StartTime != 2.6 || alt.Words[0].EndTime != 2.9 || alt.Words[0].StartTimeOffset != 2.5 {
		t.Fatalf("word timing = %+v, want reference start_time_offset applied", alt.Words)
	}

	assertAWSPanicsWithMessage(t, "start_time_offset must be non-negative", func() {
		timing.SetStartTimeOffset(-0.01)
	})
	if got := timing.StartTimeOffset(); got != 2.5 {
		t.Fatalf("StartTimeOffset after rejected update = %v, want 2.5", got)
	}
	assertAWSPanicsWithMessage(t, "start_time must be non-negative", func() {
		timing.SetStartTime(-0.01)
	})
	if got := timing.StartTime(); got != 123.5 {
		t.Fatalf("StartTime after rejected update = %v, want 123.5", got)
	}
}

func TestAWSSTTStreamZeroEndFinalEmitsReferenceBoundaries(t *testing.T) {
	reader := newFakeAWSSTTReader()
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = &fakeAWSSTTWriter{}
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
						StartTime: 0.0,
						EndTime:   0.0,
						Alternatives: []types.Alternative{{
							Transcript: awsconfig.String("not ready"),
						}},
					},
				},
			},
		},
	}
	close(reader.events)

	event, err := providerStream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want start-of-speech event", err)
	}
	if event.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("event type = %q, want start_of_speech", event.Type)
	}

	event, err = providerStream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want end-of-speech event", err)
	}
	if event.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("event type = %q, want end_of_speech", event.Type)
	}

	_, err = providerStream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF after zero-end boundary events", err)
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
	if len(writer.chunks) != 2 {
		t.Fatalf("chunks after Flush = %d, want audio plus empty flush sentinel", len(writer.chunks))
	}
	if len(writer.chunks[1]) != 0 {
		t.Fatalf("flush chunk = %q, want empty AWS Transcribe sentinel", string(writer.chunks[1]))
	}
	if writer.chunkWasNil[1] {
		t.Fatal("flush chunk = nil, want non-nil empty AWS Transcribe sentinel")
	}
	providerStream.errCh <- errors.New("stream failed")
	if _, err := providerStream.Next(); err == nil || !strings.Contains(err.Error(), "stream failed") {
		t.Fatalf("Next error = %v, want stream failed", err)
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if len(writer.chunks) != 3 {
		t.Fatalf("chunks after Close = %d, want audio plus flush and close sentinels", len(writer.chunks))
	}
	if len(writer.chunks[2]) != 0 {
		t.Fatalf("close chunk = %q, want empty AWS Transcribe sentinel", string(writer.chunks[2]))
	}
	if writer.chunkWasNil[2] {
		t.Fatal("close chunk = nil, want non-nil empty AWS Transcribe sentinel")
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
	if len(writer.chunks) != 3 {
		t.Fatalf("chunks after second Close = %d, want idempotent close", len(writer.chunks))
	}
	if !writer.closed || !reader.closed {
		t.Fatalf("closed writer/reader = %v/%v, want true/true", writer.closed, reader.closed)
	}
	if err := providerStream.PushFrame(&model.AudioFrame{Data: []byte("after-close")}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after close error = %v, want ErrClosedPipe", err)
	}
	if err := providerStream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after close error = %v, want ErrClosedPipe", err)
	}
}

func TestAWSSTTNextReturnsQueuedTranscriptBeforeStreamError(t *testing.T) {
	for range 64 {
		providerStream := &awsSTTStream{
			events: make(chan *stt.SpeechEvent, 1),
			errCh:  make(chan error, 1),
		}
		providerStream.events <- &stt.SpeechEvent{
			Type: stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{
				{Text: "hello"},
			},
		}
		providerStream.errCh <- errors.New("stream failed")

		event, err := providerStream.Next()
		if err != nil {
			t.Fatalf("Next error = %v, want queued transcript before stream error", err)
		}
		if event == nil || event.Type != stt.SpeechEventFinalTranscript {
			t.Fatalf("Next event = %#v, want queued final transcript", event)
		}
		if got := event.Alternatives[0].Text; got != "hello" {
			t.Fatalf("transcript = %q, want hello", got)
		}
	}
}

func TestAWSSTTClosedStreamNextReturnsEOF(t *testing.T) {
	reader := newFakeAWSSTTReader()
	writer := &fakeAWSSTTWriter{}
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = writer
	})
	providerStream := &awsSTTStream{
		stream: stream,
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error),
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	type nextResult struct {
		event *stt.SpeechEvent
		err   error
	}
	resultCh := make(chan nextResult, 1)
	go func() {
		event, err := providerStream.Next()
		resultCh <- nextResult{event: event, err: err}
	}()

	select {
	case got := <-resultCh:
		if got.event != nil {
			t.Fatalf("Next event = %#v, want nil", got.event)
		}
		if !errors.Is(got.err, io.EOF) {
			t.Fatalf("Next error = %v, want io.EOF", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Next after Close")
	}
}

type fakeAWSSTTWriter struct {
	lastChunk   []byte
	chunks      [][]byte
	chunkWasNil []bool
	closed      bool
	err         error
}

func (w *fakeAWSSTTWriter) Send(_ context.Context, event types.AudioStream) error {
	if w.err != nil {
		return w.err
	}
	audioEvent, ok := event.(*types.AudioStreamMemberAudioEvent)
	if !ok {
		return nil
	}
	w.chunkWasNil = append(w.chunkWasNil, audioEvent.Value.AudioChunk == nil)
	w.lastChunk = append([]byte(nil), audioEvent.Value.AudioChunk...)
	w.chunks = append(w.chunks, append([]byte(nil), audioEvent.Value.AudioChunk...))
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

func assertAWSPanicsWithMessage(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("function did not panic, want %q", want)
		}
		if got := fmt.Sprint(recovered); got != want {
			t.Fatalf("panic = %q, want %q", got, want)
		}
	}()
	fn()
}
