package gnani

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func TestGnaniSTTDefaultsMatchReference(t *testing.T) {
	provider := NewSTT("test-key")

	if provider.baseURL != "https://api.vachana.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.language != "en-IN" {
		t.Fatalf("language = %q, want en-IN", provider.language)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	caps := provider.Capabilities()
	if caps.Streaming {
		t.Fatal("streaming = true, want false until websocket streaming is implemented")
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true for REST recognition")
	}
}

func TestGnaniSTTRecognizeRequestUsesReferenceMultipart(t *testing.T) {
	provider := NewSTT("test-key")

	req, err := buildSTTRequest(context.Background(), provider, []byte{0x01, 0x02}, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.vachana.ai/stt/v3" {
		t.Fatalf("url = %q, want stt v3 endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key-ID"); got != "test-key" {
		t.Fatalf("X-API-Key-ID = %q, want test key", got)
	}

	fields, files := readMultipartRequest(t, req)
	if fields["language_code"] != "en-IN" {
		t.Fatalf("language_code = %q, want en-IN", fields["language_code"])
	}
	audio := files["audio_file"]
	if audio.filename != "audio.wav" {
		t.Fatalf("audio filename = %q, want audio.wav", audio.filename)
	}
	if audio.contentType != "audio/wav" {
		t.Fatalf("audio content type = %q, want audio/wav", audio.contentType)
	}
	if string(audio.body) != "\x01\x02" {
		t.Fatalf("audio body = %#v, want request audio", audio.body)
	}
}

func TestGnaniSTTOptionsAndLanguageOverrideMatchReference(t *testing.T) {
	provider := NewSTT("test-key",
		WithSTTBaseURL("https://gnani.example/"),
		WithSTTLanguage("hi-IN"),
		WithSTTSampleRate(8000),
		WithSTTOrganizationID("org-1"),
		WithSTTUserID("user-1"),
	)

	req, err := buildSTTRequest(context.Background(), provider, []byte{0x01}, "ta-IN")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.URL.String() != "https://gnani.example/stt/v3" {
		t.Fatalf("url = %q, want custom endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-Organization-ID"); got != "org-1" {
		t.Fatalf("X-Organization-ID = %q, want org-1", got)
	}
	if got := req.Header.Get("X-API-User-ID"); got != "user-1" {
		t.Fatalf("X-API-User-ID = %q, want user-1", got)
	}

	fields, _ := readMultipartRequest(t, req)
	if fields["language_code"] != "ta-IN" {
		t.Fatalf("language_code = %q, want override language", fields["language_code"])
	}
	if provider.sampleRate != 8000 {
		t.Fatalf("sample rate = %d, want 8000", provider.sampleRate)
	}
}

func TestGnaniSTTResponseMapsTranscriptRequestIDAndLanguage(t *testing.T) {
	event, err := gnaniSpeechEventFromResponse(gnaniSTTResponse{
		Transcript: "hello world",
		RequestID:  "req-123",
	}, "en-IN")
	if err != nil {
		t.Fatalf("speech event: %v", err)
	}

	if event.RequestID != "req-123" {
		t.Fatalf("request id = %q, want req-123", event.RequestID)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello world" {
		t.Fatalf("text = %q, want transcript", alt.Text)
	}
	if alt.Language != "en-IN" {
		t.Fatalf("language = %q, want en-IN", alt.Language)
	}
	if alt.Confidence != 1.0 {
		t.Fatalf("confidence = %f, want 1.0", alt.Confidence)
	}
}

type multipartFile struct {
	filename    string
	contentType string
	body        []byte
}

func readMultipartRequest(t *testing.T, req *http.Request) (map[string]string, map[string]multipartFile) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("media type = %q, want multipart", mediaType)
	}
	reader := multipart.NewReader(req.Body, params["boundary"])
	fields := map[string]string{}
	files := map[string]multipartFile{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FileName() != "" {
			files[part.FormName()] = multipartFile{
				filename:    part.FileName(),
				contentType: part.Header.Get("Content-Type"),
				body:        data,
			}
			continue
		}
		fields[part.FormName()] = string(data)
	}
	return fields, files
}
