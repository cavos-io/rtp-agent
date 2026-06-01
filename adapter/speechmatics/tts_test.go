package speechmatics

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
)

func TestSpeechmaticsTTSDefaultsMatchReference(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key")

	if provider.voice != "sarah" {
		t.Fatalf("voice = %q, want sarah", provider.voice)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.baseURL != "https://preview.tts.speechmatics.com" {
		t.Fatalf("base URL = %q, want preview endpoint", provider.baseURL)
	}
}

func TestSpeechmaticsTTSSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key",
		WithSpeechmaticsTTSVoice("theo"),
		WithSpeechmaticsTTSSampleRate(24000),
		WithSpeechmaticsTTSBaseURL("https://tts.example.com"),
	)

	req, err := buildSpeechmaticsTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	if req.URL.Scheme != "https" || req.URL.Host != "tts.example.com" || req.URL.Path != "/generate/theo" {
		t.Fatalf("url = %s, want https://tts.example.com/generate/theo", req.URL.String())
	}
	query := req.URL.Query()
	assertSpeechmaticsTTSQuery(t, query, "output_format", "pcm_24000")
	if query.Get("sm-sdk") == "" {
		t.Fatal("sm-sdk query parameter is empty")
	}
	if query.Get("sm-app") == "" {
		t.Fatal("sm-app query parameter is empty")
	}

	var payload map[string]string
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if payload["text"] != "hello" {
		t.Fatalf("text = %q, want hello", payload["text"])
	}
}

func TestSpeechmaticsTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := NewSpeechmaticsTTS("test-key")

	provider.UpdateOptions(
		WithSpeechmaticsTTSVoice("theo"),
		WithSpeechmaticsTTSSampleRate(24000),
	)

	if provider.voice != "theo" {
		t.Fatalf("voice = %q, want theo", provider.voice)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
}

func TestSpeechmaticsTTSSynthesizePostsAndStreamsPCM(t *testing.T) {
	originalClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "tts.example.com" || r.URL.Path != "/generate/theo" {
			t.Fatalf("url = %s, want https://tts.example.com/generate/theo", r.URL.String())
		}
		if got := r.URL.Query().Get("output_format"); got != "pcm_24000" {
			t.Fatalf("output_format = %q, want pcm_24000", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if payload["text"] != "hello" {
			t.Fatalf("text = %q, want hello", payload["text"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02, 0x03, 0x04})),
		}, nil
	})}
	defer func() {
		http.DefaultClient = originalClient
	}()

	provider := NewSpeechmaticsTTS("test-key",
		WithSpeechmaticsTTSVoice("theo"),
		WithSpeechmaticsTTSSampleRate(24000),
		WithSpeechmaticsTTSBaseURL("https://tts.example.com"),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("samples per channel = %d, want 2", audio.Frame.SamplesPerChannel)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("frame data = %#v, want complete PCM bytes", audio.Frame.Data)
	}
}

func TestSpeechmaticsTTSChunkedStreamBuffersPartialSamples(t *testing.T) {
	stream := &speechmaticsTTSChunkedStream{
		stream:     io.NopCloser(&chunkedReader{chunks: [][]byte{{0x01}, {0x02, 0x03}}}),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("first frame data = %#v, want buffered complete sample", audio.Frame.Data)
	}
	if audio.Frame.SamplesPerChannel != 1 {
		t.Fatalf("first samples per channel = %d, want 1", audio.Frame.SamplesPerChannel)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF with trailing partial byte discarded", err)
	}
}

func TestSpeechmaticsTTSChunkedStreamDiscardsPartialEOFRead(t *testing.T) {
	stream := &speechmaticsTTSChunkedStream{
		stream:     io.NopCloser(partialEOFReader{}),
		sampleRate: 24000,
	}

	_, err := stream.Next()
	if err != io.EOF {
		t.Fatalf("Next error = %v, want EOF for trailing partial sample", err)
	}
}

func assertSpeechmaticsTTSQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

type chunkedReader struct {
	chunks [][]byte
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	copy(p, chunk)
	r.chunks = r.chunks[1:]
	return len(chunk), nil
}

type partialEOFReader struct{}

func (partialEOFReader) Read(p []byte) (int, error) {
	p[0] = 0x01
	return 1, io.EOF
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
