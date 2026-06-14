package livekit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"golang.org/x/text/unicode/norm"
)

func TestTurnDetectorPluginMetadataMatchesReference(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.livekit" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.livekit", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.livekit" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.livekit", PluginPackage)
	}
}

func TestTurnDetectorModelDefinitionsMatchReference(t *testing.T) {
	if HGModel != "livekit/turn-detector" {
		t.Fatalf("HGModel = %q, want livekit/turn-detector", HGModel)
	}
	if ONNXFilename != "model_q8.onnx" {
		t.Fatalf("ONNXFilename = %q, want model_q8.onnx", ONNXFilename)
	}
	if MaxHistoryTokens != 128 {
		t.Fatalf("MaxHistoryTokens = %d, want 128", MaxHistoryTokens)
	}
	if MaxHistoryTurns != 6 {
		t.Fatalf("MaxHistoryTurns = %d, want 6", MaxHistoryTurns)
	}

	english := NewEnglishModel()
	if english.Model() != ModelEnglish {
		t.Fatalf("english model = %q, want %q", english.Model(), ModelEnglish)
	}
	if english.Provider() != "livekit" {
		t.Fatalf("english provider = %q, want livekit", english.Provider())
	}
	if english.ModelRevision() != "v1.2.2-en" {
		t.Fatalf("english revision = %q, want v1.2.2-en", english.ModelRevision())
	}
	if english.InferenceMethod() != "lk_end_of_utterance_en" {
		t.Fatalf("english inference method = %q, want lk_end_of_utterance_en", english.InferenceMethod())
	}

	multilingual := NewMultilingualModel()
	if multilingual.Model() != ModelMultilingual {
		t.Fatalf("multilingual model = %q, want %q", multilingual.Model(), ModelMultilingual)
	}
	if multilingual.ModelRevision() != "v0.4.1-intl" {
		t.Fatalf("multilingual revision = %q, want v0.4.1-intl", multilingual.ModelRevision())
	}
	if multilingual.InferenceMethod() != "lk_end_of_utterance_multilingual" {
		t.Fatalf("multilingual inference method = %q, want lk_end_of_utterance_multilingual", multilingual.InferenceMethod())
	}
}

func TestTurnDetectorUnlikelyThresholdOverrideMatchesReference(t *testing.T) {
	threshold := 0.42
	model := NewEnglishModel(WithUnlikelyThreshold(threshold))

	got, ok := model.UnlikelyThreshold("en-US")
	if !ok {
		t.Fatal("UnlikelyThreshold(en-US) ok = false, want true")
	}
	if got != threshold {
		t.Fatalf("UnlikelyThreshold(en-US) = %v, want %v", got, threshold)
	}

	model = NewEnglishModel()
	if _, ok := model.UnlikelyThreshold("en-US"); ok {
		t.Fatal("UnlikelyThreshold(en-US) ok = true without language config or override, want false")
	}
}

func TestTurnDetectorUnlikelyThresholdUsesFullAndBaseLanguage(t *testing.T) {
	model := NewMultilingualModel(WithLanguageThresholds(map[string]float64{
		"en":    0.31,
		"en-US": 0.27,
	}))

	got, ok := model.UnlikelyThreshold("en-US")
	if !ok {
		t.Fatal("UnlikelyThreshold(en-US) ok = false, want true")
	}
	if got != 0.27 {
		t.Fatalf("UnlikelyThreshold(en-US) = %v, want exact language threshold", got)
	}

	got, ok = model.UnlikelyThreshold("en-GB")
	if !ok {
		t.Fatal("UnlikelyThreshold(en-GB) ok = false, want base language threshold")
	}
	if got != 0.31 {
		t.Fatalf("UnlikelyThreshold(en-GB) = %v, want base language threshold", got)
	}
}

func TestTurnDetectorUnlikelyThresholdLoadsLanguagesJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "languages.json")
	content := `{
		"en": {"threshold": 0.31},
		"id-ID": {"threshold": 0.46}
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile languages.json error = %v", err)
	}

	model := NewMultilingualModel(WithLanguagesPath(path))

	got, ok := model.UnlikelyThreshold("id-ID")
	if !ok {
		t.Fatal("UnlikelyThreshold(id-ID) ok = false, want threshold from languages.json")
	}
	if got != 0.46 {
		t.Fatalf("UnlikelyThreshold(id-ID) = %v, want languages.json threshold", got)
	}

	got, ok = model.UnlikelyThreshold("en-US")
	if !ok {
		t.Fatal("UnlikelyThreshold(en-US) ok = false, want base language threshold from languages.json")
	}
	if got != 0.31 {
		t.Fatalf("UnlikelyThreshold(en-US) = %v, want base languages.json threshold", got)
	}
}

func TestTurnDetectorUnlikelyThresholdFetchesRemoteThreshold(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/eot/multi" {
			t.Fatalf("path = %q, want /eot/multi", r.URL.Path)
		}
		var req struct {
			Language string `json:"language"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("request decode error = %v", err)
		}
		if req.Language != "id-ID" {
			t.Fatalf("language = %q, want id-ID", req.Language)
		}
		return jsonResponse(200, `{"threshold":0.44}`), nil
	})}

	model := NewMultilingualModel(
		WithRemoteInferenceBaseURL("https://turn.example"),
		WithHTTPClient(client),
	)
	got, ok := model.UnlikelyThreshold("id-ID")
	if !ok {
		t.Fatal("UnlikelyThreshold(id-ID) ok = false, want remote threshold")
	}
	if got != 0.44 {
		t.Fatalf("UnlikelyThreshold(id-ID) = %v, want remote threshold", got)
	}

	got, ok = model.UnlikelyThreshold("id")
	if !ok || got != 0.44 {
		t.Fatalf("cached UnlikelyThreshold(id) = %v/%v, want 0.44/true", got, ok)
	}
	if calls != 1 {
		t.Fatalf("remote threshold calls = %d, want cached single call", calls)
	}
}

func TestTurnDetectorRemoteInferenceURLMatchesReference(t *testing.T) {
	if got := RemoteInferenceURL(""); got != "" {
		t.Fatalf("RemoteInferenceURL(empty) = %q, want empty", got)
	}
	if got := RemoteInferenceURL("https://turn.example"); got != "https://turn.example/eot/multi" {
		t.Fatalf("RemoteInferenceURL() = %q, want reference suffix", got)
	}
	model := NewMultilingualModel(WithRemoteInferenceBaseURL("https://turn.example"))
	if got := model.RemoteInferenceURL(); got != "https://turn.example/eot/multi" {
		t.Fatalf("Model.RemoteInferenceURL() = %q, want reference suffix", got)
	}
	if RemoteInferenceTimeout != 2*time.Second {
		t.Fatalf("RemoteInferenceTimeout = %v, want 2s", RemoteInferenceTimeout)
	}
}

func TestNewLocalEnglishModelReturnsTokenizerErrorWhenFilesMissing(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	_, err := NewLocalEnglishModel()
	if err == nil || !strings.Contains(err.Error(), "tokenizer") {
		t.Fatalf("NewLocalEnglishModel() error = %v, want tokenizer error", err)
	}
}

func TestTurnDetectorPluginDownloadFilesDownloadsReferenceFiles(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	var gotURLs []string
	downloadTurnDetectorFile = func(url string, path string) error {
		gotURLs = append(gotURLs, url)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("model"), 0o600)
	}
	t.Cleanup(func() { downloadTurnDetectorFile = downloadFile })

	if err := (Plugin{}).DownloadFiles(); err != nil {
		t.Fatalf("DownloadFiles() error = %v", err)
	}

	wantURLs := []string{
		"https://huggingface.co/livekit/turn-detector/resolve/v1.2.2-en/onnx/model_q8.onnx",
		"https://huggingface.co/livekit/turn-detector/resolve/v1.2.2-en/tokenizer.json",
		"https://huggingface.co/livekit/turn-detector/resolve/v1.2.2-en/languages.json",
		"https://huggingface.co/livekit/turn-detector/resolve/v0.4.1-intl/onnx/model_q8.onnx",
		"https://huggingface.co/livekit/turn-detector/resolve/v0.4.1-intl/tokenizer.json",
		"https://huggingface.co/livekit/turn-detector/resolve/v0.4.1-intl/languages.json",
	}
	if strings.Join(gotURLs, "\n") != strings.Join(wantURLs, "\n") {
		t.Fatalf("download URLs = %#v, want %#v", gotURLs, wantURLs)
	}

	for _, modelType := range []ModelType{ModelEnglish, ModelMultilingual} {
		onnxPath, err := ModelONNXPath(modelType)
		if err != nil {
			t.Fatalf("ModelONNXPath(%s) error = %v", modelType, err)
		}
		if _, err := os.Stat(onnxPath); err != nil {
			t.Fatalf("downloaded ONNX stat error = %v", err)
		}
		tokenizerPath, err := ModelTokenizerPath(modelType)
		if err != nil {
			t.Fatalf("ModelTokenizerPath(%s) error = %v", modelType, err)
		}
		if _, err := os.Stat(tokenizerPath); err != nil {
			t.Fatalf("downloaded tokenizer stat error = %v", err)
		}
		languagesPath, err := ModelLanguagesPath(modelType)
		if err != nil {
			t.Fatalf("ModelLanguagesPath(%s) error = %v", modelType, err)
		}
		if _, err := os.Stat(languagesPath); err != nil {
			t.Fatalf("downloaded languages stat error = %v", err)
		}
	}
}

func TestTurnDetectorPluginDownloadFilesSkipsExistingReferenceFiles(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	for _, modelType := range []ModelType{ModelEnglish, ModelMultilingual} {
		for _, pathFunc := range []func(ModelType) (string, error){ModelONNXPath, ModelTokenizerPath, ModelLanguagesPath} {
			path, err := pathFunc(modelType)
			if err != nil {
				t.Fatalf("pathFunc(%s) error = %v", modelType, err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll error = %v", err)
			}
			if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
				t.Fatalf("WriteFile error = %v", err)
			}
		}
	}
	downloadTurnDetectorFile = func(string, string) error {
		t.Fatal("download called for existing turn detector files")
		return nil
	}
	t.Cleanup(func() { downloadTurnDetectorFile = downloadFile })

	if err := (Plugin{}).DownloadFiles(); err != nil {
		t.Fatalf("DownloadFiles() error = %v", err)
	}
}

func TestTurnDetectorInferencePayloadKeepsLastSixUserAssistantMessages(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleSystem, Text: "ignore instructions"})
	for i := 0; i < 7; i++ {
		role := llm.ChatRoleUser
		if i%2 == 1 {
			role = llm.ChatRoleAssistant
		}
		chatCtx.AddMessage(llm.ChatMessageArgs{Role: role, Text: "message " + string(rune('a'+i))})
	}

	payload, err := NewEnglishModel().InferencePayload(chatCtx)
	if err != nil {
		t.Fatalf("InferencePayload() error = %v", err)
	}

	var got struct {
		ChatCtx []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"chat_ctx"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload JSON error = %v", err)
	}
	if len(got.ChatCtx) != MaxHistoryTurns {
		t.Fatalf("chat_ctx len = %d, want %d", len(got.ChatCtx), MaxHistoryTurns)
	}
	if got.ChatCtx[0].Content != "message b" {
		t.Fatalf("first retained content = %q, want message b", got.ChatCtx[0].Content)
	}
	if got.ChatCtx[5].Content != "message g" {
		t.Fatalf("last retained content = %q, want message g", got.ChatCtx[5].Content)
	}
}

func TestTurnDetectorEnglishPayloadPreservesReferenceText(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "Hello, WORLD!"})

	payload, err := NewEnglishModel().InferencePayload(chatCtx)
	if err != nil {
		t.Fatalf("InferencePayload() error = %v", err)
	}

	if !strings.Contains(string(payload), "Hello, WORLD!") {
		t.Fatalf("payload = %s, want original english text", string(payload))
	}
}

func TestTurnDetectorMultilingualPayloadNormalizesLikeReference(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: norm.NFKC.String("Hi, WORLD! Don't-stop.")})
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "NEXT\tline?"})

	payload, err := NewMultilingualModel().InferencePayload(chatCtx)
	if err != nil {
		t.Fatalf("InferencePayload() error = %v", err)
	}

	var got struct {
		ChatCtx []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"chat_ctx"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload JSON error = %v", err)
	}
	if len(got.ChatCtx) != 2 {
		t.Fatalf("chat_ctx len = %d, want 2", len(got.ChatCtx))
	}
	if got.ChatCtx[0].Content != "hi world don't-stop" {
		t.Fatalf("normalized first content = %q, want reference normalization", got.ChatCtx[0].Content)
	}
	if got.ChatCtx[1].Content != "next line" {
		t.Fatalf("normalized second content = %q, want collapsed whitespace and punctuation removal", got.ChatCtx[1].Content)
	}
}

func TestTurnDetectorPredictEndOfTurnUsesRemoteInferenceURL(t *testing.T) {
	var gotPath string
	var gotRequest struct {
		ChatCtx []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"chat_ctx"`
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("request decode error = %v", err)
		}
		return jsonResponse(200, `{"probability":0.73}`), nil
	})}

	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "Need more time"})

	probability, err := NewMultilingualModel(
		WithRemoteInferenceBaseURL("https://turn.example"),
		WithHTTPClient(client),
	).
		PredictEndOfTurn(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("PredictEndOfTurn() error = %v", err)
	}
	if gotPath != "/eot/multi" {
		t.Fatalf("path = %q, want /eot/multi", gotPath)
	}
	if len(gotRequest.ChatCtx) != 1 || gotRequest.ChatCtx[0].Content != "need more time" {
		t.Fatalf("request chat_ctx = %#v, want normalized user message", gotRequest.ChatCtx)
	}
	if probability != 0.73 {
		t.Fatalf("probability = %v, want 0.73", probability)
	}
}

func TestTurnDetectorPredictEndOfTurnDefaultsToOneForInvalidRemoteProbability(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"probability":-1}`), nil
	})}

	probability, err := NewMultilingualModel(
		WithRemoteInferenceBaseURL("https://turn.example"),
		WithHTTPClient(client),
	).
		PredictEndOfTurn(context.Background(), llm.NewChatContext())
	if err != nil {
		t.Fatalf("PredictEndOfTurn() error = %v", err)
	}
	if probability != 1 {
		t.Fatalf("probability = %v, want 1 for invalid remote probability", probability)
	}
}

func TestTurnDetectorPredictEndOfTurnUsesLocalRunnerWhenRemoteDisabled(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "Ready?"})
	var gotPayload string
	model := NewEnglishModel(WithTurnDetectorRunner(turnDetectorRunnerFunc(
		func(ctx context.Context, payload []byte) (float64, error) {
			gotPayload = string(payload)
			return 0.82, nil
		},
	)))

	probability, err := model.PredictEndOfTurn(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("PredictEndOfTurn() error = %v", err)
	}
	if probability != 0.82 {
		t.Fatalf("probability = %v, want local runner probability", probability)
	}
	if !strings.Contains(gotPayload, "Ready?") {
		t.Fatalf("runner payload = %s, want chat context payload", gotPayload)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore Chdir error = %v", err)
		}
	})
}
