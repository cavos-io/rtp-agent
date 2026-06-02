package google

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	texttospeech "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/googleapis/gax-go/v2"
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
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities = %#v, want non-streaming without aligned transcript", caps)
	}
}

func TestGoogleTTSStreamReturnsUnsupportedError(t *testing.T) {
	_, err := newGoogleTTSWithClient(nil).Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want unsupported streaming error")
	}
	if !strings.Contains(err.Error(), "streaming tts input not yet implemented") {
		t.Fatalf("Stream error = %q, want unsupported streaming error", err.Error())
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
	err      error
}

func (c *fakeGoogleTTSClient) SynthesizeSpeech(ctx context.Context, req *texttospeech.SynthesizeSpeechRequest, opts ...gax.CallOption) (*texttospeech.SynthesizeSpeechResponse, error) {
	c.request = req
	return c.response, c.err
}
