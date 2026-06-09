package clova

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func TestClovaPluginDownloadFilesMatchesReferenceNoop(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.clova" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.clova", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("plugin version = %q, want reference version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.clova" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.clova", PluginPackage)
	}
	if err := (Plugin{}).DownloadFiles(); err != nil {
		t.Fatalf("DownloadFiles() error = %v, want nil reference no-op", err)
	}
}

func TestClovaSTTDefaultsMatchReference(t *testing.T) {
	provider := NewClovaSTT("secret", "https://clova.example")

	if provider.secret != "secret" {
		t.Fatalf("secret = %q, want provided secret", provider.secret)
	}
	if provider.invokeURL != "https://clova.example" {
		t.Fatalf("invoke URL = %q, want provided invoke URL", provider.invokeURL)
	}
	if provider.language != "en-US" {
		t.Fatalf("language = %q, want en-US", provider.language)
	}
	if provider.threshold != 0.5 {
		t.Fatalf("threshold = %.1f, want 0.5", provider.threshold)
	}
	caps := provider.Capabilities()
	if caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("capabilities = %+v, want offline recognize with interim compatibility", caps)
	}
}

func TestNewClovaSTTUsesEnvironmentSecretAndInvokeURL(t *testing.T) {
	t.Setenv("CLOVA_STT_SECRET_KEY", "env-secret")
	t.Setenv("CLOVA_STT_INVOKE_URL", "https://env-clova.example/")

	provider := NewClovaSTT("", "")

	if provider.secret != "env-secret" {
		t.Fatalf("secret = %q, want env secret", provider.secret)
	}
	if provider.invokeURL != "https://env-clova.example" {
		t.Fatalf("invoke URL = %q, want env invoke URL without trailing slash", provider.invokeURL)
	}

	explicit := NewClovaSTT("explicit-secret", "https://explicit.example/")
	if explicit.secret != "explicit-secret" {
		t.Fatalf("secret = %q, want explicit secret", explicit.secret)
	}
	if explicit.invokeURL != "https://explicit.example" {
		t.Fatalf("invoke URL = %q, want explicit invoke URL without trailing slash", explicit.invokeURL)
	}
}

func TestClovaSTTLanguageMappingMatchesReference(t *testing.T) {
	provider := NewClovaSTT("secret", "https://clova.example",
		WithClovaSTTLanguage("en"),
	)
	if provider.language != "en-US" {
		t.Fatalf("language = %q, want mapped en-US", provider.language)
	}

	provider = NewClovaSTT("secret", "https://clova.example",
		WithClovaSTTLanguage("zh-CN"),
	)
	if provider.language != "zh-cn" {
		t.Fatalf("language = %q, want mapped zh-cn", provider.language)
	}
}

func TestBuildClovaSTTRecognizeRequestMatchesReference(t *testing.T) {
	provider := NewClovaSTT("secret", "https://clova.example",
		WithClovaSTTLanguage("en"),
	)

	req, err := buildClovaSTTRecognizeRequest(context.Background(), provider, []byte("pcm"), "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if req.URL.String() != "https://clova.example/recognizer/upload" {
		t.Fatalf("URL = %q, want upload URL", req.URL.String())
	}
	if req.Header.Get("X-CLOVASPEECH-API-KEY") != "secret" {
		t.Fatalf("secret header = %q, want secret", req.Header.Get("X-CLOVASPEECH-API-KEY"))
	}

	fields := readClovaMultipartFields(t, req)
	var params map[string]any
	if err := json.Unmarshal([]byte(fields["params"]), &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params["language"] != "en-US" || params["completion"] != "sync" {
		t.Fatalf("params = %+v, want language en-US completion sync", params)
	}
	if !strings.HasPrefix(fields["media"], "RIFF") || !strings.Contains(fields["media"], "WAVE") {
		t.Fatalf("media = %q, want wav payload", fields["media"][:min(len(fields["media"]), 12)])
	}
}

func TestClovaSTTSpeechEventAndThreshold(t *testing.T) {
	provider := NewClovaSTT("secret", "https://clova.example",
		WithClovaSTTLanguage("ko-KR"),
		WithClovaSTTThreshold(0.6),
	)

	event, err := clovaSTTResponseToEvent(provider, clovaSTTResponse{Text: "hello", Confidence: 0.9})
	if err != nil {
		t.Fatalf("response event: %v", err)
	}
	if event.Alternatives[0].Text != "hello" || event.Alternatives[0].Language != "ko-KR" {
		t.Fatalf("alternative = %+v, want text and language", event.Alternatives[0])
	}

	_, err = clovaSTTResponseToEvent(provider, clovaSTTResponse{Text: "quiet", Confidence: 0.2})
	if err == nil || !strings.Contains(err.Error(), "below threshold") {
		t.Fatalf("error = %v, want threshold rejection", err)
	}
}

func readClovaMultipartFields(t *testing.T, req *http.Request) map[string]string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	boundary := strings.TrimPrefix(req.Header.Get("Content-Type"), "multipart/form-data; boundary=")
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	fields := map[string]string{}
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
		fields[part.FormName()] = string(data)
	}
	return fields
}
