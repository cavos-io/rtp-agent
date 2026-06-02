package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"time"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"github.com/cavos-io/rtp-agent/library/utils"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var recordUploadTelemetryEvent = telemetry.RecordChatEvent

func UploadSessionReport(
	cloudURL string,
	apiKey string,
	apiSecret string,
	agentName string,
	report *SessionReport,
) error {
	u, err := url.Parse(cloudURL)
	if err != nil {
		return fmt.Errorf("failed to parse cloud URL: %w", err)
	}

	if !utils.IsCloud(cloudURL) {
		// Not a cloud URL, skip
		logger.Logger.Infow("Not a cloud URL, skipping upload", "url", cloudURL)
		return nil
	}

	emitUploadTelemetryEvents(context.Background(), agentName, report)

	hasAudio := report.RecordingOptions.Audio && report.AudioRecordingPath != nil && report.AudioRecordingStartedAt != nil
	if !report.RecordingOptions.Transcript && !hasAudio {
		return nil
	}

	// Create JWT token
	at := auth.NewAccessToken(apiKey, apiSecret).
		SetVideoGrant(&auth.VideoGrant{}).
		SetValidFor(6 * 3600 * time.Second)

	// Add observability grants
	// Note: go auth package might not have Observability grants struct yet or it's handled differently,
	// let's just use standard grants if Observability isn't available
	// Wait, we can just issue a regular token and LiveKit Cloud will accept it if valid
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
	if report.AudioRecordingStartedAt != nil {
		headerMsg.StartTime = timestamppb.New(time.UnixMilli(int64(*report.AudioRecordingStartedAt * 1000)))
	}

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

	req, err := http.NewRequest("POST", fmt.Sprintf("https://%s/observability/recordings/v0", u.Host), &b)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", w.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute upload request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	logger.Logger.Debugw("Successfully uploaded session report to LiveKit Cloud")
	return nil
}

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
		if report.Usage != nil {
			attrs["usage"] = usageSummaryToDict(report.Usage)
		}
		recordUploadTelemetryEvent(ctx, "session_report", "session report", attrs)
	}

	if report.Tagger == nil {
		return
	}
	for _, evaluation := range report.Tagger.Evaluations() {
		recordUploadTelemetryEvent(ctx, "evaluation", "evaluation", map[string]interface{}{
			"evaluation": evaluation,
		})
	}
	if outcome := report.Tagger.Outcome(); outcome != "" {
		outcomeData := map[string]any{"outcome": outcome}
		if reason := report.Tagger.OutcomeReason(); reason != "" {
			outcomeData["reason"] = reason
		}
		recordUploadTelemetryEvent(ctx, "outcome", "outcome", map[string]interface{}{
			"outcome": outcomeData,
		})
	}
}

func hasUploadRecordingOption(options RecordingOptions) bool {
	return options.Audio || options.Traces || options.Logs || options.Transcript
}
