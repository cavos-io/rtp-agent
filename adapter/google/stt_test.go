package google

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"

	"cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/metadata"
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
	if !config.EnableAutomaticPunctuation {
		t.Fatal("expected automatic punctuation to be enabled")
	}
	if !config.EnableWordTimeOffsets {
		t.Fatal("expected word time offsets to be enabled")
	}
	if !config.EnableWordConfidence {
		t.Fatal("expected word confidence to be enabled")
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
	provider := &GoogleSTT{}

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
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
		t.Fatalf("words = %#v, want first-result word details", got.Words)
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

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript || event.Alternatives[0].Text != "streamed" {
		t.Fatalf("event = %#v, want final streamed transcript", event)
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
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !streamClient.closed {
		t.Fatal("Close did not close streaming client")
	}
}

func TestGoogleSTTStreamCombinesReferenceInterimResultSegments(t *testing.T) {
	streamClient := &fakeGoogleStreamingRecognizeClient{
		responses: []*speechpb.StreamingRecognizeResponse{{
			Results: []*speechpb.StreamingRecognitionResult{
				{
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "hello ",
						Confidence: 0.8,
					}},
				},
				{
					Alternatives: []*speechpb.SpeechRecognitionAlternative{{
						Transcript: "world",
						Confidence: 0.6,
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

	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want EOF after one combined event", err)
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

func TestGoogleSTTStreamPropagatesClientErrors(t *testing.T) {
	wantErr := errors.New("stream error")
	provider := newGoogleSTTWithClient(&fakeGoogleSpeechClient{streamErr: wantErr})

	_, err := provider.Stream(context.Background(), "")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Stream error = %v, want %v", err, wantErr)
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

type fakeGoogleSpeechClient struct {
	stream            speechpb.Speech_StreamingRecognizeClient
	streamErr         error
	recognizeRequest  *speechpb.RecognizeRequest
	recognizeResponse *speechpb.RecognizeResponse
	recognizeErr      error
}

func (c *fakeGoogleSpeechClient) StreamingRecognize(ctx context.Context, opts ...gax.CallOption) (speechpb.Speech_StreamingRecognizeClient, error) {
	return c.stream, c.streamErr
}

func (c *fakeGoogleSpeechClient) Recognize(ctx context.Context, req *speechpb.RecognizeRequest, opts ...gax.CallOption) (*speechpb.RecognizeResponse, error) {
	c.recognizeRequest = req
	return c.recognizeResponse, c.recognizeErr
}

type fakeGoogleStreamingRecognizeClient struct {
	sent               []*speechpb.StreamingRecognizeRequest
	responses          []*speechpb.StreamingRecognizeResponse
	recvIndex          int
	closed             bool
	sendErrAfterConfig error
}

func (c *fakeGoogleStreamingRecognizeClient) Send(req *speechpb.StreamingRecognizeRequest) error {
	c.sent = append(c.sent, req)
	if req.GetAudioContent() != nil && c.sendErrAfterConfig != nil {
		return c.sendErrAfterConfig
	}
	return nil
}

func (c *fakeGoogleStreamingRecognizeClient) Recv() (*speechpb.StreamingRecognizeResponse, error) {
	if c.recvIndex >= len(c.responses) {
		return nil, io.EOF
	}
	resp := c.responses[c.recvIndex]
	c.recvIndex++
	return resp, nil
}

func (c *fakeGoogleStreamingRecognizeClient) CloseSend() error {
	c.closed = true
	return nil
}

func (c *fakeGoogleStreamingRecognizeClient) Header() (metadata.MD, error) { return nil, nil }
func (c *fakeGoogleStreamingRecognizeClient) Trailer() metadata.MD         { return nil }
func (c *fakeGoogleStreamingRecognizeClient) Context() context.Context     { return context.Background() }
func (c *fakeGoogleStreamingRecognizeClient) SendMsg(m any) error          { return nil }
func (c *fakeGoogleStreamingRecognizeClient) RecvMsg(m any) error          { return nil }
