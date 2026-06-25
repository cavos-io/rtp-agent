package clova

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type clovaCloseCountBody struct {
	closed bool
}

func (b *clovaCloseCountBody) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (b *clovaCloseCountBody) Close() error {
	if b.closed {
		return errors.New("already closed")
	}
	b.closed = true
	return nil
}

func TestClovaTTSDefaultsAndSynthesizeRequest(t *testing.T) {
	var gotMethod string
	var gotURL string
	var gotHeaders http.Header
	var gotForm url.Values
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: clovaTTSRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotMethod = req.Method
		gotURL = req.URL.String()
		gotHeaders = req.Header.Clone()
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		gotForm, err = url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("parse request body: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	})}

	provider := NewClovaTTS("client-id", "client-secret", "")
	if got, want := provider.Label(), "clova.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := provider.voice, "nara"; got != want {
		t.Fatalf("voice = %q, want default %q", got, want)
	}
	if got, want := provider.SampleRate(), 24000; got != want {
		t.Fatalf("SampleRate() = %d, want %d", got, want)
	}
	if got, want := provider.NumChannels(), 1; got != want {
		t.Fatalf("NumChannels() = %d, want %d", got, want)
	}
	if caps := provider.Capabilities(); caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want non-streaming without aligned transcript", caps)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	if got, want := gotMethod, http.MethodPost; got != want {
		t.Fatalf("method = %q, want %q", got, want)
	}
	if got, want := gotURL, "https://naveropenapi.apigw.ntruss.com/tts-premium/v1/tts"; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("Content-Type"), "application/x-www-form-urlencoded"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("X-NCP-APIGW-API-KEY-ID"), "client-id"; got != want {
		t.Fatalf("API key id header = %q, want %q", got, want)
	}
	if got, want := gotHeaders.Get("X-NCP-APIGW-API-KEY"), "client-secret"; got != want {
		t.Fatalf("API key header = %q, want %q", got, want)
	}
	wantForm := map[string]string{
		"speaker": "nara",
		"volume":  "0",
		"speed":   "0",
		"pitch":   "0",
		"text":    "hello",
		"format":  "mp3",
	}
	for key, want := range wantForm {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form %s = %q, want %q", key, got, want)
		}
	}
}

func TestClovaTTSSynthesizeStatusErrorIncludesProviderBody(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: clovaTTSRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad key"}`)),
		}, nil
	})}

	provider := NewClovaTTS("client-id", "client-secret", "nara")
	stream, err := provider.Synthesize(context.Background(), "hello")

	if stream != nil {
		t.Fatalf("stream = %T, want nil on provider status error", stream)
	}
	if err == nil || !strings.Contains(err.Error(), `{"error":"bad key"}`) {
		t.Fatalf("Synthesize() error = %v, want provider body", err)
	}
}

func TestClovaTTSStreamReportsUnsupportedNativeStreaming(t *testing.T) {
	provider := NewClovaTTS("client-id", "client-secret", "nara")

	stream, err := provider.Stream(context.Background())

	if stream != nil {
		t.Fatalf("stream = %T, want nil for unsupported native streaming", stream)
	}
	if err == nil || !strings.Contains(err.Error(), "streaming tts not natively supported") {
		t.Fatalf("Stream() error = %v, want explicit unsupported streaming error", err)
	}
}

func TestClovaTTSChunkedStreamDecodesMP3Response(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	stream := &clovaTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader(mp3Data))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("frame data is empty")
	}
	if bytes.HasPrefix(audio.Frame.Data, []byte("ID3")) || bytes.HasPrefix(audio.Frame.Data, []byte{0xff, 0xfb}) {
		t.Fatalf("frame data starts with MP3 container bytes, want decoded PCM")
	}
	if audio.Frame.SampleRate != 48000 || audio.Frame.NumChannels != 2 || audio.Frame.SamplesPerChannel == 0 {
		t.Fatalf("frame shape = rate %d channels %d samples %d, want decoded PCM frame", audio.Frame.SampleRate, audio.Frame.NumChannels, audio.Frame.SamplesPerChannel)
	}
}

func TestClovaTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	stream := &clovaTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader(mp3Data))},
	}
	defer stream.Close()

	sawAudio := false
	for {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next error before final marker = %v", err)
		}
		if audio == nil {
			t.Fatal("Next returned nil audio without error")
		}
		if audio.IsFinal {
			if !sawAudio {
				t.Fatal("final marker arrived before decoded audio")
			}
			if audio.Frame != nil {
				t.Fatalf("final marker frame = %+v, want nil", audio.Frame)
			}
			break
		}
		if audio.Frame != nil && len(audio.Frame.Data) > 0 {
			sawAudio = true
		}
	}

	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestClovaTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &clovaCloseCountBody{}
	stream := &clovaTTSChunkedStream{
		resp: &http.Response{Body: body},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
}

func TestClovaTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &clovaTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader(nil))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error before final marker: %v", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("audio = %#v, want boundary-only final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

type clovaTTSRoundTripFunc func(*http.Request) (*http.Response, error)

func (f clovaTTSRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
