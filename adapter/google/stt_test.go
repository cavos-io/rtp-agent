package google

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"testing"
	"time"

	"cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestGoogleRecognitionConfigRequestsWordDetails(t *testing.T) {
	provider := newGoogleSTTWithClient(nil)
	config := googleRecognitionConfig(provider, "id-ID")

	if config.LanguageCode != "id-ID" {
		t.Fatalf("language = %q, want id-ID", config.LanguageCode)
	}
	if config.Model != "latest_long" {
		t.Fatalf("model = %q, want latest_long", config.Model)
	}
	if config.AudioChannelCount != 1 {
		t.Fatalf("audio channel count = %d, want reference mono channel", config.AudioChannelCount)
	}
	if !config.EnableAutomaticPunctuation {
		t.Fatal("expected automatic punctuation to be enabled")
	}
	if !config.EnableWordTimeOffsets {
		t.Fatal("expected word time offsets to be enabled")
	}
	if config.EnableWordConfidence {
		t.Fatal("word confidence enabled = true, want false by default")
	}
}

func TestGoogleRecognitionConfigEnablesWordConfidenceWhenConfigured(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTWordConfidence(true))
	config := googleRecognitionConfig(provider, "en-US")

	if !config.EnableWordConfidence {
		t.Fatal("word confidence enabled = false, want true when configured")
	}
}

func TestGoogleSpeechDataFromAlternativePreservesWords(t *testing.T) {
	alt := &speechpb.SpeechRecognitionAlternative{
		Transcript: "hello world",
		Confidence: 0.87,
		Words: []*speechpb.WordInfo{
			{
				Word:         "hello",
				StartTime:    durationpb.New(100000000),
				EndTime:      durationpb.New(300000000),
				Confidence:   0.91,
				SpeakerLabel: "agent",
			},
			{
				Word:         "world",
				StartTime:    durationpb.New(400000000),
				EndTime:      durationpb.New(800000000),
				Confidence:   0.83,
				SpeakerLabel: "speaker-2",
			},
		},
	}

	data := googleSpeechDataFromAlternative(alt)
	if data.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", data.Text)
	}
	if math.Abs(data.Confidence-0.87) > 0.000001 {
		t.Fatalf("confidence = %v, want 0.87", data.Confidence)
	}
	if len(data.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(data.Words))
	}
	if got := data.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || math.Abs(got.Confidence-0.91) > 0.000001 || got.SpeakerID != "agent" {
		t.Fatalf("first word = %+v, want hello timing with speaker label", got)
	}
	if got := data.Words[1]; got.Text != "world" || got.StartTime != 0.4 || got.EndTime != 0.8 || math.Abs(got.Confidence-0.83) > 0.000001 || got.SpeakerID != "speaker-2" {
		t.Fatalf("second word = %+v, want world timing with speaker label", got)
	}
}

func TestGoogleClientOptionsFromCredentialsFile(t *testing.T) {
	emptyOpts, err := googleClientOptionsFromCredentialsFile("")
	if err != nil {
		t.Fatalf("empty credentials returned error: %v", err)
	}
	if len(emptyOpts) != 0 {
		t.Fatalf("empty credentials options = %d, want 0", len(emptyOpts))
	}

	fileOpts, err := googleClientOptionsFromCredentialsFile("/path/to/service-account.json")
	if err != nil {
		t.Fatalf("credentials file returned error: %v", err)
	}
	if len(fileOpts) != 1 {
		t.Fatalf("credentials file options = %d, want 1", len(fileOpts))
	}
}

func TestGoogleSTTLocationOptionMatchesReferenceEndpoint(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTLocation("europe-west1"))

	if got := googleSTTEndpoint(provider); got != "europe-west1-speech.googleapis.com" {
		t.Fatalf("endpoint = %q, want europe-west1-speech.googleapis.com", got)
	}

	globalProvider := newGoogleSTTWithClient(nil, WithGoogleSTTLocation("global"))
	if got := googleSTTEndpoint(globalProvider); got != "" {
		t.Fatalf("global endpoint = %q, want empty default endpoint", got)
	}
}

func TestGoogleSTTStreamUsesConfiguredReferenceLanguage(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(
		&fakeGoogleSpeechClient{stream: streamClient},
		WithGoogleSTTLanguage("id-ID"),
	)

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	config := streamClient.sent[0].GetStreamingConfig().GetConfig()
	if config.GetLanguageCode() != "id-ID" {
		t.Fatalf("language = %q, want id-ID", config.GetLanguageCode())
	}
}

func TestGoogleSpeechDataFromAlternativeToleratesMissingWordTimes(t *testing.T) {
	alt := &speechpb.SpeechRecognitionAlternative{
		Transcript: "hello",
		Words: []*speechpb.WordInfo{
			{Word: "hello"},
		},
	}

	data := googleSpeechDataFromAlternative(alt)
	if len(data.Words) != 1 {
		t.Fatalf("words = %d, want 1", len(data.Words))
	}
	if got := data.Words[0]; got.Text != "hello" || got.StartTime != 0 || got.EndTime != 0 {
		t.Fatalf("word = %+v, want hello with zero-valued missing times", got)
	}
}

func TestGoogleSTTCapabilitiesAdvertiseWordAlignment(t *testing.T) {
	provider := newGoogleSTTWithClient(nil)

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}

func TestGoogleSTTStreamingCapabilityMatchesReferenceOption(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTStreaming(false))

	capabilities := provider.Capabilities()
	if capabilities.Streaming {
		t.Fatal("Streaming capability = true, want false from reference use_streaming option")
	}
	if capabilities.AlignedTranscript != "" {
		t.Fatalf("AlignedTranscript = %q, want empty when streaming disabled", capabilities.AlignedTranscript)
	}
}

func TestGoogleSTTChirp3CapabilitiesDisableWordAlignment(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTModel("chirp_3"))

	if got := provider.Capabilities().AlignedTranscript; got != "" {
		t.Fatalf("AlignedTranscript = %q, want empty for chirp_3", got)
	}
}

func TestGoogleRecognitionConfigDisablesReferenceWordTimeOffsetsWhenConfigured(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTWordTimeOffsets(false))
	config := googleRecognitionConfig(provider, "en-US")

	if config.EnableWordTimeOffsets {
		t.Fatal("word time offsets enabled = true, want false from reference option")
	}
	if got := provider.Capabilities().AlignedTranscript; got != "" {
		t.Fatalf("AlignedTranscript = %q, want empty when word offsets disabled", got)
	}
}

func TestGoogleRecognitionConfigChirp3DisablesWordTimeOffsets(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTModel("chirp_3"))
	config := googleRecognitionConfig(provider, "en-US")

	if config.EnableWordTimeOffsets {
		t.Fatal("word time offsets enabled = true, want false for chirp_3")
	}
}

func TestGoogleRecognitionConfigUsesProviderOptions(t *testing.T) {
	provider := newGoogleSTTWithClient(nil,
		WithGoogleSTTModel("command_and_search"),
		WithGoogleSTTPunctuate(false),
		WithGoogleSTTSpokenPunctuation(true),
		WithGoogleSTTSampleRate(8000),
		WithGoogleSTTProfanityFilter(true),
	)

	config := googleRecognitionConfig(provider, "en-US")

	if config.Model != "command_and_search" {
		t.Fatalf("model = %q, want command_and_search", config.Model)
	}
	if config.EnableAutomaticPunctuation {
		t.Fatal("automatic punctuation = true, want false")
	}
	if config.SampleRateHertz != 8000 {
		t.Fatalf("sample rate = %d, want 8000", config.SampleRateHertz)
	}
	if config.EnableSpokenPunctuation == nil || !config.EnableSpokenPunctuation.Value {
		t.Fatalf("spoken punctuation = %v, want true", config.EnableSpokenPunctuation)
	}
	if !config.ProfanityFilter {
		t.Fatal("profanity filter = false, want true")
	}
}

func TestGoogleRecognitionConfigUsesReferenceKeywordAdaptation(t *testing.T) {
	provider := newGoogleSTTWithClient(nil,
		WithGoogleSTTKeywords(
			GoogleSTTKeyword{Value: "Cavos", Boost: 12.5},
			GoogleSTTKeyword{Value: "LiveKit", Boost: 9},
		),
	)

	config := googleRecognitionConfig(provider, "en-US")

	if config.Adaptation == nil || len(config.Adaptation.PhraseSets) != 1 {
		t.Fatalf("adaptation = %#v, want one phrase set", config.Adaptation)
	}
	phraseSet := config.Adaptation.PhraseSets[0]
	if phraseSet.Name != "keywords" {
		t.Fatalf("phrase set name = %q, want keywords", phraseSet.Name)
	}
	if len(phraseSet.Phrases) != 2 {
		t.Fatalf("phrases = %#v, want two keyword phrases", phraseSet.Phrases)
	}
	if got := phraseSet.Phrases[0]; got.Value != "Cavos" || got.Boost != 12.5 {
		t.Fatalf("first phrase = %#v, want Cavos boost 12.5", got)
	}
	if got := phraseSet.Phrases[1]; got.Value != "LiveKit" || got.Boost != 9 {
		t.Fatalf("second phrase = %#v, want LiveKit boost 9", got)
	}
}

func TestGoogleRecognitionConfigUsesReferenceAdaptationOverKeywords(t *testing.T) {
	adaptation := &speechpb.SpeechAdaptation{
		PhraseSets: []*speechpb.PhraseSet{{
			Name: "custom",
			Phrases: []*speechpb.PhraseSet_Phrase{{
				Value: "Acrux",
				Boost: 20,
			}},
		}},
	}
	provider := newGoogleSTTWithClient(nil,
		WithGoogleSTTKeywords(GoogleSTTKeyword{Value: "ignored", Boost: 1}),
		WithGoogleSTTAdaptation(adaptation),
	)

	config := googleRecognitionConfig(provider, "en-US")

	if config.Adaptation != adaptation {
		t.Fatalf("adaptation = %#v, want configured adaptation over keywords", config.Adaptation)
	}
}

func TestGoogleRecognitionConfigUsesReferenceAlternativeLanguages(t *testing.T) {
	provider := newGoogleSTTWithClient(nil,
		WithGoogleSTTAlternativeLanguages("es-ES", "fr-FR"),
	)

	config := googleRecognitionConfig(provider, "en-US")

	if config.LanguageCode != "en-US" {
		t.Fatalf("language code = %q, want en-US", config.LanguageCode)
	}
	if len(config.AlternativeLanguageCodes) != 2 {
		t.Fatalf("alternative languages = %#v, want two entries", config.AlternativeLanguageCodes)
	}
	if config.AlternativeLanguageCodes[0] != "es-ES" || config.AlternativeLanguageCodes[1] != "fr-FR" {
		t.Fatalf("alternative languages = %#v, want [es-ES fr-FR]", config.AlternativeLanguageCodes)
	}
}

func TestGoogleRecognitionConfigOmitsAlternativeLanguagesWhenDetectionDisabled(t *testing.T) {
	provider := newGoogleSTTWithClient(nil,
		WithGoogleSTTDetectLanguage(false),
		WithGoogleSTTAlternativeLanguages("es-ES", "fr-FR"),
	)

	config := googleRecognitionConfig(provider, "en-US")

	if config.LanguageCode != "en-US" {
		t.Fatalf("language code = %q, want en-US", config.LanguageCode)
	}
	if len(config.AlternativeLanguageCodes) != 0 {
		t.Fatalf("alternative languages = %#v, want none when detect_language is false", config.AlternativeLanguageCodes)
	}
}

func TestNewGoogleSTTRejectsMissingCredentialsFile(t *testing.T) {
	_, err := NewGoogleSTT("/definitely/missing/google-credentials.json")
	if err == nil {
		t.Fatal("NewGoogleSTT returned nil error, want missing credentials error")
	}
}

func TestGoogleSTTLabel(t *testing.T) {
	provider := &GoogleSTT{}
	if got := provider.Label(); got != "google.STT" {
		t.Fatalf("Label = %q, want google.STT", got)
	}
}

func TestGoogleSTTExposesInputSampleRate(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTSampleRate(16000))
	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate = %d, want 16000", got)
	}
}

func TestGoogleSTTRecognizeSendsAudioAndMapsFinalEvent(t *testing.T) {
	client := &fakeGoogleSpeechClient{
		recognizeResponse: &speechpb.RecognizeResponse{
			Results: []*speechpb.SpeechRecognitionResult{{
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "hello",
				}},
			}},
		},
	}
	provider := newGoogleSTTWithClient(client)

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{
		{Data: []byte("one")},
		{Data: []byte("two")},
	}, "")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}

	if client.recognizeRequest == nil {
		t.Fatal("Recognize did not call client")
	}
	if got := string(client.recognizeRequest.GetAudio().GetContent()); got != "onetwo" {
		t.Fatalf("audio content = %q, want onetwo", got)
	}
	if got := client.recognizeRequest.GetConfig().GetLanguageCode(); got != "en-US" {
		t.Fatalf("language = %q, want en-US", got)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 || event.Alternatives[0].Text != "hello" {
		t.Fatalf("event = %#v, want final hello transcript", event)
	}
	if event.Alternatives[0].Language != "en-US" {
		t.Fatalf("language = %q, want en-US", event.Alternatives[0].Language)
	}
}

func TestGoogleSTTRecognizeUsesReferenceFrameAudioFormat(t *testing.T) {
	client := &fakeGoogleSpeechClient{recognizeResponse: &speechpb.RecognizeResponse{}}
	provider := newGoogleSTTWithClient(client)

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{
		{Data: []byte("one"), SampleRate: 48000, NumChannels: 2},
		{Data: []byte("two"), SampleRate: 48000, NumChannels: 2},
	}, "")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}

	config := client.recognizeRequest.GetConfig()
	if config.GetSampleRateHertz() != 48000 {
		t.Fatalf("sample rate = %d, want reference frame sample rate 48000", config.GetSampleRateHertz())
	}
	if config.GetAudioChannelCount() != 2 {
		t.Fatalf("audio channel count = %d, want reference frame channel count 2", config.GetAudioChannelCount())
	}
}

func TestGoogleSTTRecognizeReturnsAPITimeoutError(t *testing.T) {
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{recognizeErr: context.DeadlineExceeded})

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")

	if event != nil {
		t.Fatalf("Recognize event = %#v, want nil", event)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Recognize error = %T %v, want APITimeoutError", err, err)
	}
}

func TestGoogleSTTRecognizeReturnsAPIStatusError(t *testing.T) {
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{recognizeErr: status.Error(codes.PermissionDenied, "permission denied")})

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")

	if event != nil {
		t.Fatalf("Recognize event = %#v, want nil", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Recognize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.PermissionDenied) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.PermissionDenied)
	}
	if statusErr.Retryable {
		t.Fatal("status retryable = true, want false for permission denied")
	}
}

func TestGoogleSTTRecognizeCombinesReferenceResultSegments(t *testing.T) {
	client := &fakeGoogleSpeechClient{
		recognizeResponse: &speechpb.RecognizeResponse{
			Results: []*speechpb.SpeechRecognitionResult{
				{
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "hello ",
						Confidence: 0.8,
						Words: []*speechpb.WordInfo{{
							Word:       "hello",
							StartTime:  durationpb.New(100 * 1000 * 1000),
							EndTime:    durationpb.New(300 * 1000 * 1000),
							Confidence: 0.81,
						}},
					}},
				},
				{
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "world",
						Confidence: 0.6,
						Words: []*speechpb.WordInfo{{
							Word:       "world",
							StartTime:  durationpb.New(400 * 1000 * 1000),
							EndTime:    durationpb.New(700 * 1000 * 1000),
							Confidence: 0.61,
						}},
					}},
				},
			},
		},
	}
	provider := newGoogleSTTWithClient(client)

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}

	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %#v, want one final transcript", event)
	}
	got := event.Alternatives[0]
	if got.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", got.Text)
	}
	if math.Abs(got.Confidence-0.7) > 0.000001 {
		t.Fatalf("confidence = %v, want averaged confidence 0.7", got.Confidence)
	}
	if len(got.Words) != 1 || got.Words[0].Text != "hello" {
		t.Fatalf("words = %#v, want reference first-result word details only", got.Words)
	}
	if math.Abs(got.Words[0].StartTime-0.1) > 0.000001 || math.Abs(got.Words[0].EndTime-0.3) > 0.000001 {
		t.Fatalf("first word timing = %v-%v, want first result word timing", got.Words[0].StartTime, got.Words[0].EndTime)
	}
	if math.Abs(got.StartTime-0.1) > 0.000001 || math.Abs(got.EndTime-0.7) > 0.000001 {
		t.Fatalf("timing = %v-%v, want first word start through last word end", got.StartTime, got.EndTime)
	}
}

func TestGoogleSTTRecognizeUsesProviderResultLanguage(t *testing.T) {
	client := &fakeGoogleSpeechClient{
		recognizeResponse: &speechpb.RecognizeResponse{
			Results: []*speechpb.SpeechRecognitionResult{{
				LanguageCode: "fr-FR",
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "bonjour",
				}},
			}},
		},
	}
	provider := newGoogleSTTWithClient(client)

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "en-US")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %#v, want one final transcript", event)
	}
	if got := event.Alternatives[0].Language; got != "fr-FR" {
		t.Fatalf("language = %q, want provider result language fr-FR", got)
	}
}

func TestGoogleSTTStreamSendsConfigAndEmitsEvents(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal: true,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "streamed",
				}},
			}},
		}},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if len(streamClient.sent) != 1 {
		t.Fatalf("initial sends = %d, want 1", len(streamClient.sent))
	}
	config := streamClient.sent[0].GetStreamingConfig()
	if config == nil || config.GetConfig().GetLanguageCode() != "id-ID" || !config.GetInterimResults() {
		t.Fatalf("streaming config = %#v, want id-ID interim config", config)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("pcm")}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	if len(streamClient.sent) != 2 || string(streamClient.sent[1].GetAudioContent()) != "pcm" {
		t.Fatalf("audio sends = %#v, want pcm audio request", streamClient.sent)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || event.Alternatives[0].Text != "streamed" {
		t.Fatalf("event = %#v, want final streamed transcript", event)
	}
	if event.Alternatives[0].Language != "id-ID" {
		t.Fatalf("language = %q, want id-ID", event.Alternatives[0].Language)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !streamClient.closed {
		t.Fatal("Close did not close streaming client")
	}
}

func TestGoogleSTTStreamResamplesPushedAudioLikeReference(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	frame := &model.AudioFrame{
		Data:              []byte{1, 0, 2, 0, 3, 0, 4, 0, 5, 0, 6, 0},
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 6,
	}
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}

	if len(streamClient.sent) != 2 {
		t.Fatalf("sent requests = %d, want config plus resampled audio", len(streamClient.sent))
	}
	if got, want := streamClient.sent[1].GetAudioContent(), []byte{1, 0, 4, 0}; !bytes.Equal(got, want) {
		t.Fatalf("audio content = %#v, want 48k->16k reference resampled PCM %#v", got, want)
	}
}

func TestGoogleSTTStreamFlushesReferenceResamplerTail(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	frame := &model.AudioFrame{
		Data:              []byte{7, 0},
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	if err := stream.PushFrame(frame); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	if len(streamClient.sent) != 1 {
		t.Fatalf("sent after PushFrame = %d, want only config before resampler tail flush", len(streamClient.sent))
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if len(streamClient.sent) != 2 {
		t.Fatalf("sent after Flush = %d, want config plus flushed resampler tail", len(streamClient.sent))
	}
	if got, want := streamClient.sent[1].GetAudioContent(), []byte{7, 0}; !bytes.Equal(got, want) {
		t.Fatalf("flushed audio content = %#v, want reference resampler tail %#v", got, want)
	}
}

func TestGoogleSTTStreamEndInputFlushesTailAndDrainsFinalLikeReference(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal: true,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "done",
				}},
			}},
		}},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              []byte{7, 0},
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatalf("stream type = %T, want reference EndInput support", stream)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}
	if !streamClient.closed {
		t.Fatal("EndInput did not close provider send side")
	}
	if len(streamClient.sent) != 2 {
		t.Fatalf("sent after EndInput = %d, want config plus flushed resampler tail", len(streamClient.sent))
	}
	if got, want := streamClient.sent[1].GetAudioContent(), []byte{7, 0}; !bytes.Equal(got, want) {
		t.Fatalf("EndInput audio content = %#v, want flushed tail %#v", got, want)
	}
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next after EndInput returned error: %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 || event.Alternatives[0].Text != "done" {
		t.Fatalf("Next after EndInput = %#v, want final transcript", event)
	}
}

func TestGoogleSTTProviderCloseClosesEndedInputStream(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{recvBlock: make(chan struct{})}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatalf("stream type = %T, want EndInput support", stream)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}
	streamClient.closed = false

	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close returned error: %v", err)
	}
	if !streamClient.closed {
		t.Fatal("provider Close did not close stream after EndInput")
	}
	close(streamClient.recvBlock)
}

func TestGoogleSTTStreamExplicitLanguageOverridesReferenceAlternatives(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(
		&fakeGoogleSpeechClient{stream: streamClient},
		WithGoogleSTTAlternativeLanguages("es-ES", "fr-FR"),
	)

	stream, err := provider.Stream(context.Background(), "id-ID")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if len(streamClient.sent) != 1 {
		t.Fatalf("initial sends = %d, want 1", len(streamClient.sent))
	}
	config := streamClient.sent[0].GetStreamingConfig().GetConfig()
	if config.GetLanguageCode() != "id-ID" {
		t.Fatalf("language code = %q, want id-ID", config.GetLanguageCode())
	}
	if len(config.GetAlternativeLanguageCodes()) != 0 {
		t.Fatalf("alternative languages = %#v, want none for explicit stream language", config.GetAlternativeLanguageCodes())
	}
}

func TestGoogleSTTStreamConfigUsesReferenceInterimResultsOption(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(
		&fakeGoogleSpeechClient{stream: streamClient},
		WithGoogleSTTInterimResults(false),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	config := streamClient.sent[0].GetStreamingConfig()
	if config.GetInterimResults() {
		t.Fatal("interim_results = true, want false from reference interim_results option")
	}
}

func TestGoogleSTTStreamCombinesReferenceInterimResultSegments(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{
				{
					LanguageCode: "en-AU",
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "hello ",
						Confidence: 0.8,
						Words: []*speechpb.WordInfo{{
							Word:       "hello",
							StartTime:  durationpb.New(100 * 1000 * 1000),
							EndTime:    durationpb.New(300 * 1000 * 1000),
							Confidence: 0.81,
						}},
					}},
				},
				{
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "world",
						Confidence: 0.6,
						Words: []*speechpb.WordInfo{{
							Word:       "world",
							StartTime:  durationpb.New(400 * 1000 * 1000),
							EndTime:    durationpb.New(700 * 1000 * 1000),
							Confidence: 0.61,
						}},
					}},
				},
			},
		}},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %#v, want one interim transcript", event)
	}
	got := event.Alternatives[0]
	if got.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", got.Text)
	}
	if math.Abs(got.Confidence-0.7) > 0.000001 {
		t.Fatalf("confidence = %v, want averaged confidence 0.7", got.Confidence)
	}
	if got.Language != "en-AU" {
		t.Fatalf("language = %q, want first provider result language", got.Language)
	}
	if len(got.Words) != 2 || got.Words[0].Text != "hello" || got.Words[1].Text != "world" {
		t.Fatalf("words = %#v, want all interim result words in order", got.Words)
	}
	if math.Abs(got.StartTime-0.1) > 0.000001 || math.Abs(got.EndTime-0.7) > 0.000001 {
		t.Fatalf("timing = %v-%v, want full interim result span", got.StartTime, got.EndTime)
	}

	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want EOF after one combined event", err)
	}
}

func TestGoogleSTTStreamUsesFirstReferenceResultFinality(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{
				{
					LanguageCode: "en-AU",
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "still ",
						Confidence: 0.9,
					}},
				},
				{
					IsFinal:      true,
					LanguageCode: "en-AU",
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "done",
						Confidence: 0.8,
						Words: []*speechpb.WordInfo{{
							Word:      "done",
							StartTime: durationpb.New(300 * 1000 * 1000),
							EndTime:   durationpb.New(600 * 1000 * 1000),
						}},
					}},
				},
			},
		}},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %#v, want interim transcript from first result finality", event)
	}
	got := event.Alternatives[0]
	if got.Text != "done" {
		t.Fatalf("text = %q, want later final result text", got.Text)
	}
	if got.StartTime != 0.3 || got.EndTime != 0.6 {
		t.Fatalf("timing = %v-%v, want later final result timing", got.StartTime, got.EndTime)
	}
}

func TestGoogleSTTStreamUsesFirstReferenceFinalResult(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{
				{
					IsFinal:      true,
					LanguageCode: "en-GB",
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "good ",
						Confidence: 0.9,
						Words: []*speechpb.WordInfo{{
							Word:       "good",
							StartTime:  durationpb.New(100 * 1000 * 1000),
							EndTime:    durationpb.New(300 * 1000 * 1000),
							Confidence: 0.91,
						}},
					}},
				},
				{
					IsFinal: true,
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "morning",
						Confidence: 0.8,
						Words: []*speechpb.WordInfo{{
							Word:       "morning",
							StartTime:  durationpb.New(400 * 1000 * 1000),
							EndTime:    durationpb.New(900 * 1000 * 1000),
							Confidence: 0.81,
						}},
					}},
				},
			},
		}},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %#v, want one final transcript", event)
	}
	got := event.Alternatives[0]
	if got.Text != "good " {
		t.Fatalf("text = %q, want first final transcript only", got.Text)
	}
	if got.Language != "en-GB" {
		t.Fatalf("language = %q, want first final provider language", got.Language)
	}
	if math.Abs(got.Confidence-0.9) > 0.000001 {
		t.Fatalf("confidence = %v, want first final confidence", got.Confidence)
	}
	if len(got.Words) != 1 || got.Words[0].Text != "good" {
		t.Fatalf("words = %#v, want first final result words only", got.Words)
	}
	if math.Abs(got.StartTime-0.1) > 0.000001 || math.Abs(got.EndTime-0.3) > 0.000001 {
		t.Fatalf("timing = %v-%v, want first final result span", got.StartTime, got.EndTime)
	}
}

func TestGoogleSTTStreamSuppressesEmptyFinalTranscriptLikeReference(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal: true,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "",
					Confidence: 0.9,
				}},
			}},
		}},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	event, err := stream.Next()

	if event != nil {
		t.Fatalf("Next event = %#v, want nil for empty final transcript", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF after suppressed empty final", err)
	}
}

func TestGoogleSTTStreamAppliesReferenceStartTimeOffset(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal: true,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "hello",
					Confidence: 0.9,
					Words: []*speechpb.WordInfo{{
						Word:       "hello",
						StartTime:  durationpb.New(100 * 1000 * 1000),
						EndTime:    durationpb.New(400 * 1000 * 1000),
						Confidence: 0.9,
					}},
				}},
			}},
		}},
	}
	stream := &googleSTTStream{
		stream:        streamClient,
		minConfidence: 0.65,
		events:        make(chan *stt.SpeechEvent, 10),
		errCh:         make(chan error, 1),
	}
	timing, ok := interface{}(stream).(stt.StreamTiming)
	if !ok {
		t.Fatal("google STT stream does not implement stt.StreamTiming")
	}
	timing.SetStartTimeOffset(2.5)
	timing.SetStartTime(123.5)
	if timing.StartTimeOffset() != 2.5 || timing.StartTime() != 123.5 {
		t.Fatalf("timing = offset %v start %v, want reference values", timing.StartTimeOffset(), timing.StartTime())
	}

	go stream.readLoop()
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %#v, want final transcript", event)
	}
	alt := event.Alternatives[0]
	if math.Abs(alt.StartTime-2.6) > 0.000001 || math.Abs(alt.EndTime-2.9) > 0.000001 {
		t.Fatalf("transcript timing = %v-%v, want reference start_time_offset applied", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 1 || math.Abs(alt.Words[0].StartTime-2.6) > 0.000001 || math.Abs(alt.Words[0].EndTime-2.9) > 0.000001 || alt.Words[0].StartTimeOffset != 2.5 {
		t.Fatalf("word timing = %+v, want reference start_time_offset applied", alt.Words)
	}

	assertGooglePanicsWithMessage(t, "start_time_offset must be non-negative", func() {
		timing.SetStartTimeOffset(-0.01)
	})
	if got := timing.StartTimeOffset(); got != 2.5 {
		t.Fatalf("StartTimeOffset after rejected update = %v, want 2.5", got)
	}
	assertGooglePanicsWithMessage(t, "start_time must be non-negative", func() {
		timing.SetStartTime(-0.01)
	})
	if got := timing.StartTime(); got != 123.5 {
		t.Fatalf("StartTime after rejected update = %v, want 123.5", got)
	}
}

func TestGoogleSTTStreamSuppressesLowConfidenceInterimTranscript(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "maybe",
					Confidence: 0.3,
				}},
			}},
		}},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if event, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next event = %#v, error = %v; want EOF without low-confidence interim event", event, err)
	}
}

func TestGoogleSTTConfiguredMinConfidenceThresholdFiltersInterimTranscript(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "almost",
					Confidence: 0.7,
				}},
			}},
		}},
	}
	provider := newGoogleSTTWithClient(
		&fakeGoogleSpeechClient{stream: streamClient},
		WithGoogleSTTMinConfidenceThreshold(0.75),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if event, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next event = %#v, error = %v; want EOF without below-threshold interim event", event, err)
	}
}

func TestGoogleSTTUpdateOptionsAppliesActiveStreamMinConfidence(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{
		recvBlock: secondRelease,
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "maybe",
					Confidence: 0.6,
				}},
			}},
		}},
	}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client)
	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh

	provider.UpdateOptions(WithGoogleSTTMinConfidenceThreshold(0.5))

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want reconnected stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for reconnected stream")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after min confidence update")
	}
	close(firstRelease)
	close(secondRelease)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if event == nil || event.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event = %#v, want interim transcript after lowered min confidence", event)
	}
	if got := event.Alternatives[0].Text; got != "maybe" {
		t.Fatalf("transcript = %q, want maybe", got)
	}
}

func TestGoogleSTTUpdateOptionsReconnectsActiveStreamSpeechTimeouts(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: secondRelease}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh
	if len(firstStream.sent) != 1 || firstStream.sent[0].GetStreamingConfig().GetVoiceActivityTimeout() != nil {
		t.Fatalf("first stream config = %#v, want no voice activity timeout", firstStream.sent)
	}

	provider.UpdateOptions(WithGoogleSTTSpeechEndTimeout(750 * time.Millisecond))

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want reconnected stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for reconnected stream")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after speech timeout update")
	}
	if len(secondStream.sent) != 1 {
		t.Fatalf("second stream sent = %#v, want fresh config", secondStream.sent)
	}
	timeout := secondStream.sent[0].GetStreamingConfig().GetVoiceActivityTimeout()
	if timeout == nil || timeout.GetSpeechEndTimeout().AsDuration() != 750*time.Millisecond {
		t.Fatalf("second stream voice timeout = %#v, want speech_end_timeout 750ms", timeout)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTUpdateOptionsAppliesActiveStreamLanguage(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: secondRelease}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client)

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh
	if got := firstStream.sent[0].GetStreamingConfig().GetConfig().GetLanguageCode(); got != "en-US" {
		t.Fatalf("first stream language = %q, want en-US", got)
	}

	provider.UpdateOptions(WithGoogleSTTLanguage("id-ID"))

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want reconnected stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for reconnected stream")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after language update")
	}
	if len(secondStream.sent) != 1 {
		t.Fatalf("second stream sent = %#v, want fresh config", secondStream.sent)
	}
	if got := secondStream.sent[0].GetStreamingConfig().GetConfig().GetLanguageCode(); got != "id-ID" {
		t.Fatalf("second stream language = %q, want updated id-ID", got)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTUpdateOptionsAppliesEmptyActiveStreamLanguage(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: secondRelease}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client, WithGoogleSTTLanguage("id-ID"))

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh
	if got := firstStream.sent[0].GetStreamingConfig().GetConfig().GetLanguageCode(); got != "id-ID" {
		t.Fatalf("first stream language = %q, want id-ID", got)
	}

	provider.UpdateOptions(WithGoogleSTTLanguage(""))

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want reconnected stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for reconnected stream")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after language update")
	}
	if len(secondStream.sent) != 1 {
		t.Fatalf("second stream sent = %#v, want fresh config", secondStream.sent)
	}
	if got := secondStream.sent[0].GetStreamingConfig().GetConfig().GetLanguageCode(); got != "" {
		t.Fatalf("second stream language = %q, want explicit empty language", got)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTStreamConfidenceThresholdUsesAllReferenceResults(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{
				{},
				{
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "maybe",
						Confidence: 0.8,
					}},
				},
			},
		}},
	}
	provider := newGoogleSTTWithClient(
		&fakeGoogleSpeechClient{stream: streamClient},
		WithGoogleSTTMinConfidenceThreshold(0.5),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if event, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next event = %#v, error = %v; want EOF because reference averages confidence across all results", event, err)
	}
}

func TestGoogleSTTStreamMapsReferenceVoiceActivityEvents(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{
			{SpeechEventType: speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_BEGIN},
			{SpeechEventType: speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_END},
		},
	}
	provider := newGoogleSTTWithClient(
		&fakeGoogleSpeechClient{stream: streamClient},
		WithGoogleSTTVoiceActivityEvents(true),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	config := streamClient.sent[0].GetStreamingConfig()
	if config == nil || !config.GetEnableVoiceActivityEvents() {
		t.Fatalf("enable voice activity events = %v, want true", config.GetEnableVoiceActivityEvents())
	}

	start, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event type = %v, want start of speech", start.Type)
	}

	end, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("second event type = %v, want end of speech", end.Type)
	}
}

func TestGoogleSTTStreamDoesNotEndSpeechAfterFinalTranscriptOnly(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal: true,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "done",
					Confidence: 0.9,
				}},
			}},
		}},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("first event type = %v, want final transcript", final.Type)
	}

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("second event = %#v, want no end-of-speech without provider activity end", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestGoogleSTTStreamEmitsProviderActivityEndAfterFinalTranscript(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{
			{
				Results: []*speechpb.StreamingRecognitionResult{{
					IsFinal: true,
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "done",
						Confidence: 0.9,
					}},
				}},
			},
			{
				SpeechEventType: speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_END,
			},
		},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("first event type = %v, want final transcript", final.Type)
	}

	end, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("second event type = %v, want provider end of speech", end.Type)
	}
}

func TestGoogleSTTStreamConfigUsesReferenceSpeechTimeouts(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(
		&fakeGoogleSpeechClient{stream: streamClient},
		WithGoogleSTTSpeechStartTimeout(1500*time.Millisecond),
		WithGoogleSTTSpeechEndTimeout(750*time.Millisecond),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	config := streamClient.sent[0].GetStreamingConfig()
	if config == nil {
		t.Fatal("streaming config = nil")
	}
	if !config.GetEnableVoiceActivityEvents() {
		t.Fatal("enable voice activity events = false, want true when speech timeout configured")
	}
	timeout := config.GetVoiceActivityTimeout()
	if timeout == nil {
		t.Fatal("voice activity timeout = nil")
	}
	if got := timeout.GetSpeechStartTimeout().AsDuration(); got != 1500*time.Millisecond {
		t.Fatalf("speech start timeout = %v, want 1.5s", got)
	}
	if got := timeout.GetSpeechEndTimeout().AsDuration(); got != 750*time.Millisecond {
		t.Fatalf("speech end timeout = %v, want 750ms", got)
	}
}

func TestGoogleSTTStreamIgnoresTranscriptOnVoiceActivityEvent(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			SpeechEventType: speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_END,
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal: true,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "stale final",
					Confidence: 0.9,
				}},
			}},
		}},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("event type = %v, want end of speech", event.Type)
	}

	if event, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next event = %#v, error = %v; want EOF without transcript", event, err)
	}
}

func TestGoogleSTTStreamEmitsReferenceRecognitionUsage(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{
			{
				SpeechEventTime: durationpb.New(400 * time.Millisecond),
				RequestId:       123,
			},
			{
				TotalBilledTime: durationpb.New(time.Second),
				RequestId:       456,
			},
		},
	}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if first.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("first event type = %v, want recognition_usage", first.Type)
	}
	if first.RequestID != "123" {
		t.Fatalf("first request id = %q, want 123", first.RequestID)
	}
	if first.RecognitionUsage == nil || first.RecognitionUsage.AudioDuration != 0.4 {
		t.Fatalf("first usage = %+v, want 0.4s", first.RecognitionUsage)
	}

	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if second.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("second event type = %v, want recognition_usage", second.Type)
	}
	if second.RequestID != "456" {
		t.Fatalf("second request id = %q, want 456", second.RequestID)
	}
	if second.RecognitionUsage == nil || second.RecognitionUsage.AudioDuration != 0.6 {
		t.Fatalf("second usage = %+v, want billed delta 0.6s", second.RecognitionUsage)
	}
}

func TestGoogleSTTStreamPropagatesClientErrors(t *testing.T) {
	wantErr := errors.New("stream error")
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{streamErr: wantErr})

	_, err := provider.Stream(context.Background(), "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Stream error = %v, want %v", err, wantErr)
	}
}

func TestGoogleSTTStreamReturnsAPIStatusErrorForClientStatusError(t *testing.T) {
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{streamErr: status.Error(codes.Unavailable, "unavailable")})

	_, err := provider.Stream(context.Background(), "")

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Stream error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.Unavailable) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.Unavailable)
	}
	if !statusErr.Retryable {
		t.Fatal("status retryable = false, want true for unavailable")
	}
}

func TestGoogleSTTStreamReturnsAPIStatusErrorForConfigSendFailure(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{sendErrOnConfig: status.Error(codes.PermissionDenied, "permission denied")}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	_, err := provider.Stream(context.Background(), "en-US")

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Stream error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.PermissionDenied) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.PermissionDenied)
	}
	if statusErr.Retryable {
		t.Fatal("status retryable = true, want false for permission denied")
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after config send failure")
	}
}

func TestGoogleSTTStreamClosesAfterAudioSendFailure(t *testing.T) {
	wantErr := errors.New("send failed")
	streamClient := &fakeGoogleStreamingRecognizeClient{sendErrAfterConfig: wantErr}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	err = stream.PushFrame(&model.AudioFrame{Data: []byte("pcm")})
	if !errors.Is(err, wantErr) {
		t.Fatalf("PushFrame error = %v, want %v", err, wantErr)
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after send failure")
	}

	err = stream.PushFrame(&model.AudioFrame{Data: []byte("again")})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushFrame error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after send failure error = %v", err)
	}
}

func TestGoogleSTTStreamPushFrameReturnsAPIStatusErrorForAudioSendFailure(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{sendErrAfterConfig: status.Error(codes.Unavailable, "unavailable")}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	err = stream.PushFrame(&model.AudioFrame{Data: []byte("pcm")})

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("PushFrame error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.Unavailable) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.Unavailable)
	}
	if !statusErr.Retryable {
		t.Fatal("status retryable = false, want true for unavailable")
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after send failure")
	}
}

func TestGoogleSTTStreamRejectsReferenceSampleRateChange(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000}); err != nil {
		t.Fatalf("first PushFrame error = %v", err)
	}
	err = stream.PushFrame(&model.AudioFrame{Data: []byte("second"), SampleRate: 24000})
	if err == nil || err.Error() != "the sample rate of the input frames must be consistent" {
		t.Fatalf("second PushFrame error = %v, want reference sample-rate consistency error", err)
	}
	if len(streamClient.sent) != 2 {
		t.Fatalf("sent requests = %d, want config plus first audio only", len(streamClient.sent))
	}
}

func TestGoogleSTTProviderCloseClosesActiveStreams(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after provider Close")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("again")}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestGoogleSTTStreamCloseSuppressesProviderCloseError(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{closeErr: errors.New("close failed")}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v, want nil cleanup error", err)
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after Close")
	}
}

func TestGoogleSTTNextReturnsQueuedTranscriptBeforeStreamError(t *testing.T) {
	for range 64 {
		stream := &googleSTTStream{
			stream: &fakeGoogleStreamingRecognizeClient{},
			events: make(chan *stt.SpeechEvent, 1),
			errCh:  make(chan error, 1),
		}
		stream.events <- &stt.SpeechEvent{
			Type: stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{
				{Text: "hello"},
			},
		}
		close(stream.events)
		stream.errCh <- errors.New("stream failed")

		event, err := stream.Next()
		if err != nil {
			t.Fatalf("Next error = %v, want queued transcript before stream error", err)
		}
		if event == nil || event.Type != stt.SpeechEventFinalTranscript {
			t.Fatalf("Next event = %#v, want queued final transcript", event)
		}
		if got := event.Alternatives[0].Text; got != "hello" {
			t.Fatalf("transcript = %q, want hello", got)
		}

		if _, err := stream.Next(); err == nil || err.Error() != "stream failed" {
			t.Fatalf("second Next error = %v, want queued stream error after transcript drains", err)
		}
	}
}

func TestGoogleSTTStreamNextReturnsAPITimeoutError(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{recvErr: context.DeadlineExceeded}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	event, err := stream.Next()

	if event != nil {
		t.Fatalf("Next event = %#v, want nil", event)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestGoogleSTTStreamNextReturnsAPIStatusError(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{recvErr: status.Error(codes.Unavailable, "unavailable")}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	event, err := stream.Next()

	if event != nil {
		t.Fatalf("Next event = %#v, want nil", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.Unavailable) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.Unavailable)
	}
	if !statusErr.Retryable {
		t.Fatal("status retryable = false, want true for unavailable")
	}
}

func TestGoogleSTTStreamTreatsReference409AsRetryable(t *testing.T) {
	err := googleSTTStreamError(status.Error(codes.AlreadyExists, "stream conflict"))
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("mapped error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.AlreadyExists) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.AlreadyExists)
	}
	if !statusErr.Retryable {
		t.Fatal("status retryable = false, want true for reference 409 restart path")
	}
}

func TestGoogleSTTStreamRestartsAfterReference409WithAudio(t *testing.T) {
	firstStream := &fakeGoogleStreamingRecognizeClient{recvErr: status.Error(codes.AlreadyExists, "stream conflict")}
	restartedRecv := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: restartedRecv}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000}); err != nil {
		t.Fatalf("first PushFrame returned error: %v", err)
	}

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want restarted stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for restarted stream")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after restart")
	}
	if len(secondStream.sent) != 1 || secondStream.sent[0].GetStreamingConfig() == nil {
		t.Fatalf("second stream sent = %#v, want fresh config", secondStream.sent)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("second"), SampleRate: 16000}); err != nil {
		t.Fatalf("second PushFrame after restart returned error: %v", err)
	}
	if len(secondStream.sent) != 2 || string(secondStream.sent[1].GetAudioContent()) != "second" {
		t.Fatalf("second stream sent = %#v, want later audio on restarted stream", secondStream.sent)
	}
	close(restartedRecv)
}

func TestGoogleSTTStreamRestartsAfterReference409BeforeAudio(t *testing.T) {
	firstStream := &fakeGoogleStreamingRecognizeClient{recvErr: status.Error(codes.AlreadyExists, "stream conflict")}
	restartedRecv := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: restartedRecv}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want pre-audio restart", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for pre-audio restart")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after pre-audio restart")
	}
	if len(secondStream.sent) != 1 || secondStream.sent[0].GetStreamingConfig() == nil {
		t.Fatalf("second stream sent = %#v, want fresh config", secondStream.sent)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("first"), SampleRate: 16000}); err != nil {
		t.Fatalf("PushFrame after pre-audio restart returned error: %v", err)
	}
	if len(secondStream.sent) != 2 || string(secondStream.sent[1].GetAudioContent()) != "first" {
		t.Fatalf("second stream sent = %#v, want audio on restarted stream", secondStream.sent)
	}
	close(restartedRecv)
}

func TestGoogleSTTStreamNextErrorTerminatesStream(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{recvErr: status.Error(codes.Unavailable, "unavailable")}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	event, err := stream.Next()

	if event != nil {
		t.Fatalf("Next event = %#v, want nil", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("again")}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after receive error = %v, want io.ErrClosedPipe", err)
	}
	if event, err := stream.Next(); event != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after receive error = (%#v, %v), want nil EOF", event, err)
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after receive error")
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams = %d, want 0 after receive error", len(provider.streams))
	}
}

func TestGoogleSTTStreamEOFTerminatesStream(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	event, err := stream.Next()

	if event != nil {
		t.Fatalf("Next event = %#v, want nil", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("again")}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after provider EOF = %v, want io.ErrClosedPipe", err)
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after provider EOF")
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams = %d, want 0 after provider EOF", len(provider.streams))
	}
}

func TestGoogleSTTClosedStreamNextReturnsEOF(t *testing.T) {
	stream := &googleSTTStream{
		stream: &fakeGoogleStreamingRecognizeClient{},
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	stream.events <- &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: "stale transcript"},
		},
	}
	result := make(chan error, 1)
	go func() {
		event, err := stream.Next()
		if event != nil {
			result <- errors.New("Next returned queued event after Close")
			return
		}
		result <- err
	}()

	select {
	case err := <-result:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next error after Close = %v, want %v", err, io.EOF)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next after Close blocked, want EOF")
	}
}

func TestGoogleSTTClosedStreamRejectsFlush(t *testing.T) {
	stream := &googleSTTStream{
		stream: &fakeGoogleStreamingRecognizeClient{},
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
}

func TestGoogleSTTRegisterStreamAfterCloseClosesStream(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{})
	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	stream := &googleSTTStream{
		owner:  provider,
		stream: streamClient,
		events: make(chan *stt.SpeechEvent, 1),
	}

	if provider.registerStream(stream) {
		t.Fatal("registerStream after provider Close = true, want false")
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after rejected registration")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("again")}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after rejected registration error = %v, want io.ErrClosedPipe", err)
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams = %d, want 0", len(provider.streams))
	}
}

func TestGoogleSTTStreamAfterCloseIsRejected(t *testing.T) {
	client := &fakeGoogleSpeechClient{stream: &fakeGoogleStreamingRecognizeClient{}}
	provider := newGoogleSTTWithClient(client)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	stream, err := provider.Stream(context.Background(), "en-US")
	if stream != nil {
		t.Fatalf("Stream after Close returned stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if client.streamCalls != 0 {
		t.Fatalf("Stream after Close client calls = %d, want 0", client.streamCalls)
	}
}

func TestGoogleSTTRecognizeAfterCloseIsRejected(t *testing.T) {
	client := &fakeGoogleSpeechClient{recognizeResponse: &speechpb.RecognizeResponse{}}
	provider := newGoogleSTTWithClient(client)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	event, err := provider.Recognize(context.Background(), nil, "en-US")
	if event != nil {
		t.Fatalf("Recognize after Close event = %#v, want nil", event)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Recognize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if client.recognizeCalls != 0 {
		t.Fatalf("Recognize after Close client calls = %d, want 0", client.recognizeCalls)
	}
}

type fakeGoogleSpeechClient struct {
	streams           []speechpb.Speech_StreamingRecognizeClient
	stream            speechpb.Speech_StreamingRecognizeClient
	streamErr         error
	streamCallCh      chan int
	streamCalls       int
	recognizeRequest  *speechpb.RecognizeRequest
	recognizeCalls    int
	recognizeResponse *speechpb.RecognizeResponse
	recognizeErr      error
}

func (c *fakeGoogleSpeechClient) StreamingRecognize(ctx context.Context, opts ...gax.CallOption) (speechpb.Speech_StreamingRecognizeClient, error) {
	c.streamCalls++
	if c.streamCallCh != nil {
		c.streamCallCh <- c.streamCalls
	}
	if len(c.streams) > 0 {
		stream := c.streams[0]
		c.streams = c.streams[1:]
		return stream, c.streamErr
	}
	return c.stream, c.streamErr
}

func (c *fakeGoogleSpeechClient) Recognize(ctx context.Context, req *speechpb.RecognizeRequest, opts ...gax.CallOption) (*speechpb.RecognizeResponse, error) {
	c.recognizeCalls++
	c.recognizeRequest = req
	return c.recognizeResponse, c.recognizeErr
}

type fakeGoogleStreamingRecognizeClient struct {
	sent               []*speechpb.StreamingRecognizeRequest
	responses          []*speechpb.StreamingRecognizeResponse
	recvIndex          int
	recvErr            error
	recvBlock          chan struct{}
	closed             bool
	closeErr           error
	sendErrOnConfig    error
	sendErrAfterConfig error
}

func (c *fakeGoogleStreamingRecognizeClient) Send(req *speechpb.StreamingRecognizeRequest) error {
	c.sent = append(c.sent, req)
	if req.GetStreamingConfig() != nil && c.sendErrOnConfig != nil {
		return c.sendErrOnConfig
	}
	if req.GetAudioContent() != nil && c.sendErrAfterConfig != nil {
		return c.sendErrAfterConfig
	}
	return nil
}

func (c *fakeGoogleStreamingRecognizeClient) Recv() (*speechpb.StreamingRecognizeResponse, error) {
	if c.recvIndex >= len(c.responses) {
		if c.recvBlock != nil {
			<-c.recvBlock
			c.recvBlock = nil
		}
		if c.recvErr != nil {
			return nil, c.recvErr
		}
		return nil, io.EOF
	}
	resp := c.responses[c.recvIndex]
	c.recvIndex++
	return resp, nil
}

func (c *fakeGoogleStreamingRecognizeClient) CloseSend() error {
	c.closed = true
	return c.closeErr
}

func (c *fakeGoogleStreamingRecognizeClient) Header() (metadata.MD, error) { return nil, nil }
func (c *fakeGoogleStreamingRecognizeClient) Trailer() metadata.MD         { return nil }
func (c *fakeGoogleStreamingRecognizeClient) Context() context.Context     { return context.Background() }
func (c *fakeGoogleStreamingRecognizeClient) SendMsg(m any) error          { return nil }
func (c *fakeGoogleStreamingRecognizeClient) RecvMsg(m any) error          { return nil }

func assertGooglePanicsWithMessage(t *testing.T, want string, fn func()) {
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
