package google

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	texttospeech "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestNewGoogleTTSRejectsMissingCredentialsFile(t *testing.T) {
	_, err := NewGoogleTTS("/definitely/missing/google-credentials.json")
	if err == nil {
		t.Fatal("NewGoogleTTS returned nil error, want missing credentials error")
	}
}

func TestGoogleTTSMetadata(t *testing.T) {
	provider := newGoogleTTSWithClient(nil)

	if got := provider.Label(); got != "google.TTS" {
		t.Fatalf("Label = %q, want google.TTS", got)
	}
	if got := provider.SampleRate(); got != 24000 {
		t.Fatalf("SampleRate = %d, want 24000", got)
	}
	if got := provider.NumChannels(); got != 1 {
		t.Fatalf("NumChannels = %d, want 1", got)
	}
	if got := provider.Model(); got != "gemini-2.5-flash-tts" {
		t.Fatalf("Model = %q, want gemini-2.5-flash-tts", got)
	}
	if got := provider.Provider(); got != "Google Cloud Platform" {
		t.Fatalf("Provider = %q, want Google Cloud Platform", got)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities = %#v, want reference streaming without aligned transcript", caps)
	}
}

func TestGoogleTTSStreamSendsReferenceConfigAndInput(t *testing.T) {
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{
				AudioContent: []byte{1, 2, 3, 4},
			}},
		},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSLanguage("id-ID"),
		WithGoogleTTSVoice("id-ID-Standard-A"),
		WithGoogleTTSModel("gemini-custom"),
	)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if !provider.Capabilities().Streaming {
		t.Fatal("Capabilities().Streaming = false, want true like reference")
	}
	if err := stream.PushText("halo"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	if len(client.stream.sent) != 2 {
		t.Fatalf("sent requests = %d, want config and input", len(client.stream.sent))
	}
	config := client.stream.sent[0].GetStreamingConfig()
	if config == nil {
		t.Fatal("first request has nil streaming_config")
	}
	if config.GetVoice().GetLanguageCode() != "id-ID" || config.GetVoice().GetName() != "id-ID-Standard-A" || config.GetVoice().GetModelName() != "gemini-custom" {
		t.Fatalf("streaming voice = %+v, want configured voice", config.GetVoice())
	}
	if config.GetStreamingAudioConfig().GetSampleRateHertz() != 24000 || config.GetStreamingAudioConfig().GetAudioEncoding() != texttospeech.AudioEncoding_LINEAR16 {
		t.Fatalf("audio config = %+v, want LINEAR16 24 kHz", config.GetStreamingAudioConfig())
	}
	if got := client.stream.sent[1].GetInput().GetText(); got != "halo" {
		t.Fatalf("input text = %q, want halo", got)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %v, want response bytes", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame = %+v, want 24 kHz mono samples", audio.Frame)
	}
}

func TestGoogleTTSSynthesizeRequestUsesReferenceDefaults(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := client.request
	if req == nil {
		t.Fatal("SynthesizeSpeech request = nil")
	}
	if got := req.GetVoice().GetLanguageCode(); got != "en-US" {
		t.Fatalf("voice language = %q, want en-US", got)
	}
	if got := req.GetVoice().GetName(); got != "Charon" {
		t.Fatalf("voice name = %q, want Charon", got)
	}
	if got := req.GetVoice().GetModelName(); got != "gemini-2.5-flash-tts" {
		t.Fatalf("voice model = %q, want gemini-2.5-flash-tts", got)
	}
	if got := req.GetAudioConfig().GetAudioEncoding(); got != texttospeech.AudioEncoding_LINEAR16 {
		t.Fatalf("audio encoding = %v, want LINEAR16", got)
	}
	if got := req.GetAudioConfig().GetSampleRateHertz(); got != 24000 {
		t.Fatalf("sample rate = %d, want 24000", got)
	}
	if got := req.GetAudioConfig().GetSpeakingRate(); got != 1.0 {
		t.Fatalf("speaking rate = %v, want 1.0", got)
	}
}

func TestGoogleTTSOptionsOverrideReferenceVoiceFields(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSLanguage("id-ID"),
		WithGoogleTTSVoice("id-ID-Standard-A"),
		WithGoogleTTSModel("gemini-custom"),
	)

	stream, err := provider.Synthesize(context.Background(), "halo")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	voice := client.request.GetVoice()
	if voice.GetLanguageCode() != "id-ID" || voice.GetName() != "id-ID-Standard-A" || voice.GetModelName() != "gemini-custom" {
		t.Fatalf("voice = %+v, want configured language, voice, and model", voice)
	}
}

func TestGoogleTTSUpdateOptionsMatchesReference(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client)

	provider.UpdateOptions(
		WithGoogleTTSLanguage("id-ID"),
		WithGoogleTTSVoice("id-ID-Standard-A"),
		WithGoogleTTSModel("gemini-custom"),
	)

	stream, err := provider.Synthesize(context.Background(), "halo")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	voice := client.request.GetVoice()
	if voice.GetLanguageCode() != "id-ID" || voice.GetName() != "id-ID-Standard-A" || voice.GetModelName() != "gemini-custom" {
		t.Fatalf("voice = %+v, want updated language, voice, and model", voice)
	}
}

func TestGoogleTTSUpdateOptionsPreservesExistingVoiceFields(t *testing.T) {
	provider := newGoogleTTSWithClient(nil,
		WithGoogleTTSLanguage("id-ID"),
		WithGoogleTTSModel("gemini-custom"),
	)

	provider.UpdateOptions(WithGoogleTTSVoice("id-ID-Standard-B"))

	if provider.voice.GetLanguageCode() != "id-ID" || provider.voice.GetName() != "id-ID-Standard-B" || provider.voice.GetModelName() != "gemini-custom" {
		t.Fatalf("voice = %+v, want updated voice with existing language and model", provider.voice)
	}
}

func TestGoogleTTSPromptMatchesReferenceRequests(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: []byte{5, 6}}},
		},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSPrompt("speak warmly"))

	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	if got := client.request.GetInput().GetPrompt(); got != "speak warmly" {
		t.Fatalf("synthesize prompt = %q, want speak warmly", got)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if got := client.stream.sent[1].GetInput().GetPrompt(); got != "speak warmly" {
		t.Fatalf("stream prompt = %q, want speak warmly on first input", got)
	}
}

func TestGoogleTTSSpeakingRateMatchesReferenceRequests(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: []byte{5, 6}}},
		},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSSpeakingRate(1.25))

	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	if got := client.request.GetAudioConfig().GetSpeakingRate(); got != 1.25 {
		t.Fatalf("synthesize speaking rate = %v, want 1.25", got)
	}

	provider.UpdateOptions(WithGoogleTTSSpeakingRate(0.8))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if got := client.stream.sent[0].GetStreamingConfig().GetStreamingAudioConfig().GetSpeakingRate(); got != 0.8 {
		t.Fatalf("stream speaking rate = %v, want 0.8", got)
	}
}

func TestGoogleTTSChirp3OmitsModelName(t *testing.T) {
	provider := newGoogleTTSWithClient(nil, WithGoogleTTSModel("chirp_3"))

	if got := provider.voice.GetModelName(); got != "" {
		t.Fatalf("model name = %q, want omitted for chirp_3", got)
	}
}

func TestGoogleTTSSynthesizeStripsWAVHeaderAndChunksAudio(t *testing.T) {
	payload := append(make([]byte, 44), []byte{1, 2, 3, 4}...)
	copy(payload[0:4], "RIFF")
	copy(payload[8:12], "WAVE")
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: payload},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	if client.request == nil || client.request.GetInput().GetText() != "hello" {
		t.Fatalf("request = %#v, want hello text input", client.request)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Frame.Data; !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("chunk data = %v, want stripped payload", got)
	}
	if chunk.Frame.SampleRate != 24000 || chunk.Frame.NumChannels != 1 || chunk.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame = %#v, want 24k mono 2 samples", chunk.Frame)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want io.EOF", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

type fakeGoogleTTSClient struct {
	request  *texttospeech.SynthesizeSpeechRequest
	response *texttospeech.SynthesizeSpeechResponse
	stream   *fakeGoogleTTSStream
	err      error
}

func (c *fakeGoogleTTSClient) SynthesizeSpeech(ctx context.Context, req *texttospeech.SynthesizeSpeechRequest, opts ...gax.CallOption) (*texttospeech.SynthesizeSpeechResponse, error) {
	c.request = req
	return c.response, c.err
}

func (c *fakeGoogleTTSClient) StreamingSynthesize(ctx context.Context, opts ...gax.CallOption) (texttospeech.TextToSpeech_StreamingSynthesizeClient, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.stream, nil
}

type fakeGoogleTTSStream struct {
	grpc.ClientStream
	sent      []*texttospeech.StreamingSynthesizeRequest
	responses []*texttospeech.StreamingSynthesizeResponse
	closed    bool
}

func (s *fakeGoogleTTSStream) Send(req *texttospeech.StreamingSynthesizeRequest) error {
	s.sent = append(s.sent, req)
	return nil
}

func (s *fakeGoogleTTSStream) Recv() (*texttospeech.StreamingSynthesizeResponse, error) {
	if len(s.responses) == 0 {
		return nil, io.EOF
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	return resp, nil
}

func (s *fakeGoogleTTSStream) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeGoogleTTSStream) Trailer() metadata.MD         { return nil }
func (s *fakeGoogleTTSStream) CloseSend() error {
	s.closed = true
	return nil
}
func (s *fakeGoogleTTSStream) Context() context.Context { return context.Background() }
func (s *fakeGoogleTTSStream) SendMsg(m any) error      { return nil }
func (s *fakeGoogleTTSStream) RecvMsg(m any) error      { return nil }
