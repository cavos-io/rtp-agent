package google

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/speech/apiv1/speechpb"
	speechv2pb "cloud.google.com/go/speech/apiv2/speechpb"
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

func TestGoogleSTTRecognizerUsesReferenceProjectFromCredentialsFile(t *testing.T) {
	credentials := t.TempDir() + "/service-account.json"
	err := os.WriteFile(credentials, []byte(`{"type":"service_account","project_id":"voice-project"}`), 0o600)
	if err != nil {
		t.Fatalf("write credentials: %v", err)
	}
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTLocation("us-central1"))

	project, err := googleProjectFromCredentialsFile(credentials)
	if err != nil {
		t.Fatalf("project from credentials returned error: %v", err)
	}
	provider.project = project

	if got := googleSTTRecognizer(provider); got != "projects/voice-project/locations/us-central1/recognizers/_" {
		t.Fatalf("recognizer = %q, want reference project from credentials", got)
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

func TestGoogleSTTEmptyLocationOptionMatchesReferenceEndpoint(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTLocation(""))

	if got := googleSTTEndpoint(provider); got != "-speech.googleapis.com" {
		t.Fatalf("endpoint = %q, want reference explicit empty location endpoint", got)
	}
}

func TestGoogleSTTClientOptionsUseCurrentReferenceLocation(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTLocation("europe-west1"))
	options, err := googleSTTClientOptions("", provider)
	if err != nil {
		t.Fatalf("googleSTTClientOptions returned error: %v", err)
	}
	if got := fmt.Sprintf("%#v", options); !strings.Contains(got, "europe-west1-speech.googleapis.com") {
		t.Fatalf("client options = %s, want europe-west1 endpoint", got)
	}

	provider.location = "us-central1"
	options, err = googleSTTClientOptions("", provider)
	if err != nil {
		t.Fatalf("googleSTTClientOptions after update returned error: %v", err)
	}
	if got := fmt.Sprintf("%#v", options); !strings.Contains(got, "us-central1-speech.googleapis.com") || strings.Contains(got, "europe-west1-speech.googleapis.com") {
		t.Fatalf("client options after location update = %s, want current us-central1 endpoint only", got)
	}
}

func TestGoogleSTTUpdateOptionsPreservesExplicitEmptyLocation(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTLocation("europe-west1"))

	provider.UpdateOptions(WithGoogleSTTLocation(""))

	if got := googleSTTEndpoint(provider); got != "-speech.googleapis.com" {
		t.Fatalf("endpoint = %q, want reference explicit empty location endpoint", got)
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
	if !capabilities.InterimResults {
		t.Fatal("InterimResults capability = false, want true like reference even when streaming disabled")
	}
	if capabilities.AlignedTranscript != "" {
		t.Fatalf("AlignedTranscript = %q, want empty when streaming disabled", capabilities.AlignedTranscript)
	}
	if !capabilities.OfflineRecognize {
		t.Fatal("OfflineRecognize capability = false, want true like reference")
	}
}

func TestGoogleSTTInterimCapabilityMatchesReferenceOption(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTInterimResults(false))

	if provider.interimResults {
		t.Fatal("interimResults option = true, want request option still disabled")
	}
	if !provider.Capabilities().InterimResults {
		t.Fatal("InterimResults capability = false, want true like reference despite interim_results option")
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

func TestGoogleStreamingRecognitionConfigV2UsesReferenceKeywordAdaptation(t *testing.T) {
	provider := newGoogleSTTWithV2Client(nil,
		WithGoogleSTTModel("chirp_3"),
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTKeywords(
			GoogleSTTKeyword{Value: "Cavos", Boost: 12.5},
			GoogleSTTKeyword{Value: "LiveKit", Boost: 9},
		),
	)

	config := googleStreamingRecognitionConfigV2(provider, "en-US", true)

	adaptation := config.GetConfig().GetAdaptation()
	if adaptation == nil || len(adaptation.GetPhraseSets()) != 1 {
		t.Fatalf("v2 adaptation = %#v, want one inline phrase set", adaptation)
	}
	phraseSet := adaptation.GetPhraseSets()[0].GetInlinePhraseSet()
	if phraseSet == nil {
		t.Fatalf("v2 phrase set = %#v, want inline phrase set", adaptation.GetPhraseSets()[0])
	}
	if len(phraseSet.GetPhrases()) != 2 {
		t.Fatalf("v2 phrases = %#v, want two keyword phrases", phraseSet.GetPhrases())
	}
	if got := phraseSet.GetPhrases()[0]; got.GetValue() != "Cavos" || got.GetBoost() != 12.5 {
		t.Fatalf("first v2 phrase = %#v, want Cavos boost 12.5", got)
	}
	if got := phraseSet.GetPhrases()[1]; got.GetValue() != "LiveKit" || got.GetBoost() != 9 {
		t.Fatalf("second v2 phrase = %#v, want LiveKit boost 9", got)
	}
}

func TestGoogleStreamingRecognitionConfigV2UsesReferenceCustomAdaptationOverKeywords(t *testing.T) {
	adaptation := &speechv2pb.SpeechAdaptation{
		PhraseSets: []*speechv2pb.SpeechAdaptation_AdaptationPhraseSet{{
			Value: &speechv2pb.SpeechAdaptation_AdaptationPhraseSet_InlinePhraseSet{
				InlinePhraseSet: &speechv2pb.PhraseSet{
					DisplayName: "custom",
					Phrases: []*speechv2pb.PhraseSet_Phrase{{
						Value: "Acrux",
						Boost: 20,
					}},
				},
			},
		}},
	}
	provider := newGoogleSTTWithV2Client(nil,
		WithGoogleSTTModel("chirp_3"),
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTKeywords(GoogleSTTKeyword{Value: "ignored", Boost: 1}),
		WithGoogleSTTAdaptationV2(adaptation),
	)

	config := googleStreamingRecognitionConfigV2(provider, "en-US", true)

	if config.GetConfig().GetAdaptation() != adaptation {
		t.Fatalf("v2 adaptation = %#v, want configured adaptation over keywords", config.GetConfig().GetAdaptation())
	}
}

func TestGoogleStreamingRecognitionConfigV2UsesReferenceDenoiserConfig(t *testing.T) {
	provider := newGoogleSTTWithV2Client(nil,
		WithGoogleSTTModel("chirp_3"),
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTDenoiserConfig(&speechv2pb.DenoiserConfig{
			DenoiseAudio: true,
			SnrThreshold: 8.5,
		}),
	)

	config := googleStreamingRecognitionConfigV2(provider, "en-US", true)

	denoiser := config.GetConfig().GetDenoiserConfig()
	if denoiser == nil {
		t.Fatal("v2 denoiser config = nil, want configured denoiser")
	}
	if !denoiser.GetDenoiseAudio() {
		t.Fatal("v2 denoise audio = false, want true")
	}
	if denoiser.GetSnrThreshold() != 8.5 {
		t.Fatalf("v2 SNR threshold = %v, want 8.5", denoiser.GetSnrThreshold())
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

func TestNewGoogleSTTRejectsReferenceAdaptationVersionMismatch(t *testing.T) {
	_, err := NewGoogleSTT("",
		WithGoogleSTTModel("chirp_3"),
		WithGoogleSTTAdaptation(&speechpb.SpeechAdaptation{}),
	)
	if err == nil || !strings.Contains(err.Error(), "adaptation must be cloud_speech_v2.SpeechAdaptation for v2 models") {
		t.Fatalf("v2 adaptation mismatch error = %v, want reference v2 adaptation type error", err)
	}

	_, err = NewGoogleSTT("",
		WithGoogleSTTModel("latest_long"),
		WithGoogleSTTAdaptationV2(&speechv2pb.SpeechAdaptation{}),
	)
	if err == nil || !strings.Contains(err.Error(), "adaptation must be resource_v1.SpeechAdaptation for v1 models") {
		t.Fatalf("v1 adaptation mismatch error = %v, want reference v1 adaptation type error", err)
	}
}

func TestGoogleSTTUpdateOptionsRejectsReferenceAdaptationVersionMismatch(t *testing.T) {
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{},
		WithGoogleSTTModel("latest_long"),
		WithGoogleSTTAdaptation(&speechpb.SpeechAdaptation{}),
	)

	err := provider.UpdateOptions(WithGoogleSTTModel("chirp_3"))
	if err == nil || !strings.Contains(err.Error(), "adaptation must be cloud_speech_v2.SpeechAdaptation for v2 models") {
		t.Fatalf("v2 update mismatch error = %v, want reference v2 adaptation type error", err)
	}
	if provider.model != "latest_long" {
		t.Fatalf("model after rejected v2 update = %q, want latest_long", provider.model)
	}

	provider = newGoogleSTTWithV2Client(&fakeGoogleV2SpeechClient{},
		WithGoogleSTTModel("chirp_3"),
		WithGoogleSTTAdaptationV2(&speechv2pb.SpeechAdaptation{}),
	)

	err = provider.UpdateOptions(WithGoogleSTTModel("latest_long"))
	if err == nil || !strings.Contains(err.Error(), "adaptation must be resource_v1.SpeechAdaptation for v1 models") {
		t.Fatalf("v1 update mismatch error = %v, want reference v1 adaptation type error", err)
	}
	if provider.model != "chirp_3" {
		t.Fatalf("model after rejected v1 update = %q, want chirp_3", provider.model)
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

func TestGoogleSTTPreservesReferenceExplicitZeroSampleRate(t *testing.T) {
	provider := newGoogleSTTWithClient(nil, WithGoogleSTTSampleRate(0))
	if got := provider.InputSampleRate(); got != 0 {
		t.Fatalf("InputSampleRate = %d, want explicit zero", got)
	}
	config := googleRecognitionConfig(provider, "en-US")
	if got := config.GetSampleRateHertz(); got != 0 {
		t.Fatalf("stream sample rate = %d, want explicit zero", got)
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
	if event.Alternatives[0].Language != "" {
		t.Fatalf("language = %q, want empty provider language", event.Alternatives[0].Language)
	}
}

func TestGoogleSTTRecognizeUsesReferenceV2RequestForV2Model(t *testing.T) {
	v1Client := &fakeGoogleSpeechClient{}
	v2Client := &fakeGoogleV2SpeechClient{
		recognizeResponse: &speechv2pb.RecognizeResponse{
			Results: []*speechv2pb.SpeechRecognitionResult{{
				Alternatives: []*speechv2pb.SpeechRecognitionAlternative{{
					Transcript: "hello from chirp",
					Confidence: 0.75,
				}},
				LanguageCode: "id-ID",
			}},
		},
	}
	provider := newGoogleSTTWithClient(v1Client,
		WithGoogleSTTModel("chirp_3"),
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTLocation("us-central1"),
		WithGoogleSTTLanguage("id-ID"),
	)
	provider.clientV2 = v2Client

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{
		{Data: []byte("one"), SampleRate: 24000, NumChannels: 2},
		{Data: []byte("two")},
	}, "")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}

	if v1Client.recognizeCalls != 0 {
		t.Fatalf("v1 recognize calls = %d, want 0 for v2 model", v1Client.recognizeCalls)
	}
	req := v2Client.recognizeRequest
	if req == nil {
		t.Fatal("v2 Recognize request = nil")
	}
	if got := req.GetRecognizer(); got != "projects/voice-project/locations/us-central1/recognizers/_" {
		t.Fatalf("recognizer = %q, want reference implicit recognizer", got)
	}
	if got := string(req.GetContent()); got != "onetwo" {
		t.Fatalf("v2 content = %q, want onetwo", got)
	}
	config := req.GetConfig()
	if config.GetExplicitDecodingConfig().GetSampleRateHertz() != 24000 || config.GetExplicitDecodingConfig().GetAudioChannelCount() != 2 {
		t.Fatalf("v2 decoding config = %+v, want 24 kHz stereo", config.GetExplicitDecodingConfig())
	}
	if got := config.GetLanguageCodes(); len(got) != 1 || got[0] != "id-ID" {
		t.Fatalf("v2 language codes = %#v, want id-ID", got)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %#v, want one final transcript", event)
	}
	if alt := event.Alternatives[0]; alt.Text != "hello from chirp" || alt.Language != "id-ID" || alt.Confidence != 0.75 {
		t.Fatalf("alternative = %+v, want v2 transcript/language/confidence", alt)
	}
}

func TestGoogleSTTRecognizeUsesReferenceTimingWhenLastResultHasNoWords(t *testing.T) {
	results := []*speechpb.SpeechRecognitionResult{
		{
			LanguageCode: "en-US",
			Alternatives: []*speechpb.SpeechRecognitionAlternative{{
				Transcript: "hello ",
				Confidence: 0.9,
				Words: []*speechpb.WordInfo{{
					Word:      "hello",
					StartTime: durationpb.New(100 * time.Millisecond),
					EndTime:   durationpb.New(500 * time.Millisecond),
				}},
			}},
		},
		{
			LanguageCode: "en-US",
			Alternatives: []*speechpb.SpeechRecognitionAlternative{{
				Transcript: "world",
				Confidence: 0.8,
			}},
		},
	}

	alternatives := googleSpeechDataFromRecognizeResults(results)

	if len(alternatives) != 1 {
		t.Fatalf("alternatives = %#v, want one final speech data", alternatives)
	}
	got := alternatives[0]
	if got.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", got.Text)
	}
	if got.StartTime != 0 || got.EndTime != 0 {
		t.Fatalf("timing = %v-%v, want zero timing like reference when last result has no words", got.StartTime, got.EndTime)
	}
	if len(got.Words) != 1 || got.Words[0].Text != "hello" {
		t.Fatalf("words = %#v, want first result words preserved", got.Words)
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

func TestGoogleSTTRecognizePreservesReferenceEmptyProviderLanguage(t *testing.T) {
	client := &fakeGoogleSpeechClient{
		recognizeResponse: &speechpb.RecognizeResponse{
			Results: []*speechpb.SpeechRecognitionResult{{
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
	if got := event.Alternatives[0].Language; got != "" {
		t.Fatalf("language = %q, want empty provider language", got)
	}
}

func TestGoogleSTTRecognizeV2PreservesReferenceEmptyProviderLanguage(t *testing.T) {
	v1Client := &fakeGoogleSpeechClient{}
	v2Client := &fakeGoogleV2SpeechClient{
		recognizeResponse: &speechv2pb.RecognizeResponse{
			Results: []*speechv2pb.SpeechRecognitionResult{{
				Alternatives: []*speechv2pb.SpeechRecognitionAlternative{{
					Transcript: "hello from chirp",
				}},
			}},
		},
	}
	provider := newGoogleSTTWithClient(v1Client,
		WithGoogleSTTModel("chirp_3"),
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTLanguage("id-ID"),
	)
	provider.clientV2 = v2Client

	event, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "id-ID")
	if err != nil {
		t.Fatalf("Recognize returned error: %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %#v, want one final transcript", event)
	}
	if got := event.Alternatives[0].Language; got != "" {
		t.Fatalf("language = %q, want empty provider language", got)
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
	if event.Alternatives[0].Language != "" {
		t.Fatalf("language = %q, want empty provider language", event.Alternatives[0].Language)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !streamClient.closed {
		t.Fatal("Close did not close streaming client")
	}
}

func TestGoogleSTTStreamPreservesReferenceEmptyProviderLanguage(t *testing.T) {
	tests := []struct {
		name      string
		isFinal   bool
		eventType stt.SpeechEventType
	}{
		{name: "interim", eventType: stt.SpeechEventInterimTranscript},
		{name: "final", isFinal: true, eventType: stt.SpeechEventFinalTranscript},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			streamClient := &fakeGoogleStreamingRecognizeClient{
				responses: []*speechpb.StreamingRecognizeResponse{{
					Results: []*speechpb.StreamingRecognitionResult{{
						IsFinal: tt.isFinal,
						Alternatives: []*speechpb.SpeechRecognitionAlternative{{
							Transcript: "streamed",
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

			event, err := stream.Next()
			if err != nil {
				t.Fatalf("Next returned error: %v", err)
			}
			if event.Type != tt.eventType || len(event.Alternatives) != 1 {
				t.Fatalf("event = %#v, want one %s transcript", event, tt.eventType)
			}
			if got := event.Alternatives[0].Language; got != "" {
				t.Fatalf("language = %q, want empty provider language", got)
			}
		})
	}
}

func TestGoogleSTTStreamPushFrameClonesReferenceAudio(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{}
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{stream: streamClient})

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	audio := []byte{1, 2, 3, 4}
	if err := stream.PushFrame(&model.AudioFrame{Data: audio, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 2}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	audio[0] = 9

	if len(streamClient.sent) != 2 {
		t.Fatalf("sent requests = %d, want config plus audio", len(streamClient.sent))
	}
	if got := streamClient.sent[1].GetAudioContent(); !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("audio content after caller mutation = %#v, want cloned audio", got)
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

func TestGoogleSTTStreamSuppressesEmptyFinalTranscriptWithWordsLikeReference(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal: true,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "",
					Confidence: 0.9,
					Words: []*speechpb.WordInfo{{
						Word:      "noise",
						StartTime: durationpb.New(100 * time.Millisecond),
						EndTime:   durationpb.New(200 * time.Millisecond),
					}},
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
		t.Fatalf("Next event = %#v, want nil for empty final transcript with words", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF after suppressed empty final with words", err)
	}
}

func TestGoogleSTTStreamSuppressesLaterInterimAfterEmptyFinalLikeReference(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			TotalBilledTime: durationpb.New(time.Second),
			Results: []*speechpb.StreamingRecognitionResult{
				{
					IsFinal: true,
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "",
						Confidence: 0.9,
					}},
				},
				{
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "late interim",
						Confidence: 0.9,
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

	if event != nil {
		t.Fatalf("Next event = %#v, want nil after empty final suppresses whole response including usage", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF after suppressed empty final response including usage", err)
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

func TestGoogleSTTUpdateOptionsDoesNotBlockOnReferenceReconnectClose(t *testing.T) {
	closeStarted := make(chan struct{})
	closeRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{
		recvBlock:    make(chan struct{}),
		closeBlock:   closeStarted,
		closeRelease: closeRelease,
	}
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: make(chan struct{})}
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

	updateErrCh := make(chan error, 1)
	go func() {
		updateErrCh <- provider.UpdateOptions(WithGoogleSTTMinConfidenceThreshold(0.5))
	}()

	select {
	case <-closeStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("old provider CloseSend did not start")
	}

	select {
	case err := <-updateErrCh:
		if err != nil {
			t.Fatalf("UpdateOptions returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		closeRelease <- struct{}{}
		<-updateErrCh
		t.Fatal("UpdateOptions blocked on old provider CloseSend")
	}

	closeRelease <- struct{}{}
	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want eventual reconnect", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for eventual reconnect")
	}
	close(firstStream.recvBlock)
	close(secondStream.recvBlock)
}

func TestGoogleSTTUpdateOptionsAppliesNegativeMinConfidence(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{
		recvBlock: secondRelease,
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "noise",
					Confidence: 0,
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

	provider.UpdateOptions(WithGoogleSTTMinConfidenceThreshold(-1))

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want reconnected stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for reconnected stream")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after negative min confidence update")
	}
	close(firstRelease)
	close(secondRelease)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if event == nil || event.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event = %#v, want interim transcript after negative min confidence", event)
	}
	if got := event.Alternatives[0].Text; got != "noise" {
		t.Fatalf("transcript = %q, want noise", got)
	}
}

func TestGoogleSTTUpdateOptionsReportsReferenceReconnectError(t *testing.T) {
	releaseFirst := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: releaseFirst}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream},
		streamErrs:   []error{nil, status.Error(codes.Unavailable, "restart failed")},
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

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next event = %#v, want nil reconnect error", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want reconnect APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.Unavailable) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.Unavailable)
	}
	close(releaseFirst)
}

func TestGoogleSTTUpdateOptionsNoopPreservesReferenceActiveStream(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh

	if err := provider.UpdateOptions(); err != nil {
		t.Fatalf("UpdateOptions returned error: %v", err)
	}
	select {
	case calls := <-client.streamCallCh:
		t.Fatalf("stream calls = %d, want no reconnect for no-op update", calls)
	case <-time.After(20 * time.Millisecond):
	}
	if firstStream.closed {
		t.Fatal("first stream closed = true, want active stream preserved for no-op update")
	}
	close(firstRelease)
}

func TestGoogleSTTUpdateOptionsReconnectsActiveStreamButOmitsV1SpeechTimeouts(t *testing.T) {
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
	provider.mu.Lock()
	storedEndTimeout := provider.speechEndTimeout
	provider.mu.Unlock()
	if storedEndTimeout != 750*time.Millisecond {
		t.Fatalf("stored speech end timeout = %v, want 750ms before v1 omission", storedEndTimeout)
	}

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
	config := secondStream.sent[0].GetStreamingConfig()
	if config.GetEnableVoiceActivityEvents() {
		t.Fatal("enable voice activity events = true, want false for reference v1 speech timeout update")
	}
	if timeout := config.GetVoiceActivityTimeout(); timeout != nil {
		t.Fatalf("second stream voice timeout = %#v, want nil for reference v1", timeout)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTUpdateOptionsKeepsV1SpeechTimeoutsOmittedAfterClear(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: secondRelease}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client, WithGoogleSTTSpeechEndTimeout(750*time.Millisecond))

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh
	if timeout := firstStream.sent[0].GetStreamingConfig().GetVoiceActivityTimeout(); timeout != nil {
		t.Fatalf("first stream voice timeout = %#v, want nil for reference v1", timeout)
	}

	provider.UpdateOptions(WithGoogleSTTSpeechEndTimeout(0), WithGoogleSTTSpeechStartTimeout(0))
	provider.mu.Lock()
	storedStartTimeout := provider.speechStartTimeout
	storedEndTimeout := provider.speechEndTimeout
	provider.mu.Unlock()
	if storedStartTimeout != 0 || storedEndTimeout != 0 {
		t.Fatalf("stored speech timeouts = start %v end %v, want cleared before v1 omission", storedStartTimeout, storedEndTimeout)
	}

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want reconnected stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for reconnected stream")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after timeout reset")
	}
	if len(secondStream.sent) != 1 {
		t.Fatalf("second stream sent = %#v, want fresh config", secondStream.sent)
	}
	if timeout := secondStream.sent[0].GetStreamingConfig().GetVoiceActivityTimeout(); timeout != nil {
		t.Fatalf("second stream voice timeout = %#v, want nil after explicit zero reset", timeout)
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

func TestGoogleSTTUpdateOptionsClearsReferenceAlternativeLanguages(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: secondRelease}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(
		client,
		WithGoogleSTTAlternativeLanguages("es-ES", "fr-FR"),
	)

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh
	if got := firstStream.sent[0].GetStreamingConfig().GetConfig().GetAlternativeLanguageCodes(); len(got) != 2 {
		t.Fatalf("first stream alternative languages = %#v, want configured candidates", got)
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
	config := secondStream.sent[0].GetStreamingConfig().GetConfig()
	if got := config.GetLanguageCode(); got != "id-ID" {
		t.Fatalf("second stream language = %q, want updated id-ID", got)
	}
	if got := config.GetAlternativeLanguageCodes(); len(got) != 0 {
		t.Fatalf("second stream alternative languages = %#v, want none after reference string language update", got)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTUpdateOptionsDetectLanguageKeepsReferenceActiveAlternatives(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: secondRelease}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(
		client,
		WithGoogleSTTDetectLanguage(false),
		WithGoogleSTTAlternativeLanguages("es-ES", "fr-FR"),
	)

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh
	if got := firstStream.sent[0].GetStreamingConfig().GetConfig().GetAlternativeLanguageCodes(); len(got) != 0 {
		t.Fatalf("first stream alternative languages = %#v, want none with detect_language disabled", got)
	}

	provider.UpdateOptions(WithGoogleSTTDetectLanguage(true))

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want reconnected stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for reconnected stream")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after detect language update")
	}
	config := secondStream.sent[0].GetStreamingConfig().GetConfig()
	if got := config.GetLanguageCode(); got != "en-US" {
		t.Fatalf("second stream language = %q, want original en-US", got)
	}
	if got := config.GetAlternativeLanguageCodes(); len(got) != 0 {
		t.Fatalf("second stream alternative languages = %#v, want none because reference active stream keeps sanitized language snapshot", got)
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

func TestGoogleSTTUpdateOptionsAppliesEmptyActiveStreamModel(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleStreamingRecognizeClient{recvBlock: secondRelease}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client, WithGoogleSTTModel("command_and_search"))

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh
	if got := firstStream.sent[0].GetStreamingConfig().GetConfig().GetModel(); got != "command_and_search" {
		t.Fatalf("first stream model = %q, want command_and_search", got)
	}

	provider.UpdateOptions(WithGoogleSTTModel(""))

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want reconnected stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for reconnected stream")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after model update")
	}
	if len(secondStream.sent) != 1 {
		t.Fatalf("second stream sent = %#v, want fresh config", secondStream.sent)
	}
	if got := secondStream.sent[0].GetStreamingConfig().GetConfig().GetModel(); got != "" {
		t.Fatalf("second stream model = %q, want explicit empty model", got)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTUpdateOptionsSwitchesActiveStreamToReferenceV2(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	v1Client := &fakeGoogleSpeechClient{
		stream:       firstStream,
		streamCallCh: make(chan int, 1),
	}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleV2StreamingRecognizeClient{recvBlock: secondRelease}
	v2Client := &fakeGoogleV2SpeechClient{
		stream:       secondStream,
		streamCallCh: make(chan int, 1),
	}
	provider := newGoogleSTTWithClient(v1Client,
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTLocation("us-central1"),
		WithGoogleSTTModel("latest_long"),
	)
	provider.clientV2 = v2Client

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-v1Client.streamCallCh
	if got := firstStream.sent[0].GetStreamingConfig().GetConfig().GetModel(); got != "latest_long" {
		t.Fatalf("first stream model = %q, want latest_long", got)
	}

	provider.UpdateOptions(WithGoogleSTTModel("chirp_3"))

	select {
	case calls := <-v2Client.streamCallCh:
		if calls != 1 {
			t.Fatalf("v2 stream calls = %d, want one v2 reconnect", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for v2 reconnect after model update")
	}
	if !firstStream.closed {
		t.Fatal("first v1 stream closed = false after model update")
	}
	if v1Client.streamCalls != 1 {
		t.Fatalf("v1 stream calls = %d, want no second v1 stream after v2 model update", v1Client.streamCalls)
	}
	if len(secondStream.sent) != 1 {
		t.Fatalf("second stream sent = %#v, want v2 config", secondStream.sent)
	}
	req := secondStream.sent[0]
	if got := req.GetRecognizer(); got != "projects/voice-project/locations/us-central1/recognizers/_" {
		t.Fatalf("v2 recognizer = %q, want reference implicit recognizer", got)
	}
	if got := req.GetStreamingConfig().GetConfig().GetModel(); got != "chirp_3" {
		t.Fatalf("v2 model = %q, want chirp_3", got)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTUpdateReconnectUsesLatestReferenceModel(t *testing.T) {
	oldV2 := &fakeGoogleV2StreamingRecognizeClient{recvBlock: make(chan struct{})}
	nextV2 := &fakeGoogleV2StreamingRecognizeClient{recvBlock: make(chan struct{})}
	v2Client := &fakeGoogleV2SpeechClient{
		streams:      []speechv2pb.Speech_StreamingRecognizeClient{nextV2},
		streamCallCh: make(chan int, 1),
	}
	staleV1 := &fakeGoogleStreamingRecognizeClient{}
	v1Client := &fakeGoogleSpeechClient{
		stream:       staleV1,
		streamCallCh: make(chan int, 1),
	}
	provider := newGoogleSTTWithClient(v1Client,
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTLocation("us-central1"),
		WithGoogleSTTModel("chirp_3"),
	)
	provider.clientV2 = v2Client
	googleStream := &googleSTTStream{
		owner:                       provider,
		ctx:                         context.Background(),
		streamV2:                    oldV2,
		language:                    "en-US",
		includeAlternativeLanguages: true,
		events:                      make(chan *stt.SpeechEvent, 1),
		errCh:                       make(chan error, 1),
	}

	if err := googleStream.reconnectForUpdatedConfig(); err != nil {
		t.Fatalf("reconnectForUpdatedConfig returned error: %v", err)
	}

	select {
	case calls := <-v2Client.streamCallCh:
		if calls != 1 {
			t.Fatalf("v2 stream calls = %d, want latest chirp_3 reconnect", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for latest chirp_3 reconnect")
	}
	if v1Client.streamCalls != 0 {
		t.Fatalf("v1 stream calls = %d, want no stale v1 reconnect", v1Client.streamCalls)
	}
	if !oldV2.closed {
		t.Fatal("old v2 stream closed = false after latest-model reconnect")
	}
	if len(nextV2.sent) != 1 || nextV2.sent[0].GetStreamingConfig().GetConfig().GetModel() != "chirp_3" {
		t.Fatalf("next v2 stream sent = %#v, want chirp_3 config", nextV2.sent)
	}
	close(oldV2.recvBlock)
	close(nextV2.recvBlock)
}

func TestGoogleSTTUpdateOptionsReplacesReferenceAdaptationOnVersionSwitch(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	v1Client := &fakeGoogleSpeechClient{
		stream:       firstStream,
		streamCallCh: make(chan int, 1),
	}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleV2StreamingRecognizeClient{recvBlock: secondRelease}
	v2Client := &fakeGoogleV2SpeechClient{
		stream:       secondStream,
		streamCallCh: make(chan int, 1),
	}
	v2Adaptation := &speechv2pb.SpeechAdaptation{
		PhraseSets: []*speechv2pb.SpeechAdaptation_AdaptationPhraseSet{{
			Value: &speechv2pb.SpeechAdaptation_AdaptationPhraseSet_InlinePhraseSet{
				InlinePhraseSet: &speechv2pb.PhraseSet{
					Phrases: []*speechv2pb.PhraseSet_Phrase{{Value: "Cavos"}},
				},
			},
		}},
	}
	provider := newGoogleSTTWithClient(v1Client,
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTLocation("us-central1"),
		WithGoogleSTTModel("latest_long"),
		WithGoogleSTTAdaptation(&speechpb.SpeechAdaptation{}),
	)
	provider.clientV2 = v2Client

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-v1Client.streamCallCh

	if err := provider.UpdateOptions(WithGoogleSTTModel("chirp_3"), WithGoogleSTTAdaptationV2(v2Adaptation)); err != nil {
		t.Fatalf("UpdateOptions returned error: %v", err)
	}

	select {
	case calls := <-v2Client.streamCallCh:
		if calls != 1 {
			t.Fatalf("v2 stream calls = %d, want one v2 reconnect", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for v2 reconnect after adaptation replacement")
	}
	if provider.adaptation != nil {
		t.Fatalf("v1 adaptation = %#v, want cleared after v2 adaptation update", provider.adaptation)
	}
	if provider.adaptationV2 != v2Adaptation {
		t.Fatalf("v2 adaptation = %#v, want replacement adaptation", provider.adaptationV2)
	}
	got := secondStream.sent[0].GetStreamingConfig().GetConfig().GetAdaptation()
	if got != v2Adaptation {
		t.Fatalf("v2 streaming adaptation = %#v, want replacement adaptation", got)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTUpdateOptionsCreatesReferenceV2ClientOnVersionSwitch(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{recvBlock: firstRelease}
	v1Client := &fakeGoogleSpeechClient{
		stream:       firstStream,
		streamCallCh: make(chan int, 1),
	}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleV2StreamingRecognizeClient{recvBlock: secondRelease}
	v2Client := &fakeGoogleV2SpeechClient{
		stream:       secondStream,
		streamCallCh: make(chan int, 1),
	}
	provider := newGoogleSTTWithClient(v1Client,
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTLocation("us-central1"),
		WithGoogleSTTModel("latest_long"),
	)
	var createCalls int
	provider.newClientV2 = func(context.Context) (googleSpeechV2Client, error) {
		createCalls++
		return v2Client, nil
	}

	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-v1Client.streamCallCh

	provider.UpdateOptions(WithGoogleSTTModel("chirp_3"))

	select {
	case calls := <-v2Client.streamCallCh:
		if calls != 1 {
			t.Fatalf("v2 stream calls = %d, want one v2 reconnect", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for v2 reconnect after lazy client creation")
	}
	if createCalls != 1 {
		t.Fatalf("v2 client create calls = %d, want 1", createCalls)
	}
	if !firstStream.closed {
		t.Fatal("first v1 stream closed = false after model update")
	}
	if got := secondStream.sent[0].GetStreamingConfig().GetConfig().GetModel(); got != "chirp_3" {
		t.Fatalf("v2 model = %q, want chirp_3", got)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTUpdateOptionsRecreatesReferenceV2ClientOnLocationChange(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleV2StreamingRecognizeClient{recvBlock: firstRelease}
	oldExtraStream := &fakeGoogleV2StreamingRecognizeClient{}
	oldClient := &fakeGoogleV2SpeechClient{
		streams:      []speechv2pb.Speech_StreamingRecognizeClient{firstStream, oldExtraStream},
		streamCallCh: make(chan int, 2),
	}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleV2StreamingRecognizeClient{recvBlock: secondRelease}
	newClient := &fakeGoogleV2SpeechClient{
		stream:       secondStream,
		streamCallCh: make(chan int, 1),
	}
	provider := newGoogleSTTWithV2Client(
		oldClient,
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTModel("chirp_3"),
	)
	createCalls := 0
	provider.newClientV2 = func(context.Context) (googleSpeechV2Client, error) {
		createCalls++
		return newClient, nil
	}

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-oldClient.streamCallCh

	if err := provider.UpdateOptions(WithGoogleSTTLocation("us-central1")); err != nil {
		t.Fatalf("UpdateOptions returned error: %v", err)
	}

	select {
	case calls := <-newClient.streamCallCh:
		if calls != 1 {
			t.Fatalf("new client stream calls = %d, want one reconnect", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for new v2 client reconnect after location update")
	}
	if createCalls != 1 {
		t.Fatalf("v2 client create calls = %d, want one after location update", createCalls)
	}
	if oldClient.streamCalls != 1 {
		t.Fatalf("old client stream calls = %d, want no reconnect on stale client", oldClient.streamCalls)
	}
	if got := secondStream.sent[0].GetRecognizer(); got != "projects/voice-project/locations/us-central1/recognizers/_" {
		t.Fatalf("reconnected recognizer = %q, want updated location", got)
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

func TestGoogleSTTStreamConfigOmitsReferenceV1SpeechTimeouts(t *testing.T) {
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
	provider.mu.Lock()
	storedStartTimeout := provider.speechStartTimeout
	storedEndTimeout := provider.speechEndTimeout
	provider.mu.Unlock()
	if storedStartTimeout != 1500*time.Millisecond || storedEndTimeout != 750*time.Millisecond {
		t.Fatalf("stored speech timeouts = start %v end %v, want configured before v1 omission", storedStartTimeout, storedEndTimeout)
	}
	if config.GetEnableVoiceActivityEvents() {
		t.Fatal("enable voice activity events = true, want false because reference v1 does not auto-enable for speech timeouts")
	}
	if timeout := config.GetVoiceActivityTimeout(); timeout != nil {
		t.Fatalf("voice activity timeout = %#v, want nil for reference v1", timeout)
	}
}

func TestGoogleSTTStreamUsesReferenceV2EndpointingConfig(t *testing.T) {
	streamClient := &fakeGoogleV2StreamingRecognizeClient{}
	provider := newGoogleSTTWithV2Client(
		&fakeGoogleV2SpeechClient{stream: streamClient},
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTLocation("us-central1"),
		WithGoogleSTTModel("chirp_3"),
		WithGoogleSTTSpeechStartTimeout(1500*time.Millisecond),
		WithGoogleSTTSpeechEndTimeout(750*time.Millisecond),
		WithGoogleSTTEndpointingSensitivity("ENDPOINTING_SENSITIVITY_SHORT"),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if len(streamClient.sent) != 1 {
		t.Fatalf("sent requests = %d, want initial v2 config", len(streamClient.sent))
	}
	req := streamClient.sent[0]
	if got := req.GetRecognizer(); got != "projects/voice-project/locations/us-central1/recognizers/_" {
		t.Fatalf("recognizer = %q, want reference implicit recognizer", got)
	}
	config := req.GetStreamingConfig()
	if config == nil {
		t.Fatal("streaming config = nil")
	}
	recognition := config.GetConfig()
	if recognition == nil {
		t.Fatal("recognition config = nil")
	}
	decoding := recognition.GetExplicitDecodingConfig()
	if decoding == nil {
		t.Fatal("explicit decoding config = nil")
	}
	if decoding.GetEncoding() != speechv2pb.ExplicitDecodingConfig_LINEAR16 || decoding.GetSampleRateHertz() != 16000 || decoding.GetAudioChannelCount() != 1 {
		t.Fatalf("decoding = %+v, want LINEAR16 16k mono", decoding)
	}
	features := recognition.GetFeatures()
	if features == nil {
		t.Fatal("recognition features = nil")
	}
	if features.GetEnableWordTimeOffsets() {
		t.Fatal("word time offsets = true, want disabled for chirp_3")
	}
	streaming := config.GetStreamingFeatures()
	if streaming == nil {
		t.Fatal("streaming features = nil")
	}
	if !streaming.GetInterimResults() {
		t.Fatal("interim results = false, want true")
	}
	if !streaming.GetEnableVoiceActivityEvents() {
		t.Fatal("voice activity events = false, want auto-enabled when v2 timeouts are set")
	}
	timeout := streaming.GetVoiceActivityTimeout()
	if timeout == nil {
		t.Fatal("voice activity timeout = nil")
	}
	if timeout.GetSpeechStartTimeout().AsDuration() != 1500*time.Millisecond {
		t.Fatalf("speech start timeout = %v, want 1.5s", timeout.GetSpeechStartTimeout().AsDuration())
	}
	if timeout.GetSpeechEndTimeout().AsDuration() != 750*time.Millisecond {
		t.Fatalf("speech end timeout = %v, want 750ms", timeout.GetSpeechEndTimeout().AsDuration())
	}
	if got := streaming.GetEndpointingSensitivity(); got != speechv2pb.StreamingRecognitionFeatures_ENDPOINTING_SENSITIVITY_SHORT {
		t.Fatalf("endpointing sensitivity = %v, want short", got)
	}
}

func TestGoogleSTTStreamV2ExplicitZeroTimeoutEnablesReferenceActivity(t *testing.T) {
	streamClient := &fakeGoogleV2StreamingRecognizeClient{}
	provider := newGoogleSTTWithV2Client(
		&fakeGoogleV2SpeechClient{stream: streamClient},
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTModel("chirp_3"),
		WithGoogleSTTSpeechEndTimeout(0),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if len(streamClient.sent) != 1 {
		t.Fatalf("sent requests = %d, want initial config", len(streamClient.sent))
	}
	streaming := streamClient.sent[0].GetStreamingConfig().GetStreamingFeatures()
	if streaming == nil {
		t.Fatal("streaming features = nil")
	}
	if !streaming.GetEnableVoiceActivityEvents() {
		t.Fatal("voice activity events = false, want auto-enabled for explicit zero v2 timeout")
	}
	timeout := streaming.GetVoiceActivityTimeout()
	if timeout == nil || timeout.SpeechEndTimeout == nil {
		t.Fatalf("voice activity timeout = %#v, want explicit zero speech_end_timeout", timeout)
	}
	if timeout.GetSpeechEndTimeout().AsDuration() != 0 {
		t.Fatalf("speech end timeout = %v, want explicit zero", timeout.GetSpeechEndTimeout().AsDuration())
	}
}

func TestGoogleSTTStreamIgnoresReferenceV2EndpointingForNonChirp3(t *testing.T) {
	streamClient := &fakeGoogleV2StreamingRecognizeClient{}
	provider := newGoogleSTTWithV2Client(
		&fakeGoogleV2SpeechClient{stream: streamClient},
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTModel("chirp_2"),
		WithGoogleSTTEndpointingSensitivity("ENDPOINTING_SENSITIVITY_SHORT"),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if len(streamClient.sent) != 1 {
		t.Fatalf("sent requests = %d, want initial v2 config", len(streamClient.sent))
	}
	streaming := streamClient.sent[0].GetStreamingConfig().GetStreamingFeatures()
	if streaming == nil {
		t.Fatal("streaming features = nil")
	}
	if got := streaming.GetEndpointingSensitivity(); got != speechv2pb.StreamingRecognitionFeatures_ENDPOINTING_SENSITIVITY_UNSPECIFIED {
		t.Fatalf("endpointing sensitivity = %v, want unspecified for non-chirp_3 model", got)
	}
}

func TestGoogleSTTUpdateOptionsDoesNotResurrectReferenceEndpointingForChirp3(t *testing.T) {
	firstRelease := make(chan struct{})
	firstStream := &fakeGoogleV2StreamingRecognizeClient{recvBlock: firstRelease}
	secondRelease := make(chan struct{})
	secondStream := &fakeGoogleV2StreamingRecognizeClient{recvBlock: secondRelease}
	client := &fakeGoogleV2SpeechClient{
		streams:      []speechv2pb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithV2Client(
		client,
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTModel("chirp_2"),
		WithGoogleSTTEndpointingSensitivity("ENDPOINTING_SENSITIVITY_SHORT"),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh

	provider.UpdateOptions(WithGoogleSTTModel("chirp_3"))

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want v2 reconnect", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for model update reconnect")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after model update")
	}
	streaming := secondStream.sent[0].GetStreamingConfig().GetStreamingFeatures()
	if got := streaming.GetEndpointingSensitivity(); got != speechv2pb.StreamingRecognitionFeatures_ENDPOINTING_SENSITIVITY_UNSPECIFIED {
		t.Fatalf("endpointing sensitivity = %v, want unspecified because reference ignored earlier non-chirp_3 option", got)
	}
	close(firstRelease)
	close(secondRelease)
}

func TestGoogleSTTStreamEmitsReferenceV2RecognitionUsage(t *testing.T) {
	streamClient := &fakeGoogleV2StreamingRecognizeClient{
		responses: []*speechv2pb.StreamingRecognizeResponse{
			{
				SpeechEventOffset: durationpb.New(400 * time.Millisecond),
				Metadata:          &speechv2pb.RecognitionResponseMetadata{RequestId: "v2-first"},
			},
			{
				Metadata: &speechv2pb.RecognitionResponseMetadata{
					RequestId:           "v2-final",
					TotalBilledDuration: durationpb.New(time.Second),
				},
			},
		},
	}
	provider := newGoogleSTTWithV2Client(
		&fakeGoogleV2SpeechClient{stream: streamClient},
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTModel("chirp_3"),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if first.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("first event type = %v, want recognition_usage", first.Type)
	}
	if first.RequestID != "v2-first" {
		t.Fatalf("first request id = %q, want v2-first", first.RequestID)
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
	if second.RequestID != "v2-final" {
		t.Fatalf("second request id = %q, want v2-final", second.RequestID)
	}
	if second.RecognitionUsage == nil || second.RecognitionUsage.AudioDuration != 0.6 {
		t.Fatalf("second usage = %+v, want 0.6s delta", second.RecognitionUsage)
	}
}

func TestGoogleSTTStreamResetsReferenceV2UsageAfterMaxSessionReconnect(t *testing.T) {
	firstStream := &fakeGoogleV2StreamingRecognizeClient{responses: []*speechv2pb.StreamingRecognizeResponse{
		{
			Metadata: &speechv2pb.RecognitionResponseMetadata{
				RequestId:           "first-usage",
				TotalBilledDuration: durationpb.New(time.Second),
			},
		},
		{
			Results: []*speechv2pb.StreamingRecognitionResult{{
				IsFinal:      true,
				LanguageCode: "en-US",
				Alternatives: []*speechv2pb.SpeechRecognitionAlternative{{
					Transcript: "done",
					Confidence: 0.91,
					Words: []*speechv2pb.WordInfo{{
						Word:        "done",
						StartOffset: durationpb.New(100 * time.Millisecond),
						EndOffset:   durationpb.New(300 * time.Millisecond),
						Confidence:  0.91,
					}},
				}},
			}},
		},
	}}
	secondStream := &fakeGoogleV2StreamingRecognizeClient{responses: []*speechv2pb.StreamingRecognizeResponse{{
		Metadata: &speechv2pb.RecognitionResponseMetadata{
			RequestId:           "second-usage",
			TotalBilledDuration: durationpb.New(500 * time.Millisecond),
		},
	}}}
	client := &fakeGoogleV2SpeechClient{
		streams:      []speechv2pb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithV2Client(
		client,
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTModel("chirp_3"),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	googleStream := stream.(*googleSTTStream)
	googleStream.mu.Lock()
	googleStream.sessionConnectedAt = time.Now().Add(-googleSTTMaxSessionDuration - time.Second)
	googleStream.mu.Unlock()
	<-client.streamCallCh

	first, err := stream.Next()
	if err != nil || first == nil || first.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("first event = (%#v, %v), want v2 recognition usage", first, err)
	}
	if first.RequestID != "first-usage" || first.RecognitionUsage == nil || first.RecognitionUsage.AudioDuration != 1 {
		t.Fatalf("first usage = %#v, want 1s from first stream", first)
	}
	final, err := stream.Next()
	if err != nil || final == nil || final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("second event = (%#v, %v), want final transcript", final, err)
	}

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want v2 max-session restart", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for v2 max-session restart")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after v2 max-session restart")
	}

	second, err := stream.Next()
	if err != nil || second == nil || second.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("third event = (%#v, %v), want fresh usage from restarted stream", second, err)
	}
	if second.RequestID != "second-usage" || second.RecognitionUsage == nil || second.RecognitionUsage.AudioDuration != 0.5 {
		t.Fatalf("second usage = %#v, want fresh 0.5s after v2 reconnect", second)
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

func TestGoogleSTTStreamResetsReferenceUsageAfterReconnect(t *testing.T) {
	releaseFirst := make(chan struct{})
	firstStream := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			TotalBilledTime: durationpb.New(time.Second),
			RequestId:       111,
		}},
		recvBlock: releaseFirst,
	}
	secondStream := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			TotalBilledTime: durationpb.New(500 * time.Millisecond),
			RequestId:       222,
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

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if first.Type != stt.SpeechEventRecognitionUsage || first.RecognitionUsage == nil || first.RecognitionUsage.AudioDuration != 1.0 {
		t.Fatalf("first usage = %#v, want 1s recognition_usage", first)
	}

	provider.UpdateOptions(WithGoogleSTTMinConfidenceThreshold(0.5))
	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want reconnect", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for reconnect")
	}
	close(releaseFirst)

	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if second.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("second event type = %v, want recognition_usage", second.Type)
	}
	if second.RequestID != "222" {
		t.Fatalf("second request id = %q, want 222", second.RequestID)
	}
	if second.RecognitionUsage == nil || second.RecognitionUsage.AudioDuration != 0.5 {
		t.Fatalf("second usage = %+v, want fresh 0.5s after reconnect", second.RecognitionUsage)
	}
}

func TestGoogleSTTStreamResetsReferenceV2UsageAfterUpdateReconnect(t *testing.T) {
	releaseFirst := make(chan struct{})
	firstStream := &fakeGoogleV2StreamingRecognizeClient{
		responses: []*speechv2pb.StreamingRecognizeResponse{{
			Metadata: &speechv2pb.RecognitionResponseMetadata{
				RequestId:           "first-usage",
				TotalBilledDuration: durationpb.New(time.Second),
			},
		}},
		recvBlock: releaseFirst,
	}
	secondStream := &fakeGoogleV2StreamingRecognizeClient{responses: []*speechv2pb.StreamingRecognizeResponse{{
		Metadata: &speechv2pb.RecognitionResponseMetadata{
			RequestId:           "second-usage",
			TotalBilledDuration: durationpb.New(500 * time.Millisecond),
		},
	}}}
	client := &fakeGoogleV2SpeechClient{
		streams:      []speechv2pb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithV2Client(
		client,
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTModel("chirp_3"),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh

	first, err := stream.Next()
	if err != nil || first == nil || first.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("first event = (%#v, %v), want v2 recognition usage", first, err)
	}
	if first.RequestID != "first-usage" || first.RecognitionUsage == nil || first.RecognitionUsage.AudioDuration != 1 {
		t.Fatalf("first usage = %#v, want 1s from first stream", first)
	}

	provider.UpdateOptions(WithGoogleSTTMinConfidenceThreshold(0.5))
	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want v2 update reconnect", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for v2 update reconnect")
	}
	close(releaseFirst)

	second, err := stream.Next()
	if err != nil || second == nil || second.Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("second event = (%#v, %v), want fresh v2 usage from updated stream", second, err)
	}
	if second.RequestID != "second-usage" || second.RecognitionUsage == nil || second.RecognitionUsage.AudioDuration != 0.5 {
		t.Fatalf("second usage = %#v, want fresh 0.5s after v2 update reconnect", second)
	}
}

func TestGoogleSTTStreamRestartsV2AfterReference409(t *testing.T) {
	firstStream := &fakeGoogleV2StreamingRecognizeClient{recvErr: status.Error(codes.AlreadyExists, "stream conflict")}
	restartedRecv := make(chan struct{})
	secondStream := &fakeGoogleV2StreamingRecognizeClient{recvBlock: restartedRecv}
	client := &fakeGoogleV2SpeechClient{
		streams:      []speechv2pb.Speech_StreamingRecognizeClient{firstStream, secondStream},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithV2Client(
		client,
		WithGoogleSTTProject("voice-project"),
		WithGoogleSTTModel("chirp_3"),
	)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want v2 restarted stream", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for v2 restarted stream")
	}
	if !firstStream.closed {
		t.Fatal("first v2 stream closed = false after restart")
	}
	if len(secondStream.sent) != 1 || secondStream.sent[0].GetStreamingConfig() == nil {
		t.Fatalf("second v2 stream sent = %#v, want fresh config", secondStream.sent)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("second"), SampleRate: 16000}); err != nil {
		t.Fatalf("PushFrame after v2 restart returned error: %v", err)
	}
	if len(secondStream.sent) != 2 || string(secondStream.sent[1].GetAudio()) != "second" {
		t.Fatalf("second v2 stream sent = %#v, want later audio on restarted stream", secondStream.sent)
	}
	close(restartedRecv)
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
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("again")}); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("PushFrame after provider Close error = %v, want input ended", err)
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

func TestGoogleSTTStreamReportsReference409ReconnectError(t *testing.T) {
	firstStream := &fakeGoogleStreamingRecognizeClient{recvErr: status.Error(codes.AlreadyExists, "stream conflict")}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream},
		streamErrs:   []error{nil, status.Error(codes.Unavailable, "restart failed")},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	<-client.streamCallCh

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next event = %#v, want nil reconnect error", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want reconnect APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.Unavailable) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.Unavailable)
	}
}

func TestGoogleSTTStreamRestartsAfterReferenceMaxSessionFinal(t *testing.T) {
	firstStream := &fakeGoogleStreamingRecognizeClient{responses: []*speechpb.StreamingRecognizeResponse{
		{SpeechEventType: speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_BEGIN},
		{
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal:      true,
				LanguageCode: "en-US",
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "done",
					Confidence: 0.91,
					Words: []*speechpb.WordInfo{{
						Word:       "done",
						StartTime:  durationpb.New(100 * time.Millisecond),
						EndTime:    durationpb.New(300 * time.Millisecond),
						Confidence: 0.91,
					}},
				}},
			}},
		},
	}}
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
	googleStream := stream.(*googleSTTStream)
	googleStream.mu.Lock()
	googleStream.sessionConnectedAt = time.Now().Add(-googleSTTMaxSessionDuration - time.Second)
	googleStream.mu.Unlock()
	<-client.streamCallCh

	event, err := stream.Next()
	if err != nil || event == nil || event.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event = (%#v, %v), want start of speech", event, err)
	}
	event, err = stream.Next()
	if err != nil || event == nil || event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 || event.Alternatives[0].Text != "done" {
		t.Fatalf("second event = (%#v, %v), want final transcript", event, err)
	}
	event, err = stream.Next()
	if err != nil || event == nil || event.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("third event = (%#v, %v), want end of speech before reconnect", event, err)
	}

	select {
	case calls := <-client.streamCallCh:
		if calls != 2 {
			t.Fatalf("stream calls = %d, want max-session restart", calls)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for max-session restart")
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after max-session restart")
	}
	if len(secondStream.sent) != 1 || secondStream.sent[0].GetStreamingConfig() == nil {
		t.Fatalf("second stream sent = %#v, want fresh config", secondStream.sent)
	}
	close(restartedRecv)
}

func TestGoogleSTTStreamReportsReferenceMaxSessionReconnectError(t *testing.T) {
	firstStream := &fakeGoogleStreamingRecognizeClient{responses: []*speechpb.StreamingRecognizeResponse{
		{SpeechEventType: speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_BEGIN},
		{
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal:      true,
				LanguageCode: "en-US",
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "done",
					Confidence: 0.91,
				}},
			}},
		},
	}}
	client := &fakeGoogleSpeechClient{
		streams:      []speechpb.Speech_StreamingRecognizeClient{firstStream},
		streamErrs:   []error{nil, status.Error(codes.Unavailable, "restart failed")},
		streamCallCh: make(chan int, 2),
	}
	provider := newGoogleSTTWithClient(client)

	stream, err := provider.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	googleStream := stream.(*googleSTTStream)
	googleStream.mu.Lock()
	googleStream.sessionConnectedAt = time.Now().Add(-googleSTTMaxSessionDuration - time.Second)
	googleStream.mu.Unlock()
	<-client.streamCallCh

	if event, err := stream.Next(); err != nil || event == nil || event.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event = (%#v, %v), want start of speech", event, err)
	}
	if event, err := stream.Next(); err != nil || event == nil || event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("second event = (%#v, %v), want final transcript", event, err)
	}
	if event, err := stream.Next(); err != nil || event == nil || event.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("third event = (%#v, %v), want end of speech before reconnect error", event, err)
	}

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("fourth event = %#v, want nil reconnect error", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("fourth Next error = %T %v, want reconnect APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.Unavailable) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.Unavailable)
	}
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

func TestGoogleSTTReadLoopIgnoresLateTranscriptAfterCloseLikeReference(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{{
				IsFinal: true,
				Alternatives: []*speechpb.SpeechRecognitionAlternative{{
					Transcript: "stale final",
				}},
			}},
		}},
	}
	stream := &googleSTTStream{
		stream: streamClient,
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
	}
	stream.events <- &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		stream.readLoop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Millisecond):
		<-stream.events
		<-done
		t.Fatal("readLoop blocked on late transcript after Close")
	}
}

func TestGoogleSTTReadLoopIgnoresLateReceiveErrorAfterCloseLikeReference(t *testing.T) {
	stream := &googleSTTStream{
		stream: &fakeGoogleStreamingRecognizeClient{
			recvErr: status.Error(codes.Unavailable, "late failure"),
		},
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
	}
	stream.errCh <- errors.New("older error")
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		stream.readLoop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Millisecond):
		<-stream.errCh
		<-done
		t.Fatal("readLoop blocked on late receive error after Close")
	}
}

func TestGoogleSTTStreamReportsInputEndedAfterCloseLikeReference(t *testing.T) {
	stream := &googleSTTStream{
		stream: &fakeGoogleStreamingRecognizeClient{},
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	checkInputEnded := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "input ended") {
			t.Fatalf("%s after Close error = %v, want input ended", name, err)
		}
	}
	checkInputEnded("PushFrame", stream.PushFrame(&model.AudioFrame{
		Data:              []byte{1, 2},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}))
	checkInputEnded("Flush", stream.Flush())
	checkInputEnded("EndInput", stream.EndInput())
}

func TestGoogleSTTStreamCloseUnblocksBlockedReferenceSend(t *testing.T) {
	sendStarted := make(chan struct{})
	sendRelease := make(chan struct{})
	streamClient := &fakeGoogleStreamingRecognizeClient{
		sendBlock:   sendStarted,
		sendRelease: sendRelease,
	}
	stream := &googleSTTStream{
		owner:  &GoogleSTT{sampleRate: 16000},
		stream: streamClient,
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
	}

	pushErrCh := make(chan error, 1)
	go func() {
		pushErrCh <- stream.PushFrame(&model.AudioFrame{
			Data:              []byte{1, 2},
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		})
	}()

	select {
	case <-sendStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider Send did not start")
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- stream.Close()
	}()

	select {
	case err := <-closeErrCh:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(sendRelease)
		<-pushErrCh
		<-closeErrCh
		t.Fatal("Close did not unblock blocked provider Send")
	}

	close(sendRelease)
	if err := <-pushErrCh; err == nil {
		t.Fatal("PushFrame error = nil, want closed send error after Close")
	}
	if !streamClient.closed {
		t.Fatal("provider stream closed = false")
	}
}

func TestGoogleSTTStreamCloseUnblocksBlockedReferenceEndInput(t *testing.T) {
	closeStarted := make(chan struct{})
	closeRelease := make(chan struct{})
	streamClient := &fakeGoogleStreamingRecognizeClient{
		closeBlock:   closeStarted,
		closeRelease: closeRelease,
	}
	stream := &googleSTTStream{
		owner:  &GoogleSTT{sampleRate: 16000},
		stream: streamClient,
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
	}

	endErrCh := make(chan error, 1)
	go func() {
		endErrCh <- stream.EndInput()
	}()

	select {
	case <-closeStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider CloseSend did not start")
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- stream.Close()
	}()

	select {
	case err := <-closeErrCh:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		closeRelease <- struct{}{}
		<-endErrCh
		<-closeErrCh
		t.Fatal("Close did not unblock blocked provider CloseSend")
	}

	if err := <-endErrCh; err == nil {
		t.Fatal("EndInput error = nil, want closed send error after Close")
	}
	if !streamClient.closed {
		t.Fatal("provider stream closed = false")
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
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte("again")}); err == nil || !strings.Contains(err.Error(), "input ended") {
		t.Fatalf("PushFrame after rejected registration error = %v, want input ended", err)
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
	streamErrs        []error
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
	err := c.streamErr
	if len(c.streamErrs) > 0 {
		err = c.streamErrs[0]
		c.streamErrs = c.streamErrs[1:]
	}
	if len(c.streams) > 0 {
		stream := c.streams[0]
		c.streams = c.streams[1:]
		return stream, err
	}
	return c.stream, err
}

func (c *fakeGoogleSpeechClient) Recognize(ctx context.Context, req *speechpb.RecognizeRequest, opts ...gax.CallOption) (*speechpb.RecognizeResponse, error) {
	c.recognizeCalls++
	c.recognizeRequest = req
	return c.recognizeResponse, c.recognizeErr
}

type fakeGoogleV2SpeechClient struct {
	streams           []speechv2pb.Speech_StreamingRecognizeClient
	stream            speechv2pb.Speech_StreamingRecognizeClient
	streamCallCh      chan int
	streamCalls       int
	streamErr         error
	recognizeRequest  *speechv2pb.RecognizeRequest
	recognizeCalls    int
	recognizeResponse *speechv2pb.RecognizeResponse
	recognizeErr      error
}

func (c *fakeGoogleV2SpeechClient) StreamingRecognize(ctx context.Context, opts ...gax.CallOption) (speechv2pb.Speech_StreamingRecognizeClient, error) {
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

func (c *fakeGoogleV2SpeechClient) Recognize(ctx context.Context, req *speechv2pb.RecognizeRequest, opts ...gax.CallOption) (*speechv2pb.RecognizeResponse, error) {
	c.recognizeCalls++
	c.recognizeRequest = req
	return c.recognizeResponse, c.recognizeErr
}

type fakeGoogleV2StreamingRecognizeClient struct {
	sent      []*speechv2pb.StreamingRecognizeRequest
	responses []*speechv2pb.StreamingRecognizeResponse
	recvIndex int
	recvBlock chan struct{}
	recvErr   error
	closed    bool
	closeErr  error
}

func (c *fakeGoogleV2StreamingRecognizeClient) Send(req *speechv2pb.StreamingRecognizeRequest) error {
	c.sent = append(c.sent, req)
	return nil
}

func (c *fakeGoogleV2StreamingRecognizeClient) Recv() (*speechv2pb.StreamingRecognizeResponse, error) {
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

func (c *fakeGoogleV2StreamingRecognizeClient) CloseSend() error {
	c.closed = true
	return c.closeErr
}

func (c *fakeGoogleV2StreamingRecognizeClient) Header() (metadata.MD, error) {
	return nil, nil
}
func (c *fakeGoogleV2StreamingRecognizeClient) Trailer() metadata.MD     { return nil }
func (c *fakeGoogleV2StreamingRecognizeClient) Context() context.Context { return context.Background() }
func (c *fakeGoogleV2StreamingRecognizeClient) SendMsg(m any) error      { return nil }
func (c *fakeGoogleV2StreamingRecognizeClient) RecvMsg(m any) error      { return nil }

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
	sendBlock          chan struct{}
	sendRelease        chan struct{}
	closeBlock         chan struct{}
	closeRelease       chan struct{}
}

func (c *fakeGoogleStreamingRecognizeClient) Send(req *speechpb.StreamingRecognizeRequest) error {
	c.sent = append(c.sent, req)
	if req.GetStreamingConfig() != nil && c.sendErrOnConfig != nil {
		return c.sendErrOnConfig
	}
	if req.GetAudioContent() != nil && c.sendErrAfterConfig != nil {
		return c.sendErrAfterConfig
	}
	if req.GetAudioContent() != nil && c.sendBlock != nil {
		close(c.sendBlock)
		<-c.sendRelease
		if c.closed {
			return io.ErrClosedPipe
		}
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
	if c.closeBlock != nil {
		closeBlock := c.closeBlock
		c.closeBlock = nil
		select {
		case closeBlock <- struct{}{}:
			<-c.closeRelease
		case c.closeRelease <- struct{}{}:
		}
	} else if c.closeRelease != nil {
		select {
		case c.closeRelease <- struct{}{}:
		default:
		}
	}
	if c.closed {
		return io.ErrClosedPipe
	}
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
