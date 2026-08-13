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
	recordUploadTelemetryEvent            = telemetry.RecordChatEvent
	recordUploadTelemetryEventAt          = telemetry.RecordChatEventAt
	recordUploadTelemetryEventWithOptions = telemetry.RecordChatEventWithOptions
	uploadSessionReportTelemetryFn        = uploadSessionReportTelemetry
	recordingUploadHTTPClient             = &http.Client{Timeout: 30 * time.Second}
)

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

	hasAudio := report.RecordingOptions.Audio && report.AudioRecordingPath != nil && report.AudioRecordingStartedAt != nil
	hasRecording := report.RecordingOptions.Transcript || hasAudio
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
		recordingErr = uploadSessionRecording(observabilityURL, jwt, report, hasAudio)
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
	hasAudio := report.RecordingOptions.Audio && report.AudioRecordingPath != nil && report.AudioRecordingStartedAt != nil
	if !report.RecordingOptions.Transcript && !hasAudio {
		return nil
	}
	token, err := auth.NewAccessToken(apiKey, apiSecret).
		SetObservabilityGrant(&auth.ObservabilityGrant{Write: true}).
		SetValidFor(6 * time.Hour).
		ToJWT()
	if err != nil {
		return fmt.Errorf("failed to create JWT: %w", err)
	}
	return uploadSessionRecording(observabilityURL, token, report, hasAudio)
}

func uploadSessionRecording(observabilityURL string, jwt string, report *SessionReport, hasAudio bool) error {
	// Prepare multipart writer
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	// 1. Header (protobuf)
	headerMsg := &livekit.MetricsRecordingHeader{
		RoomId: report.RoomID,
		JobId:  report.JobID,
	}
	startedAtMillis := int64(0)
	if report.AudioRecordingStartedAt != nil {
		startedAtMillis = int64(*report.AudioRecordingStartedAt * 1000)
	}
	headerMsg.StartTime = timestamppb.New(time.UnixMilli(startedAtMillis))

	headerBytes, err := proto.Marshal(headerMsg)
	if err != nil {
		return fmt.Errorf("failed to marshal header msg: %w", err)
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="header"; filename="header.binpb"`)
	h.Set("Content-Type", "application/protobuf")
	part, err := w.CreatePart(h)
	if err != nil {
		return fmt.Errorf("failed to create header part: %w", err)
	}
	part.Write(headerBytes)

	// 2. Chat history (JSON)
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
			part, err := w.CreatePart(h)
			if err == nil {
				part.Write(chatJSON)
			}
		}
	}

	// 3. Audio (Ogg)
	if hasAudio && report.AudioRecordingPath != nil {
		audioData, err := os.ReadFile(*report.AudioRecordingPath)
		if err != nil {
			logger.Logger.Errorw("failed to read audio file", err, "path", *report.AudioRecordingPath)
		} else if len(audioData) > 0 {
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", `form-data; name="audio"; filename="recording.ogg"`)
			h.Set("Content-Type", "audio/ogg")
			h.Set("Content-Length", strconv.Itoa(len(audioData)))
			part, err := w.CreatePart(h)
			if err == nil {
				part.Write(audioData)
			}
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	uploadURL := fmt.Sprintf("%s/observability/recordings/v0", observabilityURL)
	payload := b.Bytes()
	for attempt := 0; attempt <= maxRecordingUploadRetries; attempt++ {
		req, err := http.NewRequest("POST", uploadURL, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
		req.Header.Set("Content-Type", w.FormDataContentType())

		resp, err := recordingUploadHTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to execute upload request: %w", err)
		}
		if resp.StatusCode < 400 {
			resp.Body.Close()
			logger.Logger.Debugw("Successfully uploaded session report to LiveKit Cloud")
			return nil
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		retryDelay, retryable := recordingUploadRetryDelay(resp, bodyBytes)
		resp.Body.Close()
		if !retryable || attempt == maxRecordingUploadRetries {
			return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
		}
		if retryDelay > 0 {
			time.Sleep(retryDelay)
		}
	}

	return nil
}

const maxRecordingUploadRetries = 3

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
