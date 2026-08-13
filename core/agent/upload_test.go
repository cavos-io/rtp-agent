package agent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

type uploadedSessionTelemetry struct {
	authorization string
	attributes    map[string]string
	bodies        []string
	eventTypes    []string
	scopeAttrs    map[string]string
	recordAttrs   []map[string]string
}

func decodeUploadedSessionTelemetry(req *http.Request) (uploadedSessionTelemetry, error) {
	body := io.Reader(req.Body)
	if req.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(req.Body)
		if err != nil {
			return uploadedSessionTelemetry{}, err
		}
		defer reader.Close()
		body = reader
	}
	payload, err := io.ReadAll(body)
	if err != nil {
		return uploadedSessionTelemetry{}, err
	}
	request := &collectorlogsv1.ExportLogsServiceRequest{}
	if err := proto.Unmarshal(payload, request); err != nil {
		return uploadedSessionTelemetry{}, err
	}
	upload := uploadedSessionTelemetry{
		authorization: req.Header.Get("Authorization"),
		attributes:    make(map[string]string),
		scopeAttrs:    make(map[string]string),
	}
	for _, resourceLogs := range request.ResourceLogs {
		for _, attr := range resourceLogs.GetResource().GetAttributes() {
			upload.attributes[attr.Key] = attr.Value.GetStringValue()
		}
		for _, scopeLogs := range resourceLogs.ScopeLogs {
			for _, attr := range scopeLogs.GetScope().GetAttributes() {
				upload.scopeAttrs[attr.Key] = attr.Value.GetStringValue()
			}
			for _, record := range scopeLogs.LogRecords {
				upload.bodies = append(upload.bodies, record.GetBody().GetStringValue())
				recordAttrs := make(map[string]string)
				for _, attr := range record.Attributes {
					recordAttrs[attr.Key] = attr.Value.GetStringValue()
					if attr.Key == "event.type" {
						upload.eventTypes = append(upload.eventTypes, attr.Value.GetStringValue())
					}
				}
				upload.recordAttrs = append(upload.recordAttrs, recordAttrs)
			}
		}
	}
	return upload, nil
}

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

func TestUploadSessionReportExportsConcurrentJobLocalTelemetry(t *testing.T) {
	uploads := make(chan uploadedSessionTelemetry, 2)
	errs := make(chan error, 4)
	var arrivals sync.WaitGroup
	arrivals.Add(2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/observability/logs/otlp/v0" {
			w.WriteHeader(http.StatusOK)
			return
		}
		arrivals.Done()
		arrivals.Wait()
		upload, err := decodeUploadedSessionTelemetry(req)
		if err != nil {
			errs <- err
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		uploads <- upload
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	oldClient := recordingUploadHTTPClient
	recordingUploadHTTPClient = server.Client()
	t.Cleanup(func() { recordingUploadHTTPClient = oldClient })
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", server.URL)

	cases := []struct {
		roomID    string
		room      string
		jobID     string
		agentName string
	}{
		{roomID: "RM_first", room: "room-first", jobID: "AJ_first", agentName: "agent-first"},
		{roomID: "RM_second", room: "room-second", jobID: "AJ_second", agentName: "agent-second"},
	}

	var wg sync.WaitGroup
	for _, tc := range cases {
		tc := tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			report := NewSessionReport()
			report.RecordingOptions = RecordingOptions{Logs: true, Transcript: true}
			report.RoomID = tc.roomID
			report.Room = tc.room
			report.JobID = tc.jobID
			report.ChatHistory.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "hello"})
			if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", tc.agentName, report); err != nil {
				errs <- fmt.Errorf("upload %s: %w", tc.jobID, err)
			}
		}()
	}
	wg.Wait()
	close(uploads)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	got := make(map[string]uploadedSessionTelemetry)
	for upload := range uploads {
		got[upload.attributes["job_id"]] = upload
	}
	if len(got) != len(cases) {
		t.Fatalf("OTLP uploads = %d, want %d", len(got), len(cases))
	}
	for _, tc := range cases {
		upload := got[tc.jobID]
		if !strings.HasPrefix(upload.authorization, "Bearer ") {
			t.Fatalf("%s Authorization = %q, want bearer token", tc.jobID, upload.authorization)
		}
		if upload.attributes["room_id"] != tc.roomID || upload.attributes["lk.agent_name"] != tc.agentName {
			t.Fatalf("%s resource attributes = %#v", tc.jobID, upload.attributes)
		}
		if upload.attributes["service.name"] != "livekit-agents" {
			t.Fatalf("%s service.name = %q, want livekit-agents", tc.jobID, upload.attributes["service.name"])
		}
		if countValue(upload.bodies, "session report") != 1 || countValue(upload.bodies, "chat item") != 1 {
			t.Fatalf("%s record bodies = %#v, want one session report and one chat item", tc.jobID, upload.bodies)
		}
		if len(upload.eventTypes) != 0 {
			t.Fatalf("%s event types = %#v, want none on session report logs", tc.jobID, upload.eventTypes)
		}
		if upload.scopeAttrs["room_id"] != tc.roomID || upload.scopeAttrs["job_id"] != tc.jobID || upload.scopeAttrs["room"] != tc.room {
			t.Fatalf("%s instrumentation scope attributes = %#v", tc.jobID, upload.scopeAttrs)
		}
		for i, attrs := range upload.recordAttrs {
			if attrs["room_id"] != tc.roomID || attrs["job_id"] != tc.jobID || attrs["logger.name"] != "chat_history" {
				t.Fatalf("%s log record attributes = %#v", tc.jobID, attrs)
			}
			for _, key := range []string{"event.type", "lk.agent_name", "room"} {
				if _, ok := attrs[key]; ok {
					t.Fatalf("%s log record attributes = %#v, want %s omitted", tc.jobID, attrs, key)
				}
			}
			if upload.bodies[i] == "session report" && attrs["agent_name"] != tc.agentName {
				t.Fatalf("%s session report attributes = %#v, want agent_name", tc.jobID, attrs)
			}
		}
	}
}

func TestEmitSessionReportTelemetryUsesExistingJobLogger(t *testing.T) {
	uploads := make(chan uploadedSessionTelemetry, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/observability/logs/otlp/v0" {
			upload, err := decodeUploadedSessionTelemetry(req)
			if err != nil {
				t.Errorf("decode telemetry: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			uploads <- upload
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	observability, err := telemetry.NewJobObservability(context.Background(), telemetry.JobObservabilityConfig{
		EndpointURL: server.URL,
		Headers:     map[string]string{"Authorization": "Bearer test"},
		HTTPClient:  server.Client(),
		JobID:       "AJ_existing",
		RoomID:      "RM_existing",
		AgentName:   "agent-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true, Transcript: true}
	report.JobID = "AJ_existing"
	report.RoomID = "RM_existing"
	report.Room = "room-existing"
	report.ChatHistory.AddMessage(llm.ChatMessageArgs{Role: llm.ChatRoleUser, Text: "hello"})

	if err := EmitSessionReportTelemetry(observability.Context(context.Background()), observability, "agent-a", report); err != nil {
		t.Fatal(err)
	}
	if err := observability.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := observability.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	select {
	case upload := <-uploads:
		if countValue(upload.bodies, "session report") != 1 || countValue(upload.bodies, "chat item") != 1 {
			t.Fatalf("record bodies = %#v", upload.bodies)
		}
		if len(upload.eventTypes) != 0 {
			t.Fatalf("event types = %#v, want none on session report logs", upload.eventTypes)
		}
		if upload.attributes["job_id"] != "AJ_existing" || upload.attributes["room_id"] != "RM_existing" || upload.attributes["lk.agent_name"] != "agent-a" {
			t.Fatalf("resource attributes = %#v", upload.attributes)
		}
		for i, attrs := range upload.recordAttrs {
			for _, key := range []string{"event.type", "lk.agent_name", "room"} {
				if _, ok := attrs[key]; ok {
					t.Fatalf("log record attributes = %#v, want %s omitted", attrs, key)
				}
			}
			if upload.bodies[i] == "session report" && attrs["agent_name"] != "agent-a" {
				t.Fatalf("session report attributes = %#v, want agent_name", attrs)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("job-local logger did not export final report")
	}
}

func TestUploadSessionRecordingSkipsOTLPLogs(t *testing.T) {
	var logRequests atomic.Int32
	var recordingRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/observability/logs/otlp/v0":
			logRequests.Add(1)
		case "/observability/recordings/v0":
			recordingRequests.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	oldClient := recordingUploadHTTPClient
	recordingUploadHTTPClient = server.Client()
	t.Cleanup(func() { recordingUploadHTTPClient = oldClient })
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", server.URL)

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true, Transcript: true}
	report.JobID = "AJ_recording_only"
	report.RoomID = "RM_recording_only"
	if err := UploadSessionRecording("wss://tenant.livekit.cloud", "key", "secret", report); err != nil {
		t.Fatal(err)
	}
	if got := recordingRequests.Load(); got != 1 {
		t.Fatalf("recording requests = %d, want 1", got)
	}
	if got := logRequests.Load(); got != 0 {
		t.Fatalf("log requests = %d, want 0", got)
	}
}

func TestUploadSessionReportReturnsOTLPExportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	oldClient := recordingUploadHTTPClient
	recordingUploadHTTPClient = server.Client()
	t.Cleanup(func() { recordingUploadHTTPClient = oldClient })
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", server.URL)

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Logs: true}
	report.RoomID = "RM_rejected"
	report.JobID = "AJ_rejected"
	err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("UploadSessionReport() error = %v, want OTLP 401 error", err)
	}
}

func TestUploadSessionReportOTLPFailureStillUploadsRecording(t *testing.T) {
	telemetryErr := errors.New("telemetry unavailable")
	oldTelemetryUpload := uploadSessionReportTelemetryFn
	uploadSessionReportTelemetryFn = func(context.Context, string, string, string, *SessionReport) error {
		return telemetryErr
	}
	t.Cleanup(func() { uploadSessionReportTelemetryFn = oldTelemetryUpload })

	recordingUploaded := false
	oldClient := recordingUploadHTTPClient
	recordingUploadHTTPClient = &http.Client{Transport: recordingUploadRoundTripper(func(req *http.Request) (*http.Response, error) {
		recordingUploaded = req.URL.Path == "/observability/recordings/v0"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	t.Cleanup(func() { recordingUploadHTTPClient = oldClient })
	t.Setenv("LIVEKIT_OBSERVABILITY_URL", "https://observability.test")

	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report)
	if !errors.Is(err, telemetryErr) {
		t.Fatalf("UploadSessionReport() error = %v, want telemetry error", err)
	}
	if !recordingUploaded {
		t.Fatal("recording upload was suppressed by OTLP failure")
	}
}

func TestUploadSessionReportNoWorkSkipsCredentialsAndHTTP(t *testing.T) {
	oldTelemetryUpload := uploadSessionReportTelemetryFn
	uploadSessionReportTelemetryFn = func(context.Context, string, string, string, *SessionReport) error {
		t.Fatal("empty report initialized OTLP telemetry")
		return nil
	}
	t.Cleanup(func() { uploadSessionReportTelemetryFn = oldTelemetryUpload })

	report := NewSessionReport()
	if err := UploadSessionReport("wss://tenant.livekit.cloud", "", "", "", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v, want no-op", err)
	}
}

func countValue(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
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

func TestUploadSessionReportHeaderIncludesJobID(t *testing.T) {
	headerCh := make(chan *livekit.MetricsRecordingHeader, 1)
	useRecordingUploadHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := multipartPartsFromRequest(t, r)
		header := &livekit.MetricsRecordingHeader{}
		if err := proto.Unmarshal(parts["header"], header); err != nil {
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
	report.RoomID = "RM_job_header"
	report.JobID = "AJ_job_header"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}

	select {
	case header := <-headerCh:
		if header.JobId != "AJ_job_header" {
			t.Fatalf("header JobId = %q, want AJ_job_header", header.JobId)
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
	useNoopSessionReportTelemetry(t)
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
	useNoopSessionReportTelemetry(t)
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
	useNoopSessionReportTelemetry(t)
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
	useNoopSessionReportTelemetry(t)
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
	useNoopSessionReportTelemetry(t)
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
	useNoopSessionReportTelemetry(t)
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
	messageItem := chatCtx.AddMessage(llm.ChatMessageArgs{
		Role:      llm.ChatRoleUser,
		Text:      "hello there",
		CreatedAt: createdAt,
	})
	confidence := 0.9
	messageItem.TranscriptConfidence = &confidence
	messageItem.Extra = map[string]any{"turn": 1}
	messageItem.Metrics = map[string]any{
		"started_speaking_at": 1800.0,
		"transcription_delay": 0.25,
		"end_of_turn_delay":   0.5,
	}
	callCreatedAt := createdAt.Add(time.Millisecond)
	chatCtx.Items = append(chatCtx.Items, &llm.FunctionCall{
		ID:        "call_1",
		CallID:    "call_lookup",
		Name:      "lookup",
		Arguments: `{"city":"Paris"}`,
		CreatedAt: callCreatedAt,
	})
	outputCreatedAt := createdAt.Add(2 * time.Millisecond)
	chatCtx.Items = append(chatCtx.Items, &llm.FunctionCallOutput{
		ID:        "out_1",
		CallID:    "call_lookup",
		Name:      "lookup",
		Output:    "tool failed",
		IsError:   true,
		CreatedAt: outputCreatedAt,
	})
	handoffCreatedAt := createdAt.Add(3 * time.Millisecond)
	chatCtx.Items = append(chatCtx.Items, &llm.AgentHandoff{
		ID:         "handoff_1",
		NewAgentID: "assistant",
		CreatedAt:  handoffCreatedAt,
	})
	configCreatedAt := createdAt.Add(4 * time.Millisecond)
	instructions := "be helpful"
	chatCtx.Items = append(chatCtx.Items, &llm.AgentConfigUpdate{
		ID:           "config_1",
		Instructions: &instructions,
		ToolsAdded:   []string{"lookup"},
		CreatedAt:    configCreatedAt,
	})
	report := NewSessionReport()
	report.RecordingOptions = RecordingOptions{Transcript: true}
	report.ChatHistory = chatCtx
	report.RoomID = "RM_chat"

	if err := UploadSessionReport("wss://tenant.livekit.cloud", "key", "secret", "agent-a", report); err != nil {
		t.Fatalf("UploadSessionReport() error = %v", err)
	}
	if len(events) != 6 {
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
	message, ok := item["message"].(map[string]any)
	if !ok || message["role"] != "USER" {
		t.Fatalf("chat.item = %#v, want wrapped user message", item)
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("chat.item content = %#v, want protobuf text content", message["content"])
	}
	text, textOK := content[0].(map[string]any)
	if !textOK || text["text"] != "hello there" {
		t.Fatalf("chat.item content = %#v, want protobuf text content", message["content"])
	}
	if message["created_at"] != "1970-01-01T00:30:00.125Z" {
		t.Fatalf("message created_at = %#v, want protobuf timestamp", message["created_at"])
	}
	if message["transcript_confidence"] != confidence || !reflect.DeepEqual(message["extra"], map[string]any{"turn": "1"}) {
		t.Fatalf("message metadata = %#v, want confidence and stringified extra", message)
	}
	metrics := message["metrics"].(map[string]any)
	if metrics["started_speaking_at"] != "1970-01-01T00:30:00Z" || metrics["transcription_delay"] != 0.25 || metrics["end_of_turn_delay"] != 0.5 {
		t.Fatalf("message metrics = %#v, want reference metrics", metrics)
	}
	call := events[2].attrs["chat.item"].(map[string]any)["function_call"].(map[string]any)
	if call["call_id"] != "call_lookup" || call["arguments"] != `{"city":"Paris"}` {
		t.Fatalf("function_call = %#v, want reference fields", call)
	}
	if events[3].eventType != "chat_item" || events[3].body != "chat item" {
		t.Fatalf("fourth telemetry event = %#v, want errored function output chat item event", events[3])
	}
	if !events[3].timestamp.Equal(outputCreatedAt) {
		t.Fatalf("function output event timestamp = %v, want item created_at %v", events[3].timestamp, outputCreatedAt)
	}
	if events[3].severity != "error" {
		t.Fatalf("function output event severity = %q, want error", events[3].severity)
	}
	output := events[3].attrs["chat.item"].(map[string]any)["function_call_output"].(map[string]any)
	if output["is_error"] != true || output["output"] != "tool failed" {
		t.Fatalf("function_call_output = %#v, want errored output", output)
	}
	handoff := events[4].attrs["chat.item"].(map[string]any)["agent_handoff"].(map[string]any)
	if handoff["new_agent_id"] != "assistant" {
		t.Fatalf("agent_handoff = %#v, want initial assistant handoff", handoff)
	}
	if _, ok := handoff["old_agent_id"]; ok {
		t.Fatalf("agent_handoff = %#v, want omitted old_agent_id", handoff)
	}
	config := events[5].attrs["chat.item"].(map[string]any)["agent_config_update"].(map[string]any)
	if config["instructions"] != instructions || !reflect.DeepEqual(config["tools_added"], []any{"lookup"}) {
		t.Fatalf("agent_config_update = %#v, want instructions and tools", config)
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
	useNoopSessionReportTelemetry(t)
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
	useNoopSessionReportTelemetry(t)
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
	useNoopSessionReportTelemetry(t)
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
	useNoopSessionReportTelemetry(t)
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

func useNoopSessionReportTelemetry(t *testing.T) {
	t.Helper()
	oldUpload := uploadSessionReportTelemetryFn
	uploadSessionReportTelemetryFn = func(context.Context, string, string, string, *SessionReport) error {
		return nil
	}
	t.Cleanup(func() {
		uploadSessionReportTelemetryFn = oldUpload
	})
}

type recordingUploadRoundTripper func(*http.Request) (*http.Response, error)

func (f recordingUploadRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
