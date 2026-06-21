package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

func multipartPartsFromRequest(t *testing.T, req *http.Request) map[string][]byte {
	t.Helper()
	reader, err := req.MultipartReader()
	if err != nil {
		t.Fatalf("MultipartReader: %v", err)
	}
	parts := make(map[string][]byte)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("ReadAll multipart part: %v", err)
		}
		parts[part.FormName()] = data
	}
	return parts
}

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
	oldRecordAt := recordUploadTelemetryEventAt
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	recordUploadTelemetryEventAt = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: timestamp})
	}
	defer func() {
		recordUploadTelemetryEvent = oldRecord
		recordUploadTelemetryEventAt = oldRecordAt
	}()

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}
	report.SDKVersion = "test-sdk"
	report.Timestamp = 1700.5
	startedAt := 1600.25
	report.StartedAt = &startedAt

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("telemetry events = %#v, want one session report event", events)
	}
	if events[0].eventType != "session_report" || events[0].body != "session report" {
		t.Fatalf("telemetry event = %#v, want session report event", events[0])
	}
	wantStartedAt := time.Unix(1600, 250000000)
	if !events[0].timestamp.Equal(wantStartedAt) {
		t.Fatalf("session report event timestamp = %v, want started_at timestamp %v", events[0].timestamp, wantStartedAt)
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
	oldRecordAt := recordUploadTelemetryEventAt
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	recordUploadTelemetryEventAt = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: timestamp})
	}
	defer func() {
		recordUploadTelemetryEvent = oldRecord
		recordUploadTelemetryEventAt = oldRecordAt
	}()

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

func TestUploadSessionReportRecordsEmptySessionTagsAsNil(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	oldRecordAt := recordUploadTelemetryEventAt
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	recordUploadTelemetryEventAt = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: timestamp})
	}
	defer func() {
		recordUploadTelemetryEvent = oldRecord
		recordUploadTelemetryEventAt = oldRecordAt
	}()

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}
	report.Tagger = NewTagger()

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("telemetry events = %#v, want one session report event", events)
	}
	tags, ok := events[0].attrs["session.tags"]
	if !ok {
		t.Fatalf("session.tags missing from attrs: %#v", events[0].attrs)
	}
	if tags != nil {
		t.Fatalf("session.tags = %#v, want nil for empty tagger", tags)
	}
}

func TestUploadSessionReportRecordsModelUsage(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	oldRecordAt := recordUploadTelemetryEventAt
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	recordUploadTelemetryEventAt = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: timestamp})
	}
	defer func() {
		recordUploadTelemetryEvent = oldRecord
		recordUploadTelemetryEventAt = oldRecordAt
	}()

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

func TestUploadSessionReportRecordsEmptyUsageAsNil(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	oldRecordAt := recordUploadTelemetryEventAt
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	recordUploadTelemetryEventAt = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: timestamp})
	}
	defer func() {
		recordUploadTelemetryEvent = oldRecord
		recordUploadTelemetryEventAt = oldRecordAt
	}()

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("telemetry events = %#v, want one session report event", events)
	}
	usage, ok := events[0].attrs["usage"]
	if !ok {
		t.Fatalf("usage missing from attrs: %#v", events[0].attrs)
	}
	if usage != nil {
		t.Fatalf("usage = %#v, want nil when report has no model usage", usage)
	}
}

func TestUploadSessionReportRecordsTranscriptChatItems(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	oldRecordAt := recordUploadTelemetryEventAt
	oldRecordWithOptions := recordUploadTelemetryEventWithOptions
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	recordUploadTelemetryEventAt = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: timestamp})
	}
	recordUploadTelemetryEventWithOptions = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, options telemetry.ChatEventOptions) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: options.Timestamp, severity: options.SeverityText})
	}
	defer func() {
		recordUploadTelemetryEvent = oldRecord
		recordUploadTelemetryEventAt = oldRecordAt
		recordUploadTelemetryEventWithOptions = oldRecordWithOptions
	}()
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", "https://observability.test")

	chatCtx := llm.NewChatContext()
	createdAt := time.Unix(1800, 125000000)
	chatCtx.AddMessage(llm.ChatMessageArgs{
		Role:      llm.ChatRoleUser,
		Text:      "hello there",
		CreatedAt: createdAt,
	})
	outputCreatedAt := createdAt.Add(time.Millisecond)
	chatCtx.Items = append(chatCtx.Items, &llm.FunctionCallOutput{
		ID:        "out_1",
		CallID:    "call_lookup",
		Name:      "lookup",
		Output:    "tool failed",
		IsError:   true,
		CreatedAt: outputCreatedAt,
	})
	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.ChatHistory = chatCtx
	report.RoomID = "RM_chat"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("telemetry events = %#v, want session report and chat item events", events)
	}
	if events[1].eventType != "chat_item" || events[1].body != "chat item" {
		t.Fatalf("second telemetry event = %#v, want chat item event", events[1])
	}
	if !events[1].timestamp.Equal(createdAt) {
		t.Fatalf("chat item event timestamp = %v, want item created_at %v", events[1].timestamp, createdAt)
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
	if events[2].eventType != "chat_item" || events[2].body != "chat item" {
		t.Fatalf("third telemetry event = %#v, want errored function output chat item event", events[2])
	}
	if !events[2].timestamp.Equal(outputCreatedAt) {
		t.Fatalf("function output event timestamp = %v, want item created_at %v", events[2].timestamp, outputCreatedAt)
	}
	if events[2].severity != "error" {
		t.Fatalf("function output event severity = %q, want error", events[2].severity)
	}
}

func TestUploadSessionReportSkipsMalformedCloudURLLikeReference(t *testing.T) {
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("UploadSessionReport issued request to %s, want malformed URL skipped", r.URL.String())
	}))

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.RoomID = "RM_test"

	if err := UploadSessionReport("://bad-url", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v, want nil for malformed non-cloud URL", err)
	}
}

func TestUploadSessionReportNormalizesCloudHostnameLikeReference(t *testing.T) {
	requestCh := make(chan string, 1)
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCh <- r.URL.Host
		w.WriteHeader(http.StatusOK)
	}))

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.RoomID = "RM_test"

	if err := UploadSessionReport("wss://Tenant.LiveKit.Cloud:443/project-a", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	select {
	case host := <-requestCh:
		if host != "tenant.livekit.cloud" {
			t.Fatalf("upload host = %q, want reference hostname without port", host)
		}
	default:
		t.Fatal("UploadSessionReport did not POST to normalized cloud observability URL")
	}
}

func TestUploadSessionReportOmitsEmptyAudioPartLikeReference(t *testing.T) {
	audioPath := filepath.Join(t.TempDir(), "empty.ogg")
	if err := os.WriteFile(audioPath, nil, 0o600); err != nil {
		t.Fatalf("write empty audio file: %v", err)
	}
	startedAt := 12.5
	partsCh := make(chan map[string][]byte, 1)
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		partsCh <- multipartPartsFromRequest(t, r)
		w.WriteHeader(http.StatusOK)
	}))

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Audio: true, Transcript: true}
	report.RoomID = "RM_test"
	report.AudioRecordingPath = &audioPath
	report.AudioRecordingStartedAt = &startedAt

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	select {
	case parts := <-partsCh:
		if _, ok := parts["audio"]; ok {
			t.Fatalf("multipart parts include empty audio part: %#v", parts)
		}
		if _, ok := parts["header"]; !ok {
			t.Fatalf("multipart parts missing header: %#v", parts)
		}
	case <-time.After(time.Second):
		t.Fatal("UploadSessionReport did not POST recording upload")
	}
}

func TestUploadSessionReportSanitizesTranscriptChatHistory(t *testing.T) {
	oldClient := recordingUploadHTTPClient
	oldRecord := recordUploadTelemetryEvent
	oldRecordAt := recordUploadTelemetryEventAt
	oldRecordWithOptions := recordUploadTelemetryEventWithOptions
	var events []uploadTelemetryEvent
	var uploadedChatHistory map[string]any
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	recordUploadTelemetryEventAt = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: timestamp})
	}
	recordUploadTelemetryEventWithOptions = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, options telemetry.ChatEventOptions) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: options.Timestamp, severity: options.SeverityText})
	}
	recordingUploadHTTPClient = &http.Client{Transport: recordingUploadRoundTripper(func(req *http.Request) (*http.Response, error) {
		parts := multipartPartsFromRequest(t, req)
		if err := json.Unmarshal(parts["chat_history"], &uploadedChatHistory); err != nil {
			t.Fatalf("unmarshal uploaded chat_history: %v", err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	defer func() {
		recordingUploadHTTPClient = oldClient
		recordUploadTelemetryEvent = oldRecord
		recordUploadTelemetryEventAt = oldRecordAt
		recordUploadTelemetryEventWithOptions = oldRecordWithOptions
	}()

	instructions := "be helpful"
	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.RoomID = "RM_chat"
	report.ChatHistory.Append(&llm.ChatMessage{ID: "empty", Role: llm.ChatRoleUser})
	report.ChatHistory.Append(&llm.ChatMessage{ID: "blank", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: ""}}})
	report.ChatHistory.Append(&llm.ChatMessage{ID: "real", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}})
	report.ChatHistory.Append(&llm.AgentConfigUpdate{ID: "config-1", Instructions: &instructions})
	report.ChatHistory.Append(&llm.AgentConfigUpdate{ID: "config-1", Instructions: &instructions})

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	items := uploadedChatHistory["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("uploaded chat_history items = %#v, want real message and config update", items)
	}
	if len(events) != 3 {
		t.Fatalf("telemetry events = %#v, want session report plus two sanitized chat items", events)
	}
	gotIDs := []string{report.ChatHistory.Items[0].GetID(), report.ChatHistory.Items[1].GetID()}
	wantIDs := []string{"real", "config-1"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("report ChatHistory ids = %#v, want %#v", gotIDs, wantIDs)
	}
}

func TestUploadSessionReportRecordsEvaluationAndOutcome(t *testing.T) {
	oldRecord := recordUploadTelemetryEvent
	oldRecordAt := recordUploadTelemetryEventAt
	oldRecordWithOptions := recordUploadTelemetryEventWithOptions
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	recordUploadTelemetryEventAt = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: timestamp})
	}
	recordUploadTelemetryEventWithOptions = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, options telemetry.ChatEventOptions) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: options.Timestamp, severity: options.SeverityText})
	}
	defer func() {
		recordUploadTelemetryEvent = oldRecord
		recordUploadTelemetryEventAt = oldRecordAt
		recordUploadTelemetryEventWithOptions = oldRecordWithOptions
	}()

	report := NewSessionReport()
	report.Timestamp = 1700.5
	report.Tagger = NewTagger()
	report.Tagger.Evaluation(&EvaluationResult{
		Judgments:    map[string]string{"helpfulness": "fail"},
		Reasoning:    map[string]string{"helpfulness": "clear answer"},
		Instructions: map[string]string{"helpfulness": "judge helpfulness"},
	})
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
	wantReportTimestamp := time.Unix(1700, 500000000)
	if !events[0].timestamp.Equal(wantReportTimestamp) {
		t.Fatalf("evaluation event timestamp = %v, want report timestamp %v", events[0].timestamp, wantReportTimestamp)
	}
	if events[0].severity != "error" {
		t.Fatalf("evaluation event severity = %q, want error", events[0].severity)
	}
	evaluation, ok := events[0].attrs["evaluation"].(map[string]any)
	if !ok {
		t.Fatalf("evaluation attr = %T, want map", events[0].attrs["evaluation"])
	}
	if evaluation["tag"] != "lk.judge.helpfulness:fail" {
		t.Fatalf("evaluation tag = %#v, want generated judge tag", evaluation["tag"])
	}
	if evaluation["reasoning"] != "clear answer" || evaluation["instructions"] != "judge helpfulness" {
		t.Fatalf("evaluation attr = %#v, want reasoning and instructions", evaluation)
	}
	if events[1].eventType != "outcome" || events[1].body != "outcome" {
		t.Fatalf("second telemetry event = %#v, want outcome", events[1])
	}
	if !events[1].timestamp.Equal(wantReportTimestamp) {
		t.Fatalf("outcome event timestamp = %v, want report timestamp %v", events[1].timestamp, wantReportTimestamp)
	}
	if events[1].severity != "error" {
		t.Fatalf("outcome event severity = %q, want error", events[1].severity)
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
	oldRecordAt := recordUploadTelemetryEventAt
	var events []uploadTelemetryEvent
	recordUploadTelemetryEvent = func(_ context.Context, eventType string, body string, attrs map[string]interface{}) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs})
	}
	recordUploadTelemetryEventAt = func(_ context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
		events = append(events, uploadTelemetryEvent{eventType: eventType, body: body, attrs: attrs, timestamp: timestamp})
	}
	defer func() {
		recordUploadTelemetryEvent = oldRecord
		recordUploadTelemetryEventAt = oldRecordAt
	}()

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}
	report.Tagger = NewTagger()
	beforeAdd := time.Now()
	report.Tagger.Add("appointment:booked", map[string]any{
		"slot_id":  "abc123",
		"calendar": "cal.com",
	})
	afterAdd := time.Now()

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("telemetry events = %#v, want session report and tag events", events)
	}
	if events[1].eventType != "tag" || events[1].body != "tag" {
		t.Fatalf("second telemetry event = %#v, want tag event", events[1])
	}
	if events[1].timestamp.IsZero() {
		t.Fatalf("tag event timestamp is zero, want tag creation timestamp")
	}
	if events[1].timestamp.Before(beforeAdd) || events[1].timestamp.After(afterAdd) {
		t.Fatalf("tag event timestamp = %v, want tag creation timestamp between %v and %v", events[1].timestamp, beforeAdd, afterAdd)
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
	timestamp time.Time
	severity  string
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
