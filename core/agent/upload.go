package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	livekitagent "github.com/livekit/protocol/livekit/agent"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	errAudioRecordingEmpty                = errors.New("audio recording is empty")
	errAudioRecordingPathMissing          = errors.New("audio recording path is missing")
	errAudioRecordingStartMissing         = errors.New("audio recording start timestamp is missing")
	errRecordingUploadFailed              = errors.New("upload failed")
	recordUploadTelemetryEvent            = telemetry.RecordChatEvent
	recordUploadTelemetryEventAt          = telemetry.RecordChatEventAt
	recordUploadTelemetryEventWithOptions = telemetry.RecordChatEventWithOptions
	uploadSessionReportTelemetryFn        = uploadSessionReportTelemetry
)

const (
	maxRecordingUploadRetries       = 3
	recordingUploadConnectTimeout   = 30 * time.Second
	recordingUploadInitialRetryWait = 100 * time.Millisecond
	recordingUploadMaxRetryWait     = 800 * time.Millisecond
	recordingUploadTimeout          = 15 * time.Minute
)

func newRecordingUploadHTTPClient() *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{Transport: http.DefaultTransport, Timeout: recordingUploadTimeout}
	}

	clonedTransport := transport.Clone()
	clonedTransport.DialContext = (&net.Dialer{Timeout: recordingUploadConnectTimeout}).DialContext

	return &http.Client{Transport: clonedTransport, Timeout: recordingUploadTimeout}
}

func UploadSessionReport(
	cloudURL string,
	apiKey string,
	apiSecret string,
	agentName string,
	report *SessionReport,
) error {
	observabilityURL, err := observabilityURLFromLiveKitURL(cloudURL)
	if err != nil {
		return err
	}
	if observabilityURL == "" {
		logger.Logger.Infow("Not a cloud URL, skipping upload", "url", cloudURL)
		return nil
	}
	report.ChatHistory = sanitizeSessionReportChatHistory(report.ChatHistory)

	hasRecording := report.RecordingOptions.Transcript || report.RecordingOptions.Audio
	hasTelemetry := hasUploadTelemetryEvents(report)
	if !hasRecording && !hasTelemetry {
		return nil
	}

	at := auth.NewAccessToken(apiKey, apiSecret).
		SetObservabilityGrant(&auth.ObservabilityGrant{Write: true}).
		SetValidFor(6 * 3600 * time.Second)

	jwt, err := at.ToJWT()
	if err != nil {
		return fmt.Errorf("failed to create JWT: %w", err)
	}

	var telemetryErr error
	if hasTelemetry {
		emitUploadTelemetryEvents(context.Background(), agentName, report)
		telemetryErr = uploadSessionReportTelemetryFn(context.Background(), observabilityURL, jwt, agentName, report)
	}
	var recordingErr error
	if hasRecording {
		recordingErr = uploadSessionRecording(observabilityURL, jwt, report)
	}
	return errors.Join(telemetryErr, recordingErr)
}

func UploadSessionRecording(
	cloudURL string,
	apiKey string,
	apiSecret string,
	report *SessionReport,
) error {
	if report == nil {
		return nil
	}
	observabilityURL, err := observabilityURLFromLiveKitURL(cloudURL)
	if err != nil {
		return err
	}
	if observabilityURL == "" {
		return nil
	}
	report.ChatHistory = sanitizeSessionReportChatHistory(report.ChatHistory)
	if !report.RecordingOptions.Transcript && !report.RecordingOptions.Audio {
		return nil
	}
	token, err := auth.NewAccessToken(apiKey, apiSecret).
		SetObservabilityGrant(&auth.ObservabilityGrant{Write: true}).
		SetValidFor(6 * time.Hour).
		ToJWT()
	if err != nil {
		return fmt.Errorf("failed to create JWT: %w", err)
	}

	return uploadSessionRecording(observabilityURL, token, report)
}

func uploadSessionRecording(observabilityURL string, jwt string, report *SessionReport) error {
	audioData, audioErr := sessionRecordingAudio(report)
	if !report.RecordingOptions.Transcript && len(audioData) == 0 {
		return audioErr
	}

	if audioErr != nil {
		logger.Logger.Warnw("audio omitted from session report upload", audioErr,
			"roomID", report.RoomID,
			"jobID", report.JobID,
		)
	}

	payload, contentType, err := buildSessionRecordingPayload(report, audioData)
	if err != nil {
		return errors.Join(audioErr, err)
	}

	return uploadSessionRecordingPayload(
		observabilityURL,
		jwt,
		report,
		payload,
		contentType,
		len(audioData),
		audioErr,
		newRecordingUploadHTTPClient().Do,
	)
}

func buildSessionRecordingPayload(report *SessionReport, audioData []byte) ([]byte, string, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	headerBytes, err := sessionRecordingHeaderBytes(report)
	if err != nil {
		return nil, "", err
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="header"; filename="header.binpb"`)
	h.Set("Content-Type", "application/protobuf")

	if err := writeMultipartPart(w, h, headerBytes); err != nil {
		return nil, "", fmt.Errorf("write header part: %w", err)
	}

	if report.RecordingOptions.Transcript {
		chatJSON, err := json.Marshal(report.ChatHistory.ToDict(llm.ChatContextDictOptions{
			IncludeImage:     true,
			IncludeAudio:     true,
			IncludeTimestamp: true,
		}))
		if err != nil {
			logger.Logger.Errorw("failed to marshal chat history", err)
		} else {
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", `form-data; name="chat_history"; filename="chat_history.json"`)
			h.Set("Content-Type", "application/json")
			h.Set("Content-Length", strconv.Itoa(len(chatJSON)))

			if err := writeMultipartPart(w, h, chatJSON); err != nil {
				return nil, "", fmt.Errorf("write chat history part: %w", err)
			}
		}
	}

	if len(audioData) > 0 {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="audio"; filename="recording.ogg"`)
		h.Set("Content-Type", "audio/ogg")
		h.Set("Content-Length", strconv.Itoa(len(audioData)))

		if err := writeMultipartPart(w, h, audioData); err != nil {
			return nil, "", fmt.Errorf("write audio part: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	return b.Bytes(), w.FormDataContentType(), nil
}

func sessionRecordingHeaderBytes(report *SessionReport) ([]byte, error) {
	header := &livekit.MetricsRecordingHeader{
		RoomId:           report.RoomID,
		JobId:            report.JobID,
		RedactionEnabled: report.RedactionEnabled,
	}

	startedAtMillis := int64(0)
	if report.AudioRecordingStartedAt != nil {
		startedAtMillis = int64(*report.AudioRecordingStartedAt * float64(time.Second/time.Millisecond))
	}

	header.StartTime = timestamppb.New(time.UnixMilli(startedAtMillis))

	headerBytes, err := proto.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal header msg: %w", err)
	}

	return headerBytes, nil
}

func writeMultipartPart(w *multipart.Writer, header textproto.MIMEHeader, data []byte) error {
	part, err := w.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create multipart part: %w", err)
	}

	_, err = part.Write(data)
	if err != nil {
		return fmt.Errorf("write multipart part: %w", err)
	}

	return nil
}

func uploadSessionRecordingPayload(
	observabilityURL string,
	jwt string,
	report *SessionReport,
	payload []byte,
	contentType string,
	audioBytes int,
	audioErr error,
	httpDo func(*http.Request) (*http.Response, error),
) error {
	uploadURL := fmt.Sprintf("%s/observability/recordings/v0", observabilityURL)
	for attempt := 0; attempt <= maxRecordingUploadRetries; attempt++ {
		logger.Logger.Debugw("uploading session report to LiveKit Cloud",
			"roomID", report.RoomID,
			"jobID", report.JobID,
			"audioBytes", audioBytes,
			"attempt", attempt+1,
		)
		req, err := http.NewRequest("POST", uploadURL, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
		req.Header.Set("Content-Type", contentType)

		resp, err := httpDo(req)
		if err != nil {
			if attempt < maxRecordingUploadRetries && recordingUploadConnectionRetryable(err) {
				time.Sleep(recordingUploadConnectionRetryDelay(attempt))

				continue
			}

			return errors.Join(audioErr, fmt.Errorf("failed to execute upload request: %w", err))
		}
		if resp.StatusCode < 400 {
			resp.Body.Close()
			logger.Logger.Debugw("finished uploading session report to LiveKit Cloud",
				"roomID", report.RoomID,
				"jobID", report.JobID,
				"audioBytes", audioBytes,
				"status", resp.StatusCode,
			)

			return audioErr
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		retryDelay, retryable := recordingUploadRetryDelay(resp, bodyBytes)
		resp.Body.Close()
		if !retryable || attempt == maxRecordingUploadRetries {
			return errors.Join(audioErr, fmt.Errorf("%w with status %d: %s", errRecordingUploadFailed, resp.StatusCode, string(bodyBytes)))
		}
		if retryDelay > 0 {
			time.Sleep(retryDelay)
		}
	}

	return audioErr
}

func sessionRecordingAudio(report *SessionReport) ([]byte, error) {
	if report == nil || !report.RecordingOptions.Audio {
		return nil, nil
	}

	if report.AudioRecordingPath == nil || strings.TrimSpace(*report.AudioRecordingPath) == "" {
		return nil, errAudioRecordingPathMissing
	}

	if report.AudioRecordingStartedAt == nil {
		return nil, errAudioRecordingStartMissing
	}

	audioData, err := os.ReadFile(*report.AudioRecordingPath)
	if err != nil {
		return nil, fmt.Errorf("read audio recording %q: %w", *report.AudioRecordingPath, err)
	}

	if len(audioData) == 0 {
		return nil, fmt.Errorf("%w: %s", errAudioRecordingEmpty, *report.AudioRecordingPath)
	}

	return audioData, nil
}

func recordingUploadConnectionRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}

	var requestErr *url.Error
	if !errors.As(err, &requestErr) {
		return false
	}

	if errors.Is(requestErr.Err, context.DeadlineExceeded) {
		return true
	}

	var networkErr net.Error

	return errors.As(requestErr.Err, &networkErr)
}

func recordingUploadConnectionRetryDelay(attempt int) time.Duration {
	delay := recordingUploadInitialRetryWait * time.Duration(1<<attempt)
	if delay > recordingUploadMaxRetryWait {
		return recordingUploadMaxRetryWait
	}

	return delay
}

func emitUploadTelemetryEvents(ctx context.Context, agentName string, report *SessionReport) {
	emitUploadTelemetryEventsWithRecorder(ctx, agentName, report, functionUploadTelemetryRecorder{})
}

type uploadTelemetryRecorder interface {
	recordAt(context.Context, string, string, map[string]interface{}, time.Time)
	recordWithOptions(context.Context, string, string, map[string]interface{}, telemetry.ChatEventOptions)
}

type functionUploadTelemetryRecorder struct{}

func (functionUploadTelemetryRecorder) recordAt(ctx context.Context, eventType string, body string, attrs map[string]interface{}, timestamp time.Time) {
	recordUploadTelemetryEventAt(ctx, eventType, body, attrs, timestamp)
}

func (functionUploadTelemetryRecorder) recordWithOptions(ctx context.Context, eventType string, body string, attrs map[string]interface{}, options telemetry.ChatEventOptions) {
	recordUploadTelemetryEventWithOptions(ctx, eventType, body, attrs, options)
}

func emitUploadTelemetryEventsWithRecorder(ctx context.Context, agentName string, report *SessionReport, recorder uploadTelemetryRecorder) {
	if report == nil {
		return
	}

	if hasUploadRecordingOption(report.RecordingOptions) {
		attrs := map[string]interface{}{
			"agent_name":               agentName,
			"sdk_version":              report.SDKVersion,
			"session.report_timestamp": report.Timestamp,
			"session.options":          sessionReportOptionsToDict(report.Options),
		}
		if report.Tagger != nil {
			tags := report.Tagger.Tags()
			if len(tags) > 0 {
				attrs["session.tags"] = tags
			} else {
				attrs["session.tags"] = nil
			}
		}
		if len(report.ModelUsage) > 0 {
			attrs["usage"] = modelUsageToDict(report.ModelUsage)
		} else {
			attrs["usage"] = nil
		}
		recorder.recordAt(ctx, "session_report", "session report", attrs, sessionReportTelemetryTimestamp(report))
	}
	if report.RecordingOptions.Transcript && report.ChatHistory != nil {
		for _, item := range report.ChatHistory.Items {
			itemLog, err := chatItemTelemetryDict(item)
			if err != nil {
				logger.Logger.Warnw("failed to encode chat item telemetry", err, "itemType", item.GetType())
				continue
			}
			if itemLog == nil {
				continue
			}
			createdAt := item.GetCreatedAt()
			attrs := map[string]interface{}{
				"chat.item": itemLog,
			}
			if output, ok := item.(*llm.FunctionCallOutput); ok && output.IsError {
				recorder.recordWithOptions(ctx, "chat_item", "chat item", attrs, telemetry.ErrorChatEventOptions(createdAt))
			} else {
				recorder.recordAt(ctx, "chat_item", "chat item", attrs, createdAt)
			}
		}
	}

	if report.Tagger == nil {
		return
	}
	reportTimestamp := unixSecondsToTime(report.Timestamp)
	for _, evaluation := range report.Tagger.Evaluations() {
		attrs := map[string]interface{}{
			"evaluation": evaluation,
		}
		if evaluation["verdict"] == "fail" {
			recorder.recordWithOptions(ctx, "evaluation", "evaluation", attrs, telemetry.ErrorChatEventOptions(reportTimestamp))
		} else {
			recorder.recordAt(ctx, "evaluation", "evaluation", attrs, reportTimestamp)
		}
	}
	for _, tag := range report.Tagger.MetadataTags() {
		recorder.recordAt(ctx, "tag", "tag", map[string]interface{}{
			"tag": map[string]any{
				"name":     tag.Name,
				"metadata": tag.Metadata,
			},
		}, tag.Timestamp)
	}
	if outcome := report.Tagger.Outcome(); outcome != "" {
		outcomeData := map[string]any{"outcome": outcome}
		if reason := report.Tagger.OutcomeReason(); reason != "" {
			outcomeData["reason"] = reason
		}
		attrs := map[string]interface{}{
			"outcome": outcomeData,
		}
		if outcome == "fail" {
			recorder.recordWithOptions(ctx, "outcome", "outcome", attrs, telemetry.ErrorChatEventOptions(reportTimestamp))
		} else {
			recorder.recordAt(ctx, "outcome", "outcome", attrs, reportTimestamp)
		}
	}
}

func chatItemTelemetryDict(item llm.ChatItem) (map[string]any, error) {
	var protoItem *livekitagent.ChatContext_ChatItem
	switch item := item.(type) {
	case *llm.ChatMessage:
		content := make([]*livekitagent.ChatMessage_ChatContent, 0, len(item.Content))
		for _, part := range item.Content {
			text := part.Text
			if text == "" && part.Instructions != nil {
				text = part.Instructions.String()
			} else if text == "" && (part.Image != nil || part.Audio != nil) {
				continue
			}
			content = append(content, &livekitagent.ChatMessage_ChatContent{
				Payload: &livekitagent.ChatMessage_ChatContent_Text{Text: text},
			})
		}
		extra := make(map[string]string, len(item.Extra))
		for key, value := range item.Extra {
			extra[key] = fmt.Sprint(value)
		}
		message := &livekitagent.ChatMessage{
			Id:                   item.ID,
			Role:                 telemetryChatRole(item.Role),
			Content:              content,
			Interrupted:          item.Interrupted,
			TranscriptConfidence: item.TranscriptConfidence,
			Extra:                extra,
			Metrics:              telemetryMetricsReport(item.Metrics),
			CreatedAt:            telemetryTimestamp(item.CreatedAt),
		}
		protoItem = &livekitagent.ChatContext_ChatItem{Item: &livekitagent.ChatContext_ChatItem_Message{Message: message}}
	case *llm.FunctionCall:
		call := &livekitagent.FunctionCall{
			Id:        item.ID,
			CallId:    item.CallID,
			Arguments: item.Arguments,
			Name:      item.Name,
			CreatedAt: telemetryTimestamp(item.CreatedAt),
		}
		protoItem = &livekitagent.ChatContext_ChatItem{Item: &livekitagent.ChatContext_ChatItem_FunctionCall{FunctionCall: call}}
	case *llm.FunctionCallOutput:
		output := &livekitagent.FunctionCallOutput{
			Id:        item.ID,
			Name:      item.Name,
			CallId:    item.CallID,
			Output:    item.Output,
			IsError:   item.IsError,
			CreatedAt: telemetryTimestamp(item.CreatedAt),
		}
		protoItem = &livekitagent.ChatContext_ChatItem{Item: &livekitagent.ChatContext_ChatItem_FunctionCallOutput{FunctionCallOutput: output}}
	case *llm.AgentHandoff:
		handoff := &livekitagent.AgentHandoff{
			Id:         item.ID,
			OldAgentId: item.OldAgentID,
			NewAgentId: item.NewAgentID,
			CreatedAt:  telemetryTimestamp(item.CreatedAt),
		}
		protoItem = &livekitagent.ChatContext_ChatItem{Item: &livekitagent.ChatContext_ChatItem_AgentHandoff{AgentHandoff: handoff}}
	case *llm.AgentConfigUpdate:
		instructions := item.Instructions
		if item.InstructionVariants != nil {
			text := item.InstructionVariants.String()
			instructions = &text
		}
		update := &livekitagent.AgentConfigUpdate{
			Id:           item.ID,
			Instructions: instructions,
			ToolsAdded:   item.ToolsAdded,
			ToolsRemoved: item.ToolsRemoved,
			CreatedAt:    telemetryTimestamp(item.CreatedAt),
		}
		protoItem = &livekitagent.ChatContext_ChatItem{Item: &livekitagent.ChatContext_ChatItem_AgentConfigUpdate{AgentConfigUpdate: update}}
	default:
		return nil, nil
	}

	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(protoItem)
	if err != nil {
		return nil, err
	}
	var itemLog map[string]any
	if err := json.Unmarshal(data, &itemLog); err != nil {
		return nil, err
	}
	return itemLog, nil
}

func telemetryChatRole(role llm.ChatRole) livekitagent.ChatRole {
	switch role {
	case llm.ChatRoleSystem:
		return livekitagent.ChatRole_SYSTEM
	case llm.ChatRoleUser:
		return livekitagent.ChatRole_USER
	case llm.ChatRoleAssistant:
		return livekitagent.ChatRole_ASSISTANT
	default:
		return livekitagent.ChatRole_DEVELOPER
	}
}

func telemetryTimestamp(value time.Time) *timestamppb.Timestamp {
	return timestamppb.New(time.UnixMilli(value.UnixMilli()))
}

func telemetryMetricsReport(metrics map[string]any) *livekitagent.MetricsReport {
	report := &livekitagent.MetricsReport{}
	if value, ok := telemetryFloat(metrics["started_speaking_at"]); ok {
		report.StartedSpeakingAt = timestamppb.New(time.UnixMilli(int64(value * 1000)))
	}
	if value, ok := telemetryFloat(metrics["stopped_speaking_at"]); ok {
		report.StoppedSpeakingAt = timestamppb.New(time.UnixMilli(int64(value * 1000)))
	}
	report.TranscriptionDelay = telemetryFloatPtr(metrics["transcription_delay"])
	report.EndOfTurnDelay = telemetryFloatPtr(metrics["end_of_turn_delay"])
	report.OnUserTurnCompletedDelay = telemetryFloatPtr(metrics["on_user_turn_completed_delay"])
	report.LlmNodeTtft = telemetryFloatPtr(metrics["llm_node_ttft"])
	report.TtsNodeTtfb = telemetryFloatPtr(metrics["tts_node_ttfb"])
	report.E2ELatency = telemetryFloatPtr(metrics["e2e_latency"])
	if report.StartedSpeakingAt == nil && report.StoppedSpeakingAt == nil &&
		report.TranscriptionDelay == nil && report.EndOfTurnDelay == nil &&
		report.OnUserTurnCompletedDelay == nil && report.LlmNodeTtft == nil &&
		report.TtsNodeTtfb == nil && report.E2ELatency == nil {
		return nil
	}
	return report
}

func telemetryFloatPtr(value any) *float64 {
	parsed, ok := telemetryFloat(value)
	if !ok {
		return nil
	}
	return &parsed
}

func telemetryFloat(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	case time.Duration:
		return value.Seconds(), true
	default:
		return 0, false
	}
}

func unixSecondsToTime(seconds float64) time.Time {
	return time.Unix(0, int64(math.Round(seconds*1e9)))
}

func sessionReportTelemetryTimestamp(report *SessionReport) time.Time {
	if report.StartedAt != nil {
		return unixSecondsToTime(*report.StartedAt)
	}
	return unixSecondsToTime(report.Timestamp)
}

func hasUploadRecordingOption(options RecordingOptions) bool {
	return options.Audio || options.Traces || options.Logs || options.Transcript
}

func hasUploadTelemetryEvents(report *SessionReport) bool {
	if report == nil {
		return false
	}
	if hasUploadRecordingOption(report.RecordingOptions) {
		return true
	}
	if report.Tagger == nil {
		return false
	}
	return len(report.Tagger.Evaluations()) > 0 || len(report.Tagger.MetadataTags()) > 0 || report.Tagger.Outcome() != ""
}

func observabilityURLFromLiveKitURL(liveKitURL string) (string, error) {
	if override := os.Getenv("LIVEKIT_OBSERVABILITY_URL"); override != "" {
		return strings.TrimRight(override, "/"), nil
	}

	u, err := url.Parse(liveKitURL)
	if err != nil {
		return "", nil
	}
	hostname := strings.ToLower(u.Hostname())
	if !utils.IsCloud(liveKitURL) || hostname == "" {
		return "", nil
	}
	return "https://" + hostname, nil
}

func recordingUploadRetryDelay(resp *http.Response, body []byte) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}
	value := resp.Header.Get("Retry-After")
	if value != "" {
		if seconds, err := strconv.Atoi(value); err == nil {
			if seconds < 0 {
				return 0, false
			}
			return time.Duration(seconds) * time.Second, true
		}
		retryAt, err := http.ParseTime(value)
		if err != nil {
			return 0, false
		}
		delay := time.Until(retryAt)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}

	var status statuspb.Status
	if err := proto.Unmarshal(body, &status); err != nil {
		return 0, false
	}
	for _, detail := range status.GetDetails() {
		var retryInfo errdetails.RetryInfo
		if detail.UnmarshalTo(&retryInfo) == nil {
			if retryInfo.GetRetryDelay() == nil {
				return 0, true
			}
			return retryInfo.GetRetryDelay().AsDuration(), true
		}
	}
	return 0, false
}
