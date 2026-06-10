package agent

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	recordUploadTelemetryEvent            = telemetry.RecordChatEvent
	recordUploadTelemetryEventAt          = telemetry.RecordChatEventAt
	recordUploadTelemetryEventWithOptions = telemetry.RecordChatEventWithOptions
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

	emitUploadTelemetryEvents(context.Background(), agentName, report)

	hasAudio := report.RecordingOptions.Audio && report.AudioRecordingPath != nil && report.AudioRecordingStartedAt != nil
	if !report.RecordingOptions.Transcript && !hasAudio {
		return nil
	}

	at := auth.NewAccessToken(apiKey, apiSecret).
		SetObservabilityGrant(&auth.ObservabilityGrant{Write: true}).
		SetValidFor(6 * 3600 * time.Second)

	jwt, err := at.ToJWT()
	if err != nil {
		return fmt.Errorf("failed to create JWT: %w", err)
	}

	// Prepare multipart writer
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	// 1. Header (protobuf)
	headerMsg := &livekit.MetricsRecordingHeader{
		RoomId: report.RoomID,
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
		chatJSON, err := json.Marshal(report.ChatHistory)
		if err != nil {
			logger.Logger.Errorw("failed to marshal chat history", err)
		} else {
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", `form-data; name="chat_history"; filename="chat_history.json"`)
			h.Set("Content-Type", "application/json")
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
			attrs["session.tags"] = report.Tagger.Tags()
		}
		if len(report.ModelUsage) > 0 {
			attrs["usage"] = modelUsageToDict(report.ModelUsage)
		}
		recordUploadTelemetryEventAt(ctx, "session_report", "session report", attrs, sessionReportTelemetryTimestamp(report))
	}
	if report.RecordingOptions.Transcript && report.ChatHistory != nil {
		for _, item := range report.ChatHistory.Items {
			recordUploadTelemetryEventAt(ctx, "chat_item", "chat item", map[string]interface{}{
				"chat.item": chatItemReportDict(item),
			}, item.GetCreatedAt())
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
			recordUploadTelemetryEventWithOptions(ctx, "evaluation", "evaluation", attrs, telemetry.ErrorChatEventOptions(reportTimestamp))
		} else {
			recordUploadTelemetryEventAt(ctx, "evaluation", "evaluation", attrs, reportTimestamp)
		}
	}
	for _, tag := range report.Tagger.MetadataTags() {
		recordUploadTelemetryEventAt(ctx, "tag", "tag", map[string]interface{}{
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
			recordUploadTelemetryEventWithOptions(ctx, "outcome", "outcome", attrs, telemetry.ErrorChatEventOptions(reportTimestamp))
		} else {
			recordUploadTelemetryEventAt(ctx, "outcome", "outcome", attrs, reportTimestamp)
		}
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

func observabilityURLFromLiveKitURL(liveKitURL string) (string, error) {
	if override := os.Getenv("LIVEKIT_OBSERVABILITY_URL"); override != "" {
		return strings.TrimRight(override, "/"), nil
	}

	u, err := url.Parse(liveKitURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse cloud URL: %w", err)
	}
	if !utils.IsCloud(liveKitURL) || u.Host == "" {
		return "", nil
	}
	return "https://" + u.Host, nil
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
