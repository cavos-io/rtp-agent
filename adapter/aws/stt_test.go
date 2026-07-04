package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
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
	if len(data.Words) != 3 {
		t.Fatalf("words = %d, want pronunciation plus punctuation items", len(data.Words))
	}
	if got := data.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0.94 || got.SpeakerID != "spk_0" {
		t.Fatalf("first word = %+v, want hello timing with speaker", got)
	}
	if got := data.Words[2]; got.Text != "world" || got.StartTime != 0.4 || got.EndTime != 0.8 || got.Confidence != 0.91 || got.SpeakerID != "spk_1" {
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

func TestAWSSpeechDataFromAlternativePreservesPunctuationItems(t *testing.T) {
	data := awsSpeechDataFromAlternative(types.Alternative{
		Transcript: awsconfig.String("hello, world"),
		Items: []types.Item{
			{
				Type:       types.ItemTypePronunciation,
				Content:    awsconfig.String("hello"),
				StartTime:  0.1,
				EndTime:    0.3,
				Confidence: awsconfig.Float64(0.94),
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
			},
		},
	})

	if len(data.Words) != 3 {
		t.Fatalf("words = %d, want punctuation item preserved between words", len(data.Words))
	}
	if got := data.Words[1]; got.Text != "," || got.StartTime != 0 || got.EndTime != 0 || got.Confidence != 0 {
		t.Fatalf("punctuation word = %+v, want reference punctuation timed string", got)
	}
}

func TestAWSSpeechDataFromAlternativeUsesReferenceFirstItemConfidence(t *testing.T) {
	data := awsSpeechDataFromAlternative(types.Alternative{
		Transcript: awsconfig.String(", hello"),
		Items: []types.Item{
			{
				Type:    types.ItemTypePunctuation,
				Content: awsconfig.String(","),
			},
			{
				Type:       types.ItemTypePronunciation,
				Content:    awsconfig.String("hello"),
				StartTime:  0.1,
				EndTime:    0.3,
				Confidence: awsconfig.Float64(0.94),
			},
		},
	})

	if data.Confidence != 0 {
		t.Fatalf("confidence = %v, want first item confidence zero from punctuation", data.Confidence)
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

func TestAWSSTTModelAndProviderMatchReference(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}
	if got := stt.Model(provider); got != "unknown" {
		t.Fatalf("Model = %q, want unknown", got)
	}
	if got := stt.Provider(provider); got != "Amazon Transcribe" {
		t.Fatalf("Provider = %q, want Amazon Transcribe", got)
	}

	custom, err := newAWSSTTWithClient(nil, WithAWSSTTLanguageModelName("support-model"))
	if err != nil {
		t.Fatalf("newAWSSTTWithClient custom error = %v", err)
	}
	if got := stt.Model(custom); got != "support-model" {
		t.Fatalf("custom Model = %q, want support-model", got)
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

func TestAWSSTTStreamInputUsesReferenceConfiguredLanguage(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil, WithAWSSTTLanguage(types.LanguageCodeIdId))
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.LanguageCode != types.LanguageCodeIdId {
		t.Fatalf("language = %q, want configured id-ID", input.LanguageCode)
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

	if input.LanguageCode != types.LanguageCodeEnUs {
		t.Fatalf("language = %q, want configured en-US despite stream language argument", input.LanguageCode)
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

func TestAWSSTTStreamInputOmitsReferenceDetectionOptionsWithoutDetection(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTLanguageOptions("en-US,id-ID"),
		WithAWSSTTPreferredLanguage(types.LanguageCodeIdId),
		WithAWSSTTVocabularyNames("global-vocab"),
		WithAWSSTTVocabularyFilterNames("global-filter"),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.LanguageOptions != nil {
		t.Fatalf("language options = %v, want nil without language detection", input.LanguageOptions)
	}
	if input.PreferredLanguage != "" {
		t.Fatalf("preferred language = %q, want empty without language detection", input.PreferredLanguage)
	}
	if input.VocabularyNames != nil {
		t.Fatalf("vocabulary names = %v, want nil without language detection", input.VocabularyNames)
	}
	if input.VocabularyFilterNames != nil {
		t.Fatalf("vocabulary filter names = %v, want nil without language detection", input.VocabularyFilterNames)
	}
}

func TestAWSSTTStreamInputPreservesReferenceZeroChannelCount(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTEnableChannelIdentification(true),
		WithAWSSTTNumberOfChannels(0),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.NumberOfChannels == nil || *input.NumberOfChannels != 0 {
		t.Fatalf("number of channels = %v, want explicit reference zero", input.NumberOfChannels)
	}
}

func TestAWSSTTStreamInputPreservesReferenceEmptyVocabularyName(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTVocabularyName(""),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.VocabularyName == nil || *input.VocabularyName != "" {
		t.Fatalf("vocabulary name = %v, want explicit empty reference value", input.VocabularyName)
	}
}

func TestAWSSTTStreamInputPreservesReferenceEmptyLanguageModelName(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTLanguageModelName(""),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.LanguageModelName == nil || *input.LanguageModelName != "" {
		t.Fatalf("language model = %v, want explicit empty reference value", input.LanguageModelName)
	}
}

func TestAWSSTTStreamInputPreservesReferenceEmptyVocabularyFilterName(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTVocabularyFilterName(""),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.VocabularyFilterName == nil || *input.VocabularyFilterName != "" {
		t.Fatalf("vocabulary filter name = %v, want explicit empty reference value", input.VocabularyFilterName)
	}
}

func TestAWSSTTStreamInputPreservesReferenceEmptySessionID(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTSessionID(""),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.SessionId == nil || *input.SessionId != "" {
		t.Fatalf("session ID = %v, want explicit empty reference value", input.SessionId)
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

func TestAWSSTTStreamInputPreservesReferenceEmptyLanguageOptions(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTIdentifyLanguage(true),
		WithAWSSTTLanguageOptions(""),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.LanguageOptions == nil || *input.LanguageOptions != "" {
		t.Fatalf("language options = %v, want explicit empty reference value", input.LanguageOptions)
	}
}

func TestAWSSTTStreamInputPreservesReferenceEmptyVocabularyNames(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTIdentifyLanguage(true),
		WithAWSSTTVocabularyNames(""),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.VocabularyNames == nil || *input.VocabularyNames != "" {
		t.Fatalf("vocabulary names = %v, want explicit empty reference value", input.VocabularyNames)
	}
}

func TestAWSSTTStreamInputPreservesReferenceEmptyVocabularyFilterNames(t *testing.T) {
	provider, err := newAWSSTTWithClient(nil,
		WithAWSSTTIdentifyLanguage(true),
		WithAWSSTTVocabularyFilterNames(""),
	)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.VocabularyFilterNames == nil || *input.VocabularyFilterNames != "" {
		t.Fatalf("vocabulary filter names = %v, want explicit empty reference value", input.VocabularyFilterNames)
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

func TestNewAWSSTTUsesReferenceRegionDefaults(t *testing.T) {
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_CONFIG_FILE", t.TempDir()+"/config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", t.TempDir()+"/credentials")

	provider, err := NewAWSSTT(context.Background(), "")
	if err != nil {
		t.Fatalf("NewAWSSTT error = %v", err)
	}
	sdk, ok := provider.client.(awsSTTSDKClient)
	if !ok {
		t.Fatalf("client type = %T, want awsSTTSDKClient", provider.client)
	}
	if got := sdk.client.Options().Region; got != defaultAWSRegion {
		t.Fatalf("region = %q, want reference default %q", got, defaultAWSRegion)
	}

	t.Setenv("AWS_REGION", "us-west-2")
	provider, err = NewAWSSTT(context.Background(), "")
	if err != nil {
		t.Fatalf("NewAWSSTT with AWS_REGION error = %v", err)
	}
	sdk = provider.client.(awsSTTSDKClient)
	if got := sdk.client.Options().Region; got != "us-west-2" {
		t.Fatalf("region from env = %q, want us-west-2", got)
	}

	provider, err = NewAWSSTT(context.Background(), "eu-central-1")
	if err != nil {
		t.Fatalf("NewAWSSTT with explicit region error = %v", err)
	}
	sdk = provider.client.(awsSTTSDKClient)
	if got := sdk.client.Options().Region; got != "eu-central-1" {
		t.Fatalf("explicit region = %q, want eu-central-1", got)
	}
}

func TestAWSSTTExplicitCredentialsMatchReference(t *testing.T) {
	creds := AWSCredentials{
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		SessionToken:    "test-token",
	}
	provider, err := NewAWSSTT(context.Background(), "us-west-2", WithAWSSTTCredentials(creds))
	if err != nil {
		t.Fatalf("NewAWSSTT error = %v, want nil with explicit credentials", err)
	}
	if !provider.credentialsSet {
		t.Fatal("credentialsSet = false, want explicit credentials stored")
	}
	if provider.credentials != creds {
		t.Fatalf("credentials = %#v, want %#v", provider.credentials, creds)
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
	if client.input.LanguageCode != types.LanguageCodeEnUs {
		t.Fatalf("language code = %q, want configured en-US despite stream language argument", client.input.LanguageCode)
	}
	if client.input.MediaSampleRateHertz == nil || *client.input.MediaSampleRateHertz != 16000 {
		t.Fatalf("sample rate = %v, want 16000", client.input.MediaSampleRateHertz)
	}
	awsStream, ok := stream.(*awsSTTStream)
	if !ok {
		t.Fatalf("stream = %T, want *awsSTTStream", stream)
	}
	if awsStream.language != types.LanguageCodeEnUs {
		t.Fatalf("stream fallback language = %q, want configured en-US", awsStream.language)
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
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
}

func TestAWSSTTStreamRequiresConfiguredClient(t *testing.T) {
	provider := &AWSSTT{
		language:   types.LanguageCodeEnUs,
		sampleRate: 24000,
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Stream panic = %v, want APIConnectionError", r)
		}
	}()

	stream, err := provider.Stream(context.Background(), "en-US")

	if stream != nil {
		t.Fatalf("Stream = %#v, want nil without client", stream)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Stream error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "client is not configured") {
		t.Fatalf("Stream error = %v, want configured-client context", err)
	}
}

func TestAWSSTTStreamReturnsAPITimeoutErrorOnDeadline(t *testing.T) {
	client := &fakeAWSSTTClient{err: context.DeadlineExceeded}
	provider, err := newAWSSTTWithClient(client)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	stream, err := provider.Stream(context.Background(), "en-US")

	if stream != nil {
		t.Fatalf("Stream = %#v, want nil on timeout", stream)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Stream error = %T %v, want APITimeoutError", err, err)
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

func TestAWSSTTStreamPreservesReferenceQueuedTranscriptBurst(t *testing.T) {
	reader := &fakeAWSSTTReader{events: make(chan types.TranscriptResultStream, 16)}
	writer := &fakeAWSSTTWriter{}
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = writer
	})
	client := &fakeAWSSTTClient{stream: stream}
	provider, err := newAWSSTTWithClient(client)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}
	recognizeStream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	providerStream := recognizeStream.(*awsSTTStream)

	const transcripts = 12
	for i := 0; i < transcripts; i++ {
		reader.events <- &types.TranscriptResultStreamMemberTranscriptEvent{
			Value: types.TranscriptEvent{
				Transcript: &types.Transcript{
					Results: []types.Result{
						{
							IsPartial: true,
							StartTime: float64(i) + 0.1,
							EndTime:   float64(i) + 0.2,
							Alternatives: []types.Alternative{
								{Transcript: awsconfig.String(fmt.Sprintf("burst-%02d", i))},
							},
						},
					},
				},
			},
		}
	}
	close(reader.events)

	deadline := time.After(150 * time.Millisecond)
	for {
		provider.mu.Lock()
		active := len(provider.streams)
		provider.mu.Unlock()
		if active == 0 {
			break
		}
		select {
		case <-deadline:
			_ = providerStream.Close()
			t.Fatalf("AWS STT readLoop still active behind %d queued transcripts; want reference-style unbounded transcript delivery", transcripts)
		case <-time.After(time.Millisecond):
		}
	}

	for i := 0; i < transcripts; i++ {
		ev, err := providerStream.Next()
		if err != nil {
			t.Fatalf("Next transcript %d error = %v", i, err)
		}
		want := fmt.Sprintf("burst-%02d", i)
		if ev.Type != stt.SpeechEventInterimTranscript || len(ev.Alternatives) != 1 || ev.Alternatives[0].Text != want {
			t.Fatalf("event %d = %#v, want interim transcript %q", i, ev, want)
		}
	}
	if ev, err := providerStream.Next(); err != io.EOF || ev != nil {
		t.Fatalf("Next EOF = (%#v, %v), want nil EOF", ev, err)
	}
}

func TestAWSSTTStreamIgnoresReferenceNilTranscriptEvent(t *testing.T) {
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
		Value: types.TranscriptEvent{},
	}
	close(reader.events)

	event, err := providerStream.Next()
	if event != nil {
		t.Fatalf("Next event = %#v, want nil for ignored nil transcript", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF after ignored nil transcript", err)
	}
}

func TestAWSSTTStreamEmitsReferenceStartOfSpeechForEachZeroStartResult(t *testing.T) {
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
		stt.SpeechEventStartOfSpeech,
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
	if len(writer.chunks) != 1 {
		t.Fatalf("chunks after Flush = %d, want no provider close sentinel until Close", len(writer.chunks))
	}
	providerStream.errCh <- errors.New("stream failed")
	if _, err := providerStream.Next(); err == nil || !strings.Contains(err.Error(), "stream failed") {
		t.Fatalf("Next error = %v, want stream failed", err)
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if len(writer.chunks) != 2 {
		t.Fatalf("chunks after Close = %d, want audio plus close sentinel", len(writer.chunks))
	}
	if len(writer.chunks[1]) != 0 {
		t.Fatalf("close chunk = %q, want empty AWS Transcribe sentinel", string(writer.chunks[1]))
	}
	if writer.chunkWasNil[1] {
		t.Fatal("close chunk = nil, want non-nil empty AWS Transcribe sentinel")
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
	if len(writer.chunks) != 2 {
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

func TestAWSSTTStreamCloseUnblocksInFlightReferencePushFrame(t *testing.T) {
	reader := newFakeAWSSTTReader()
	writer := newBlockingAWSSTTWriter()
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = writer
	})
	providerStream := &awsSTTStream{
		stream: stream,
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- providerStream.PushFrame(&model.AudioFrame{Data: []byte("pcm")})
	}()

	select {
	case <-writer.sendStarted:
	case <-time.After(time.Second):
		t.Fatal("PushFrame did not start provider send")
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("in-flight PushFrame error = %v, want nil for reference close cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight PushFrame remained blocked after Close")
	}
}

func TestAWSSTTStreamPushNilFrameIsReferenceNoop(t *testing.T) {
	reader := newFakeAWSSTTReader()
	writer := &fakeAWSSTTWriter{}
	providerStream := &awsSTTStream{
		stream: transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
			es.Reader = reader
			es.Writer = writer
		}),
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}

	if err := providerStream.PushFrame(nil); err != nil {
		t.Fatalf("PushFrame(nil) error = %v, want nil", err)
	}
	if len(writer.chunks) != 0 {
		t.Fatalf("chunks after PushFrame(nil) = %#v, want no provider audio", writer.chunks)
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if len(writer.chunks) != 1 || len(writer.chunks[0]) != 0 {
		t.Fatalf("chunks after Close = %#v, want one empty AWS close sentinel", writer.chunks)
	}
}

func TestAWSSTTStreamRejectsReferenceSampleRateChange(t *testing.T) {
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

	if err := providerStream.PushFrame(&model.AudioFrame{
		Data:       []byte("pcm-24k"),
		SampleRate: 24000,
	}); err != nil {
		t.Fatalf("first PushFrame error = %v, want nil", err)
	}

	err := providerStream.PushFrame(&model.AudioFrame{
		Data:       []byte("pcm-16k"),
		SampleRate: 16000,
	})

	if err == nil || !strings.Contains(err.Error(), "sample rate of the input frames must be consistent") {
		t.Fatalf("second PushFrame error = %v, want reference sample-rate consistency error", err)
	}
	if len(writer.chunks) != 1 {
		t.Fatalf("provider chunks = %d, want only first frame written", len(writer.chunks))
	}
	if string(writer.chunks[0]) != "pcm-24k" {
		t.Fatalf("provider chunk = %q, want first frame only", string(writer.chunks[0]))
	}

	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	err = providerStream.PushFrame(&model.AudioFrame{
		Data:       []byte("after-close"),
		SampleRate: 16000,
	})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after close error = %v, want ErrClosedPipe before sample-rate check", err)
	}
}

func TestAWSSTTStreamFlushDoesNotSendReferenceCloseSentinel(t *testing.T) {
	reader := newFakeAWSSTTReader()
	writer := &fakeAWSSTTWriter{}
	providerStream := &awsSTTStream{
		stream: transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
			es.Reader = reader
			es.Writer = writer
		}),
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}

	if err := providerStream.Flush(); err != nil {
		t.Fatalf("Flush error = %v, want nil", err)
	}
	if len(writer.chunks) != 0 {
		t.Fatalf("chunks after Flush = %#v, want no AWS close sentinel", writer.chunks)
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if len(writer.chunks) != 1 || len(writer.chunks[0]) != 0 {
		t.Fatalf("chunks after Close = %#v, want one empty AWS close sentinel", writer.chunks)
	}
}

func TestAWSSTTStreamEndInputSendsReferenceSentinelAndKeepsOutputOpen(t *testing.T) {
	reader := newFakeAWSSTTReader()
	writer := &fakeAWSSTTWriter{}
	providerStream := &awsSTTStream{
		stream: transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
			es.Reader = reader
			es.Writer = writer
		}),
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
	}
	ending, ok := interface{}(providerStream).(stt.InputEnding)
	if !ok {
		t.Fatal("aws STT stream does not implement stt.InputEnding")
	}

	go providerStream.readLoop()
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v, want nil", err)
	}
	if len(writer.chunks) != 1 || len(writer.chunks[0]) != 0 {
		t.Fatalf("chunks after EndInput = %#v, want one empty AWS close sentinel", writer.chunks)
	}
	if writer.chunkWasNil[0] {
		t.Fatal("EndInput chunk = nil, want non-nil empty AWS close sentinel")
	}
	if !writer.closed {
		t.Fatal("writer closed = false, want provider input side closed")
	}
	if reader.closed {
		t.Fatal("reader closed = true, want output side open for final transcripts after EndInput")
	}
	if err := providerStream.PushFrame(&model.AudioFrame{Data: []byte("after-end")}); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("PushFrame after EndInput error = %v, want stream input ended", err)
	}
	if err := providerStream.Flush(); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("Flush after EndInput error = %v, want stream input ended", err)
	}
	if err := ending.EndInput(); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("second EndInput error = %v, want stream input ended", err)
	}

	reader.events <- &types.TranscriptResultStreamMemberTranscriptEvent{
		Value: types.TranscriptEvent{
			Transcript: &types.Transcript{
				Results: []types.Result{{
					IsPartial: false,
					StartTime: 0.1,
					EndTime:   0.4,
					Alternatives: []types.Alternative{{
						Transcript: awsconfig.String("final after end"),
					}},
				}},
			},
		},
	}
	close(reader.events)

	event, err := providerStream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final transcript after EndInput", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 || event.Alternatives[0].Text != "final after end" {
		t.Fatalf("event = %#v, want final transcript after EndInput", event)
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close after EndInput error = %v, want nil", err)
	}
	if len(writer.chunks) != 1 {
		t.Fatalf("chunks after Close following EndInput = %#v, want no duplicate AWS close sentinel", writer.chunks)
	}
}

func TestAWSSTTStreamCloseSuppressesReferenceWriterCloseError(t *testing.T) {
	writer := &fakeAWSSTTWriter{closeErr: errors.New("transcribe close failed")}
	providerStream := &awsSTTStream{
		stream: transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
			es.Reader = newFakeAWSSTTReader()
			es.Writer = writer
		}),
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}

	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil for reference cleanup suppression", err)
	}
	if !writer.closed {
		t.Fatal("writer closed = false, want close attempted")
	}
	if len(writer.chunks) != 1 || len(writer.chunks[0]) != 0 {
		t.Fatalf("close chunks = %#v, want one empty sentinel before close", writer.chunks)
	}
}

func TestAWSSTTProviderCloseClosesActiveStreams(t *testing.T) {
	writer := &fakeAWSSTTWriter{}
	client := &fakeAWSSTTClient{
		stream: transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
			es.Reader = newFakeAWSSTTReader()
			es.Writer = writer
		}),
	}
	provider, err := newAWSSTTWithClient(client)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}
	providerStream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !writer.closed {
		t.Fatal("writer closed = false after provider Close")
	}
	if len(writer.chunks) != 1 || len(writer.chunks[0]) != 0 {
		t.Fatalf("close chunks = %#v, want one empty sentinel before close", writer.chunks)
	}
	if err := providerStream.PushFrame(&model.AudioFrame{Data: []byte("after-close")}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after provider Close error = %v, want ErrClosedPipe", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("second Close error = %v", err)
	}
	if len(writer.chunks) != 1 {
		t.Fatalf("close chunks after second Close = %#v, want unchanged", writer.chunks)
	}
}

func TestAWSSTTStreamAfterCloseIsRejected(t *testing.T) {
	client := &fakeAWSSTTClient{}
	provider, err := newAWSSTTWithClient(client)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stream, err := provider.Stream(context.Background(), "")
	if stream != nil {
		t.Fatalf("Stream after Close = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want ErrClosedPipe", err)
	}
	if calls := atomic.LoadInt32(&client.calls); calls != 0 {
		t.Fatalf("client calls after Stream on closed provider = %d, want 0", calls)
	}
}

func TestAWSSTTStreamWriteFailureReturnsAPIConnectionError(t *testing.T) {
	writer := &fakeAWSSTTWriter{err: errors.New("transcribe write failed")}
	providerStream := &awsSTTStream{
		stream: transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
			es.Reader = newFakeAWSSTTReader()
			es.Writer = writer
		}),
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}

	err := providerStream.PushFrame(&model.AudioFrame{Data: []byte("pcm")})

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("PushFrame error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "AWS Transcribe audio write failed") {
		t.Fatalf("PushFrame error = %q, want write failure context", err.Error())
	}

	err = providerStream.Flush()
	if err != nil {
		t.Fatalf("Flush error = %v, want nil because reference FlushSentinel does not write to AWS", err)
	}
}

func TestAWSSTTStreamWriteDeadlineReturnsAPITimeoutError(t *testing.T) {
	writer := &fakeAWSSTTWriter{err: context.DeadlineExceeded}
	providerStream := &awsSTTStream{
		stream: transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
			es.Reader = newFakeAWSSTTReader()
			es.Writer = writer
		}),
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}

	err := providerStream.PushFrame(&model.AudioFrame{Data: []byte("pcm")})
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("PushFrame error = %T %v, want APITimeoutError", err, err)
	}

	err = providerStream.Flush()
	if err != nil {
		t.Fatalf("Flush error = %v, want nil because reference FlushSentinel does not write to AWS", err)
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

func TestAWSSTTNextDrainsReferenceQueuedStreamTranscriptBeforeError(t *testing.T) {
	event := &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: "queued final"},
		},
	}
	eventStream := &awsRealtimeQueuedStream[*stt.SpeechEvent]{
		out:   make(chan *stt.SpeechEvent),
		wake:  make(chan struct{}, 1),
		queue: []*stt.SpeechEvent{event},
	}
	providerStream := &awsSTTStream{
		events:      eventStream.Chan(),
		eventStream: eventStream,
		errCh:       make(chan error, 1),
	}
	providerStream.errCh <- errors.New("stream failed")

	got, err := providerStream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want queued transcript before stream error", err)
	}
	if got == nil || got.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("Next event = %#v, want queued final transcript", got)
	}
	if got.Alternatives[0].Text != "queued final" {
		t.Fatalf("transcript = %q, want queued final", got.Alternatives[0].Text)
	}
}

func TestAWSSTTProviderStreamErrorReturnsAPIConnectionError(t *testing.T) {
	reader := newFakeAWSSTTReader()
	reader.err = errors.New("transcribe stream reset")
	close(reader.events)
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
	go providerStream.readLoop()

	event, err := providerStream.Next()

	if event != nil {
		t.Fatalf("Next event = %#v, want nil", event)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "AWS Transcribe stream failed") {
		t.Fatalf("Next error = %q, want stream failure context", err.Error())
	}
}

func TestAWSSTTProviderStreamDeadlineReturnsAPITimeoutError(t *testing.T) {
	reader := newFakeAWSSTTReader()
	reader.err = context.DeadlineExceeded
	close(reader.events)
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
	go providerStream.readLoop()

	event, err := providerStream.Next()

	if event != nil {
		t.Fatalf("Next event = %#v, want nil", event)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestAWSSTTEmptyFrameCloseDiagnosticReturnsEOF(t *testing.T) {
	reader := newFakeAWSSTTReader()
	reader.err = errors.New("complete signal was sent without the preceding empty frame")
	close(reader.events)
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = &fakeAWSSTTWriter{}
	})
	providerStream := &awsSTTStream{
		stream: stream,
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}
	go providerStream.readLoop()

	event, err := providerStream.Next()

	if event != nil {
		t.Fatalf("Next event = %#v, want nil", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF for reference harmless empty-frame diagnostic", err)
	}
}

func TestAWSSTTRestartsReferenceStreamAfterIdleTimeout(t *testing.T) {
	firstReader := newFakeAWSSTTReader()
	firstReader.err = errors.New("Your request timed out because no new audio was received")
	close(firstReader.events)
	firstWriter := &fakeAWSSTTWriter{}
	firstStream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = firstReader
		es.Writer = firstWriter
	})
	secondReader := newFakeAWSSTTReader()
	secondWriter := &fakeAWSSTTWriter{}
	secondStream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = secondReader
		es.Writer = secondWriter
	})
	client := &fakeAWSSTTClient{streams: []awsSTTEventStream{firstStream, secondStream}}
	provider, err := newAWSSTTWithClient(client)
	if err != nil {
		t.Fatalf("newAWSSTTWithClient error = %v", err)
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	deadline := time.After(time.Second)
	for atomic.LoadInt32(&client.calls) < 2 {
		select {
		case <-deadline:
			t.Fatalf("StartStreamTranscription calls = %d, want restart after idle timeout", atomic.LoadInt32(&client.calls))
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("after-timeout")}); err != nil {
		t.Fatalf("PushFrame after timeout restart error = %v", err)
	}
	if string(firstWriter.lastChunk) == "after-timeout" {
		t.Fatalf("first stream received post-timeout audio, want restarted stream")
	}
	if !firstWriter.closed {
		t.Fatal("first stream writer closed = false, want closed during reference timeout restart cleanup")
	}
	if len(firstWriter.chunks) == 0 || len(firstWriter.chunks[len(firstWriter.chunks)-1]) != 0 {
		t.Fatalf("first stream close chunks = %#v, want empty sentinel before restart cleanup", firstWriter.chunks)
	}
	if string(secondWriter.lastChunk) != "after-timeout" {
		t.Fatalf("second stream last chunk = %q, want post-timeout audio", string(secondWriter.lastChunk))
	}
}

func TestAWSSTTRestartAfterInputEndedClosesReferenceInput(t *testing.T) {
	firstReader := newFakeAWSSTTReader()
	firstReader.err = errors.New("Your request timed out because no new audio was received")
	close(firstReader.events)
	firstWriter := &fakeAWSSTTWriter{}
	firstStream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = firstReader
		es.Writer = firstWriter
	})
	secondReader := newFakeAWSSTTReader()
	secondWriter := &fakeAWSSTTWriter{}
	secondStream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = secondReader
		es.Writer = secondWriter
	})
	providerStream := &awsSTTStream{
		stream:  firstStream,
		restart: func() (awsSTTEventStream, error) { return secondStream, nil },
		events:  make(chan *stt.SpeechEvent),
		errCh:   make(chan error, 1),
	}
	ending, ok := interface{}(providerStream).(stt.InputEnding)
	if !ok {
		t.Fatal("aws STT stream does not implement stt.InputEnding")
	}

	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	go providerStream.readLoop()
	defer providerStream.Close()
	defer close(secondReader.events)

	deadline := time.After(time.Second)
	for !secondWriter.isClosed() {
		select {
		case <-deadline:
			t.Fatalf("second stream writer closed = false, chunks = %#v; want reference restart to send empty close sentinel for already-ended input", secondWriter.snapshotChunks())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	chunks, chunkWasNil := secondWriter.snapshotChunksAndNilFlags()
	if len(chunks) != 1 || len(chunks[0]) != 0 || chunkWasNil[0] {
		t.Fatalf("second stream chunks = %#v nil=%#v, want one non-nil empty close sentinel", chunks, chunkWasNil)
	}
}

func TestAWSSTTInvalidStateErrorReturnsEOF(t *testing.T) {
	reader := newFakeAWSSTTReader()
	reader.err = errors.New("concurrent.futures.InvalidStateError: invalid state")
	close(reader.events)
	stream := transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
		es.Reader = reader
		es.Writer = &fakeAWSSTTWriter{}
	})
	providerStream := &awsSTTStream{
		stream: stream,
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}
	go providerStream.readLoop()

	event, err := providerStream.Next()

	if event != nil {
		t.Fatalf("Next event = %#v, want nil", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF for reference InvalidStateError cleanup", err)
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

func TestAWSSTTReadLoopUnblocksWhenClosedWithFullEventQueue(t *testing.T) {
	reader := newFakeAWSSTTReader()
	writer := &fakeAWSSTTWriter{}
	providerStream := &awsSTTStream{
		stream: transcribestreaming.NewStartStreamTranscriptionEventStream(func(es *transcribestreaming.StartStreamTranscriptionEventStream) {
			es.Reader = reader
			es.Writer = writer
		}),
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error, 1),
	}
	done := make(chan struct{})
	go func() {
		providerStream.readLoop()
		close(done)
	}()

	reader.events <- &types.TranscriptResultStreamMemberTranscriptEvent{
		Value: types.TranscriptEvent{
			Transcript: &types.Transcript{
				Results: []types.Result{{
					StartTime: 0,
					EndTime:   0.4,
					IsPartial: true,
					Alternatives: []types.Alternative{{
						Transcript: awsconfig.String("queued"),
					}},
				}},
			},
		},
	}

	select {
	case <-done:
		t.Fatal("readLoop returned before close; want blocked on full event queue")
	case <-time.After(20 * time.Millisecond):
	}

	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("readLoop did not unblock after Close with full event queue")
	}
}

type fakeAWSSTTWriter struct {
	mu          sync.Mutex
	lastChunk   []byte
	chunks      [][]byte
	chunkWasNil []bool
	closed      bool
	err         error
	closeErr    error
}

type blockingAWSSTTWriter struct {
	sendStarted   chan struct{}
	closeCh       chan struct{}
	sendStartOnce sync.Once
}

func newBlockingAWSSTTWriter() *blockingAWSSTTWriter {
	return &blockingAWSSTTWriter{
		sendStarted: make(chan struct{}),
		closeCh:     make(chan struct{}),
	}
}

func (w *blockingAWSSTTWriter) Send(ctx context.Context, event types.AudioStream) error {
	audioEvent, ok := event.(*types.AudioStreamMemberAudioEvent)
	if !ok || len(audioEvent.Value.AudioChunk) == 0 {
		return nil
	}
	w.sendStartOnce.Do(func() {
		close(w.sendStarted)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.closeCh:
		return io.ErrClosedPipe
	}
}

func (w *blockingAWSSTTWriter) Close() error {
	select {
	case <-w.closeCh:
	default:
		close(w.closeCh)
	}
	return nil
}

func (w *blockingAWSSTTWriter) Err() error { return nil }

func (w *fakeAWSSTTWriter) Send(_ context.Context, event types.AudioStream) error {
	if w.err != nil {
		return w.err
	}
	audioEvent, ok := event.(*types.AudioStreamMemberAudioEvent)
	if !ok {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chunkWasNil = append(w.chunkWasNil, audioEvent.Value.AudioChunk == nil)
	w.lastChunk = append([]byte(nil), audioEvent.Value.AudioChunk...)
	w.chunks = append(w.chunks, append([]byte(nil), audioEvent.Value.AudioChunk...))
	return nil
}

func (w *fakeAWSSTTWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return w.closeErr
}

func (w *fakeAWSSTTWriter) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func (w *fakeAWSSTTWriter) snapshotChunks() [][]byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return cloneByteSlices(w.chunks)
}

func (w *fakeAWSSTTWriter) snapshotChunksAndNilFlags() ([][]byte, []bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return cloneByteSlices(w.chunks), append([]bool(nil), w.chunkWasNil...)
}

func cloneByteSlices(values [][]byte) [][]byte {
	cloned := make([][]byte, len(values))
	for i, value := range values {
		cloned[i] = append([]byte(nil), value...)
	}
	return cloned
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
	input   *transcribestreaming.StartStreamTranscriptionInput
	stream  awsSTTEventStream
	streams []awsSTTEventStream
	err     error
	calls   int32
}

func (c *fakeAWSSTTClient) StartStreamTranscription(_ context.Context, input *transcribestreaming.StartStreamTranscriptionInput) (awsSTTEventStream, error) {
	atomic.AddInt32(&c.calls, 1)
	c.input = input
	if c.err != nil {
		return nil, c.err
	}
	if len(c.streams) > 0 {
		stream := c.streams[0]
		c.streams = c.streams[1:]
		return stream, nil
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
