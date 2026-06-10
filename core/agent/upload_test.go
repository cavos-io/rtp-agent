package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestUploadSessionReportUsesObservabilityWriteGrant(t *testing.T) {
	const apiSecret = "secret"

	authCh := make(chan string, 1)
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCh <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", "https://observability.test")

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.RoomID = "RM_grant"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", apiSecret, "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	var authHeader string
	select {
	case authHeader = <-authCh:
	default:
		t.Fatal("UploadSessionReport did not POST recording upload")
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader || token == "" {
		t.Fatalf("Authorization header = %q, want bearer token", authHeader)
	}

	parsed, err := jwt.ParseSigned(token)
	if err != nil {
		t.Fatalf("ParseSigned() error = %v", err)
	}
	grants := auth.ClaimGrants{}
	if err := parsed.Claims([]byte(apiSecret), &jwt.Claims{}, &grants); err != nil {
		t.Fatalf("token Claims() error = %v", err)
	}
	if grants.Observability == nil || !grants.Observability.Write {
		t.Fatalf("observability grant = %#v, want write grant", grants.Observability)
	}
	if grants.Video != nil {
		t.Fatalf("video grant = %#v, want nil", grants.Video)
	}
}

func TestUploadSessionReportTranscriptOnlySetsZeroHeaderStartTime(t *testing.T) {
	headerCh := make(chan *livekit.MetricsRecordingHeader, 1)
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm() error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("header")
		if err != nil {
			t.Errorf("FormFile(header) error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("ReadAll(header) error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		header := &livekit.MetricsRecordingHeader{}
		if err := proto.Unmarshal(data, header); err != nil {
			t.Errorf("Unmarshal(header) error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		headerCh <- header
		w.WriteHeader(http.StatusOK)
	}))
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", "https://observability.test")

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.RoomID = "RM_transcript_only"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	select {
	case header := <-headerCh:
		if header.StartTime == nil {
			t.Fatal("header StartTime = nil, want explicit zero timestamp")
		}
		if header.StartTime.Seconds != 0 || header.StartTime.Nanos != 0 {
			t.Fatalf("header StartTime = %v, want zero timestamp", header.StartTime)
		}
	case <-time.After(time.Second):
		t.Fatal("UploadSessionReport did not POST recording header")
	}
}

func TestUploadSessionReportUsesObservabilityURLEnvOverride(t *testing.T) {
	requestCh := make(chan string, 1)
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCh <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", "https://observability.test")

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.RoomID = "RM_test"

	if err := UploadSessionReport("ws://localhost:7880", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	select {
	case path := <-requestCh:
		if path != "/observability/recordings/v0" {
			t.Fatalf("upload path = %q, want /observability/recordings/v0", path)
		}
	default:
		t.Fatal("UploadSessionReport did not POST to observability URL override")
	}
}

func TestUploadSessionReportRetriesRetryableRecordingUpload(t *testing.T) {
	var attempts int32
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", "https://observability.test")

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.RoomID = "RM_retry"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("upload attempts = %d, want 2", got)
	}
}

func TestUploadSessionReportRetriesProtobufRetryInfo(t *testing.T) {
	retryInfo, err := anypb.New(&errdetails.RetryInfo{RetryDelay: durationpb.New(0)})
	if err != nil {
		t.Fatalf("Create RetryInfo detail: %v", err)
	}
	body, err := proto.Marshal(&statuspb.Status{Details: []*anypb.Any{retryInfo}})
	if err != nil {
		t.Fatalf("Marshal Status: %v", err)
	}

	var attempts int32
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write(body)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", "https://observability.test")

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.RoomID = "RM_retry_proto"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("upload attempts = %d, want 2", got)
	}
}

func TestUploadSessionReportRecordsLogsOnlySessionReport(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	defer func() { recordUploadTelemetryEvent = oldRecord }()

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}
	report.SDKVersion = "test-sdk"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("telemetry events = %#v, want one session report event", events)
	}
	if events[0].eventType != "session_report" || events[0].body != "session report" {
		t.Fatalf("telemetry event = %#v, want session report event", events[0])
	}
	if events[0].attrs["agent_name"] != "agent-a" {
		t.Fatalf("agent_name attr = %#v, want agent-a", events[0].attrs["agent_name"])
	}
	if events[0].attrs["sdk_version"] != "test-sdk" {
		t.Fatalf("sdk_version attr = %#v, want test-sdk", events[0].attrs["sdk_version"])
	}
}

func TestUploadSessionReportRecordsSessionTagsSorted(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	defer func() { recordUploadTelemetryEvent = oldRecord }()

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}
	report.Tagger = NewTagger()
	for _, tag := range []string{"zeta:true", "appointment:booked", "language:es", "alpha:first"} {
		report.Tagger.Add(tag)
	}

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("telemetry events = %#v, want one session report event", events)
	}
	tags, ok := events[0].attrs["session.tags"].([]string)
	if !ok {
		t.Fatalf("session.tags = %T, want []string", events[0].attrs["session.tags"])
	}
	want := []string{"alpha:first", "appointment:booked", "language:es", "zeta:true"}
	if !slices.Equal(tags, want) {
		t.Fatalf("session.tags = %#v, want sorted %#v", tags, want)
	}
}

func TestUploadSessionReportRecordsModelUsage(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	defer func() { recordUploadTelemetryEvent = oldRecord }()

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}
	report.Usage = &telemetry.UsageSummary{LLMPromptTokens: 99}
	report.ModelUsage = []telemetry.ModelUsage{
		&telemetry.LLMModelUsage{
			Provider:          "openai",
			Model:             "gpt-report",
			InputTokens:       12,
			InputCachedTokens: 3,
			OutputTokens:      7,
		},
	}

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("telemetry events = %#v, want one session report event", events)
	}
	usage, ok := events[0].attrs["usage"].([]map[string]any)
	if !ok {
		t.Fatalf("usage = %T, want []map[string]any", events[0].attrs["usage"])
	}
	if len(usage) != 1 {
		t.Fatalf("usage = %#v, want one model usage entry", usage)
	}
	entry := usage[0]
	for key, want := range map[string]any{
		"type":                "llm_usage",
		"provider":            "openai",
		"model":               "gpt-report",
		"input_tokens":        12,
		"input_cached_tokens": 3,
		"output_tokens":       7,
	} {
		if entry[key] != want {
			t.Fatalf("usage[%s] = %#v, want %#v in %#v", key, entry[key], want, entry)
		}
	}
	if _, ok := entry["llm_prompt_tokens"]; ok {
		t.Fatalf("usage = %#v, want model usage keys not summary keys", usage)
	}
}

func TestUploadSessionReportRecordsTranscriptChatItems(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	defer func() { recordUploadTelemetryEvent = oldRecord }()
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", "https://observability.test")

	chatCtx := llm.NewChatContext()
	chatCtx.AddMessage(llm.ChatMessageArgs{
		Role: llm.ChatRoleUser,
		Text: "hello there",
	})
	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.ChatHistory = chatCtx
	report.RoomID = "RM_chat"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("telemetry events = %#v, want session report and chat item", events)
	}
	if events[1].eventType != "chat_item" || events[1].body != "chat item" {
		t.Fatalf("second telemetry event = %#v, want chat item event", events[1])
	}
	item, ok := events[1].attrs["chat.item"].(map[string]any)
	if !ok {
		t.Fatalf("chat.item = %T, want map", events[1].attrs["chat.item"])
	}
	if item["type"] != "message" || item["role"] != "user" {
		t.Fatalf("chat.item = %#v, want user message", item)
	}
	content, ok := item["content"].([]any)
	if !ok || len(content) != 1 || content[0] != "hello there" {
		t.Fatalf("chat.item content = %#v, want hello there", item["content"])
	}
}

func TestUploadSessionReportRecordsEvaluationAndOutcome(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	defer func() { recordUploadTelemetryEvent = oldRecord }()

	report := NewSessionReport()
	report.Tagger = NewTagger()
	report.Tagger.Evaluation(&EvaluationResult{Judgments: map[string]string{"helpfulness": "pass"}})
	report.Tagger.Fail("caller hung up")

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("telemetry events = %#v, want evaluation and outcome events", events)
	}
	if events[0].eventType != "evaluation" || events[0].body != "evaluation" {
		t.Fatalf("first telemetry event = %#v, want evaluation", events[0])
	}
	evaluation, ok := events[0].attrs["evaluation"].(map[string]any)
	if !ok {
		t.Fatalf("evaluation attr = %T, want map", events[0].attrs["evaluation"])
	}
	if evaluation["tag"] != "lk.judge.helpfulness:pass" {
		t.Fatalf("evaluation tag = %#v, want generated judge tag", evaluation["tag"])
	}
	if events[1].eventType != "outcome" || events[1].body != "outcome" {
		t.Fatalf("second telemetry event = %#v, want outcome", events[1])
	}
	outcome, ok := events[1].attrs["outcome"].(map[string]any)
	if !ok {
		t.Fatalf("outcome attr = %T, want map", events[1].attrs["outcome"])
	}
	if outcome["outcome"] != "fail" || outcome["reason"] != "caller hung up" {
		t.Fatalf("outcome attr = %#v, want fail reason", outcome)
	}
}

func TestUploadSessionReportRecordsTagMetadata(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	defer func() { recordUploadTelemetryEvent = oldRecord }()

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}
	report.Tagger = NewTagger()
	report.Tagger.Add("appointment:booked", map[string]any{
		"slot_id":  "abc123",
		"calendar": "cal.com",
	})

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("telemetry events = %#v, want session report and tag events", events)
	}
	if events[1].eventType != "tag" || events[1].body != "tag" {
		t.Fatalf("second telemetry event = %#v, want tag event", events[1])
	}
	tag, ok := events[1].attrs["tag"].(map[string]any)
	if !ok {
		t.Fatalf("tag attr = %T, want map", events[1].attrs["tag"])
	}
	if tag["name"] != "appointment:booked" {
		t.Fatalf("tag name = %#v, want appointment:booked", tag["name"])
	}
	metadata, ok := tag["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("tag metadata = %T, want map", tag["metadata"])
	}
	if metadata["slot_id"] != "abc123" || metadata["calendar"] != "cal.com" {
		t.Fatalf("tag metadata = %#v, want appointment metadata", metadata)
	}
}

type uploadTelemetryEvent struct {
	eventType string
	body      string
	attrs     map[string]interface{}
}

func useRecordingUploadHTTPClient(t *testing.T, handler http.Handler) {
	t.Helper()
	oldClient := recordingUploadHTTPClient
	recordingUploadHTTPClient = &http.Client{
		Transport: recordingUploadRoundTripper(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			resp := recorder.Result()
			if resp.Body == nil {
				resp.Body = io.NopCloser(strings.NewReader(""))
			}
			return resp, nil
		}),
	}
	t.Cleanup(func() {
		recordingUploadHTTPClient = oldClient
	})
}

type recordingUploadRoundTripper func(*http.Request) (*http.Response, error)

func (f recordingUploadRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
