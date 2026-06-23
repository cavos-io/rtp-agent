package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	lkllm "github.com/cavos-io/rtp-agent/core/llm"
	lktts "github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func runTTSValueObjects(input json.RawMessage) (any, error) {
	var payload struct {
		Action        string            `json:"action"`
		Chunks        []string          `json:"chunks"`
		Transforms    []string          `json:"transforms"`
		Replacements  map[string]string `json:"replacements"`
		CaseSensitive bool              `json:"case_sensitive"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	orderedReplacements, err := orderedTTSReplacements(input, payload.Replacements)
	if err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "metadata_defaults"
	}
	provider := fakeScenarioTTS{}
	switch payload.Action {
	case "metadata_defaults":
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{
					"name":        "metadata_defaults",
					"model":       lktts.Model(provider),
					"provider":    lktts.Provider(provider),
					"sample_rate": provider.SampleRate(),
					"channels":    provider.NumChannels(),
					"streaming":   provider.Capabilities().Streaming,
				},
			},
		}, nil
	case "prewarm_noop":
		lktts.Prewarm(provider)
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{"name": "prewarm_noop", "error": false},
			},
		}, nil
	case "close_noop":
		err := lktts.Close(provider)
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{"name": "close_noop", "error": err != nil},
			},
		}, nil
	case "capabilities_json":
		data, marshalErr := json.Marshal(lktts.TTSCapabilities{
			Streaming:         true,
			AlignedTranscript: true,
		})
		if marshalErr != nil {
			return nil, marshalErr
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{
					"name":               "capabilities_json",
					"streaming":          payload["streaming"],
					"aligned_transcript": payload["aligned_transcript"],
					"has_camel_case":     hasAnyKey(payload, "Streaming", "AlignedTranscript"),
				},
			},
		}, nil
	case "capabilities_default_aligned":
		var caps lktts.TTSCapabilities
		if err := json.Unmarshal([]byte(`{"streaming":true}`), &caps); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{
					"name":               "capabilities_default_aligned",
					"streaming":          caps.Streaming,
					"aligned_transcript": caps.AlignedTranscript,
				},
			},
		}, nil
	case "capabilities_required_streaming":
		var missing lktts.TTSCapabilities
		err := json.Unmarshal([]byte(`{"aligned_transcript":true}`), &missing)
		var caps lktts.TTSCapabilities
		if unmarshalErr := json.Unmarshal([]byte(`{"streaming":true}`), &caps); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		return map[string]any{
			"contract": "tts-capabilities-required-streaming",
			"events": []map[string]any{
				{
					"name":               "capabilities_required_streaming",
					"missing_required":   err != nil && strings.Contains(err.Error(), "streaming"),
					"streaming":          caps.Streaming,
					"aligned_transcript": caps.AlignedTranscript,
				},
			},
		}, nil
	case "synthesized_audio_json":
		data, marshalErr := json.Marshal(lktts.SynthesizedAudio{
			RequestID: "req-a",
			IsFinal:   true,
			SegmentID: "segment-a",
			DeltaText: "hello",
		})
		if marshalErr != nil {
			return nil, marshalErr
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{
					"name":                 "synthesized_audio_json",
					"frame_is_none":        payload["frame"] == nil,
					"request_id":           payload["request_id"],
					"is_final":             payload["is_final"],
					"segment_id":           payload["segment_id"],
					"delta_text":           payload["delta_text"],
					"has_go_field_names":   hasAnyKey(payload, "RequestID", "IsFinal", "SegmentID", "DeltaText"),
					"has_timed_transcript": hasAnyKey(payload, "timed_transcript"),
				},
			},
		}, nil
	case "synthesized_audio_required_fields":
		requiredFields := []string{"frame", "request_id"}
		base := map[string]any{"frame": nil, "request_id": ""}
		missingFields := make([]string, 0, len(requiredFields))
		for _, fieldName := range requiredFields {
			payload := make(map[string]any, len(base)-1)
			for key, value := range base {
				if key != fieldName {
					payload[key] = value
				}
			}
			data, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var audio lktts.SynthesizedAudio
			err = json.Unmarshal(data, &audio)
			if err != nil && strings.Contains(err.Error(), fieldName) {
				missingFields = append(missingFields, fieldName)
			}
		}
		var audio lktts.SynthesizedAudio
		if err := json.Unmarshal([]byte(`{"frame":null,"request_id":""}`), &audio); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "tts-synthesized-audio-required-fields",
			"events": []map[string]any{
				{
					"name":           "synthesized_audio_required_fields",
					"missing_fields": missingFields,
					"frame_is_none":  audio.Frame == nil,
					"request_id":     audio.RequestID,
					"is_final":       audio.IsFinal,
					"segment_id":     audio.SegmentID,
					"delta_text":     audio.DeltaText,
				},
			},
		}, nil
	case "timed_string_json":
		data, marshalErr := json.Marshal(lktts.TimedString{
			Text:            "hello",
			StartTime:       0.25,
			EndTime:         0.5,
			Confidence:      0.875,
			StartTimeOffset: 1.25,
			SpeakerID:       "speaker-a",
		})
		if marshalErr != nil {
			return nil, marshalErr
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{
					"name":               "timed_string_json",
					"text":               payload["text"],
					"start_time":         payload["start_time"],
					"end_time":           payload["end_time"],
					"confidence":         payload["confidence"],
					"start_time_offset":  payload["start_time_offset"],
					"speaker_id":         payload["speaker_id"],
					"has_go_field_names": hasAnyKey(payload, "StartTime", "EndTime", "StartTimeOffset", "SpeakerID"),
				},
			},
		}, nil
	case "timed_string_optional_speaker":
		data, marshalErr := json.Marshal(lktts.TimedString{Text: "hello"})
		if marshalErr != nil {
			return nil, marshalErr
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		return map[string]any{
			"contract": "tts-timed-string-optional-speaker",
			"events": []map[string]any{
				{
					"name":            "timed_string_optional_speaker",
					"text":            payload["text"],
					"speaker_id":      payload["speaker_id"],
					"speaker_is_none": payload["speaker_id"] == nil,
				},
			},
		}, nil
	case "timed_string_text":
		timed := lktts.TimedString{
			Text:            "hello",
			StartTime:       0.25,
			EndTime:         0.5,
			Confidence:      0.875,
			StartTimeOffset: 1.25,
			SpeakerID:       "speaker-a",
		}
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{
					"name":                   "timed_string_text",
					"text":                   fmt.Sprint(timed),
					"repr_includes_metadata": false,
				},
			},
		}, nil
	case "timed_string_required_text":
		var missing lktts.TimedString
		err := json.Unmarshal([]byte(`{"start_time":0.25}`), &missing)
		var timed lktts.TimedString
		if unmarshalErr := json.Unmarshal([]byte(`{"text":"hello"}`), &timed); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		return map[string]any{
			"contract": "tts-timed-string-required-text",
			"events": []map[string]any{
				{
					"name":                      "timed_string_required_text",
					"missing_required":          err != nil && strings.Contains(err.Error(), "text"),
					"text":                      timed.Text,
					"start_time_default":        timed.StartTime,
					"end_time_default":          timed.EndTime,
					"confidence_default":        timed.Confidence,
					"start_time_offset_default": timed.StartTimeOffset,
				},
			},
		}, nil
	case "tts_error_payload":
		err := lktts.TTSError{
			Type:        lktts.TTSErrorType,
			Timestamp:   time.Now(),
			Label:       "tts",
			Err:         errors.New("provider disconnected"),
			Recoverable: true,
		}
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{
					"name":               "tts_error_payload",
					"type":               err.Type,
					"label":              err.Label,
					"recoverable":        err.Recoverable,
					"timestamp_positive": err.Timestamp.UnixNano() > 0,
					"error_message":      err.Error(),
				},
			},
		}, nil
	case "tts_error_json":
		err := lktts.TTSError{
			Type:        lktts.TTSErrorType,
			Timestamp:   time.Now(),
			Label:       "provider.TTS",
			Err:         errors.New("provider disconnected"),
			Recoverable: true,
		}
		data, marshalErr := json.Marshal(err)
		if marshalErr != nil {
			return nil, marshalErr
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		timestamp, _ := payload["timestamp"].(float64)
		return map[string]any{
			"contract": "tts-value-objects",
			"events": []map[string]any{
				{
					"name":               "tts_error_json",
					"type":               payload["type"],
					"label":              payload["label"],
					"recoverable":        payload["recoverable"],
					"timestamp_positive": timestamp > 0,
					"has_error_field":    hasAnyKey(payload, "error"),
					"has_err_field":      hasAnyKey(payload, "err"),
				},
			},
		}, nil
	case "tts_error_required_fields":
		requiredFields := []string{"timestamp", "label", "recoverable"}
		base := map[string]any{
			"timestamp":   1.25,
			"label":       "provider.TTS",
			"recoverable": true,
		}
		acceptedMissingFields := make([]string, 0, len(requiredFields))
		for _, fieldName := range requiredFields {
			payload := make(map[string]any, len(base)-1)
			for key, value := range base {
				if key != fieldName {
					payload[key] = value
				}
			}
			data, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			var ttsErr lktts.TTSError
			if err := json.Unmarshal(data, &ttsErr); err == nil {
				acceptedMissingFields = append(acceptedMissingFields, fieldName)
			}
		}
		var ttsErr lktts.TTSError
		if err := json.Unmarshal([]byte(`{"timestamp":1.25,"label":"provider.TTS","recoverable":true}`), &ttsErr); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "tts-error-required-fields",
			"events": []map[string]any{
				{
					"name":                    "tts_error_required_fields",
					"accepted_missing_fields": acceptedMissingFields,
					"type":                    ttsErr.Type,
					"timestamp":               float64(ttsErr.Timestamp.UnixNano()) / float64(1e9),
					"label":                   ttsErr.Label,
					"recoverable":             ttsErr.Recoverable,
				},
			},
		}, nil
	case "text_transform":
		transforms := payload.Transforms
		if !hasJSONField(input, "transforms") {
			transforms = []string{"filter_markdown"}
		}
		buffer, err := lktts.NewTextTransformBufferWithTransforms(transforms)
		if err != nil {
			return nil, err
		}
		chunks := []string{}
		for _, chunk := range payload.Chunks {
			chunks = append(chunks, buffer.Push(chunk)...)
		}
		chunks = append(chunks, buffer.Flush()...)
		joined := ""
		for _, chunk := range chunks {
			joined += chunk
		}
		return map[string]any{
			"contract": "tts-text-transforms",
			"events": []map[string]any{
				{
					"name":   "text_transform",
					"chunks": chunks,
					"joined": joined,
				},
			},
		}, nil
	case "text_replace":
		buffer := lktts.NewOrderedTextRegexReplaceBuffer(orderedReplacements, payload.CaseSensitive)
		chunks := []string{}
		for _, chunk := range payload.Chunks {
			chunks = append(chunks, buffer.Push(chunk)...)
		}
		chunks = append(chunks, buffer.Flush()...)
		joined := ""
		for _, chunk := range chunks {
			joined += chunk
		}
		containsOriginal := false
		for old := range payload.Replacements {
			if strings.Contains(joined, old) {
				containsOriginal = true
				break
			}
		}
		return map[string]any{
			"contract": "tts-text-replacements",
			"events": []map[string]any{
				{
					"name":              "text_replace",
					"joined":            joined,
					"contains_original": containsOriginal,
				},
			},
		}, nil
	case "text_replace_words":
		buffer := lktts.NewTextReplaceBuffer(payload.Replacements, false)
		chunks := []string{}
		for _, chunk := range payload.Chunks {
			chunks = append(chunks, buffer.Push(chunk)...)
		}
		chunks = append(chunks, buffer.Flush()...)
		joined := ""
		for _, chunk := range chunks {
			joined += chunk
		}
		return map[string]any{
			"contract": "tts-text-replacements",
			"events": []map[string]any{
				{
					"name":                  "text_replace_words",
					"joined":                joined,
					"workflow_preserved":    strings.Contains(joined, "workflow"),
					"substring_replaced":    strings.Contains(joined, "workstream"),
					"punctuation_preserved": strings.Contains(joined, "stream,"),
					"final_word_replaced":   strings.HasSuffix(joined, "stream!"),
				},
			},
		}, nil
	case "metrics_panic_isolated":
		requestIDs := make([]string, 0, 1)
		escapedError := false
		provider.OnMetricsCollected(func(*telemetry.TTSMetrics) {
			panic("metrics handler failed")
		})
		provider.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
			requestIDs = append(requestIDs, metrics.RequestID)
		})
		func() {
			defer func() {
				if recover() != nil {
					escapedError = true
				}
			}()
			provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "req-1"})
		}()
		return map[string]any{
			"contract": "tts-metrics-panic-isolated",
			"events": []map[string]any{
				{
					"name":          "metrics_panic_isolated",
					"request_ids":   requestIDs,
					"escaped_error": escapedError,
				},
			},
		}, nil
	case "metrics_unsubscribe":
		requestIDs := make([]string, 0, 1)
		unsubscribe := provider.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
			requestIDs = append(requestIDs, metrics.RequestID)
		})
		unsubscribe()
		unsubscribe()
		provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "after-unsubscribe"})
		return map[string]any{
			"contract": "tts-metrics-reference-unsubscribe",
			"events": []map[string]any{
				{
					"name":        "metrics_unsubscribe",
					"request_ids": requestIDs,
				},
			},
		}, nil
	case "error_panic_isolated":
		labels := make([]string, 0, 1)
		escapedError := false
		provider.OnError(func(lktts.TTSError) {
			panic("error handler failed")
		})
		provider.OnError(func(err lktts.TTSError) {
			labels = append(labels, err.Label)
		})
		func() {
			defer func() {
				if recover() != nil {
					escapedError = true
				}
			}()
			provider.EmitError(lktts.TTSError{Label: "tts", Err: errors.New("tts failed")})
		}()
		return map[string]any{
			"contract": "tts-error-panic-isolated",
			"events": []map[string]any{
				{
					"name":          "error_panic_isolated",
					"labels":        labels,
					"escaped_error": escapedError,
				},
			},
		}, nil
	case "error_unsubscribe":
		labels := make([]string, 0, 1)
		unsubscribe := provider.OnError(func(err lktts.TTSError) {
			labels = append(labels, err.Label)
		})
		unsubscribe()
		unsubscribe()
		provider.EmitError(lktts.TTSError{Label: "after-unsubscribe", Err: errors.New("tts failed")})
		return map[string]any{
			"contract": "tts-error-emitter-unsubscribe",
			"events": []map[string]any{
				{
					"name":   "error_unsubscribe",
					"labels": labels,
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported TTS value object action %q", payload.Action)
	}
}

func runTTSFallback(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
		Mode   string `json:"mode"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "model_provider"
	}
	switch payload.Action {
	case "model_provider":
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{fakeScenarioTTS{}})
		return map[string]any{
			"contract": "tts-fallback",
			"events": []map[string]any{
				{
					"name":        "model_provider",
					"model":       adapter.Model(),
					"provider":    adapter.Provider(),
					"sample_rate": adapter.SampleRate(),
					"channels":    adapter.NumChannels(),
				},
			},
		}, nil
	case "sample_rate":
		adapter := lktts.NewFallbackAdapterWithOptions([]lktts.TTS{
			fakeScenarioTTS{sampleRate: 16000},
			fakeScenarioTTS{sampleRate: 48000},
		}, lktts.FallbackAdapterOptions{SampleRate: 24000})
		return map[string]any{
			"contract": "tts-fallback",
			"events": []map[string]any{
				{
					"name":        "sample_rate",
					"sample_rate": adapter.SampleRate(),
					"channels":    adapter.NumChannels(),
					"streaming":   adapter.Capabilities().Streaming,
				},
			},
		}, nil
	case "prewarm":
		primary := &fakeScenarioTTS{}
		fallback := &fakeScenarioTTS{}
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{primary, fallback})
		adapter.Prewarm()
		return map[string]any{
			"contract": "tts-fallback",
			"events": []map[string]any{
				{
					"name":                   "prewarm",
					"primary_prewarm_calls":  primary.prewarmCalls,
					"fallback_prewarm_calls": fallback.prewarmCalls,
				},
			},
		}, nil
	case "provider_error_not_forwarded":
		primary := &fakeScenarioTTS{provider: "primary"}
		fallback := &fakeScenarioTTS{provider: "fallback"}
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{primary, fallback})
		labels := make([]string, 0, 3)
		unsubscribe := adapter.OnError(func(err lktts.TTSError) {
			labels = append(labels, err.Label)
		})
		defer unsubscribe()
		primary.EmitError(lktts.TTSError{Label: "primary", Err: errors.New("primary failed")})
		fallback.EmitError(lktts.TTSError{Label: "fallback", Err: errors.New("fallback failed")})
		adapter.EmitError(lktts.TTSError{Label: "adapter", Err: errors.New("adapter failed")})
		return map[string]any{
			"contract": "tts-fallback-provider-error-not-forwarded",
			"events": []map[string]any{
				{"name": "provider_error_not_forwarded", "labels": labels},
			},
		}, nil
	case "error_unsubscribe_local":
		primary := &fakeScenarioTTS{provider: "primary"}
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{primary})
		labels := make([]string, 0, 1)
		unsubscribe := adapter.OnError(func(err lktts.TTSError) {
			labels = append(labels, err.Label)
		})
		unsubscribe()
		adapter.EmitError(lktts.TTSError{Label: "adapter", Err: errors.New("adapter failed")})
		return map[string]any{
			"contract": "tts-fallback-error-unsubscribe-local",
			"events": []map[string]any{
				{"name": "error_unsubscribe_local", "labels": labels},
			},
		}, nil
	case "forward_metrics":
		primary := &fakeScenarioTTS{provider: "primary"}
		fallback := &fakeScenarioTTS{provider: "fallback"}
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{primary, fallback})
		requestIDs := make([]string, 0, 2)
		unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
			requestIDs = append(requestIDs, metrics.RequestID)
		})
		defer unsubscribe()
		primary.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "primary-req"})
		fallback.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "fallback-req"})
		return map[string]any{
			"contract": "tts-fallback-forward-metrics",
			"events": []map[string]any{
				{"name": "forward_metrics", "request_ids": requestIDs},
			},
		}, nil
	case "metrics_unsubscribe":
		primary := &fakeScenarioTTS{provider: "primary"}
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{primary})
		requestIDs := make([]string, 0, 1)
		unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
			requestIDs = append(requestIDs, metrics.RequestID)
		})
		unsubscribe()
		primary.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "primary-req"})
		adapter.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "adapter-req"})
		return map[string]any{
			"contract": "tts-fallback-metrics-unsubscribe",
			"events": []map[string]any{
				{"name": "metrics_unsubscribe", "request_ids": requestIDs},
			},
		}, nil
	case "close_unsubscribes_provider_metrics":
		primary := &fakeScenarioTTS{provider: "primary"}
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{primary})
		requestIDs := make([]string, 0, 2)
		unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
			requestIDs = append(requestIDs, metrics.RequestID)
		})
		defer unsubscribe()
		primary.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "before"})
		if err := adapter.Close(); err != nil {
			return nil, err
		}
		primary.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "after"})
		adapter.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "local"})
		return map[string]any{
			"contract": "tts-fallback-close-unsubscribes-provider-metrics",
			"events": []map[string]any{
				{"name": "close_unsubscribes_provider_metrics", "request_ids": requestIDs},
			},
		}, nil
	case "close_provider_ownership":
		primary := &fakeScenarioTTS{}
		fallback := &fakeScenarioTTS{}
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{primary, fallback})
		if err := adapter.Close(); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "tts-fallback-close-provider-ownership",
			"events": []map[string]any{
				{
					"name":                 "close_provider_ownership",
					"primary_close_calls":  primary.closeCalls,
					"fallback_close_calls": fallback.closeCalls,
				},
			},
		}, nil
	case "chunked_start_all_failed":
		var synthesizeCalls int
		primary := &fakeScenarioTTS{
			label:        "primary",
			provider:     "primary",
			chunkedError: lkllm.NewAPIConnectionError("provider unavailable"),
			chunkedCalls: &synthesizeCalls,
		}
		adapter := lktts.NewFallbackAdapterWithOptions([]lktts.TTS{primary}, lktts.FallbackAdapterOptions{DisableRetries: true})
		stream, err := adapter.Synthesize(context.Background(), "hello")
		streamCreated := err == nil
		errorClass := ""
		retryable := false
		hasAllFailed := false
		hasProviderLabel := false
		if err == nil {
			defer stream.Close()
			_, err = stream.Next()
		}
		if err != nil {
			var connectionErr *lkllm.APIConnectionError
			if errors.As(err, &connectionErr) {
				errorClass = "APIConnectionError"
				retryable = connectionErr.Retryable
			}
			message := err.Error()
			hasAllFailed = strings.Contains(message, "all TTSs failed")
			hasProviderLabel = strings.Contains(message, "primary")
		}
		waitForScenarioTTSCalls(&synthesizeCalls, 2)
		return map[string]any{
			"contract": "tts-fallback-chunked-start-all-failed",
			"events": []map[string]any{
				{
					"name":               "chunked_start_all_failed",
					"stream_created":     streamCreated,
					"error_class":        errorClass,
					"retryable":          retryable,
					"has_all_failed":     hasAllFailed,
					"has_provider_label": hasProviderLabel,
					"synthesize_calls":   synthesizeCalls,
				},
			},
		}, nil
	case "chunked_stream_all_failed":
		var synthesizeCalls int
		primary := &fakeScenarioTTS{
			label:              "primary",
			provider:           "primary",
			chunkedCalls:       &synthesizeCalls,
			chunkedStreamError: lkllm.NewAPIConnectionError("provider failed"),
		}
		adapter := lktts.NewFallbackAdapterWithOptions([]lktts.TTS{primary}, lktts.FallbackAdapterOptions{DisableRetries: true})
		stream, err := adapter.Synthesize(context.Background(), "hello")
		streamCreated := err == nil
		errorClass := ""
		retryable := false
		hasAllFailed := false
		hasProviderLabel := false
		if err == nil {
			defer stream.Close()
			_, err = stream.Next()
		}
		if err != nil {
			var connectionErr *lkllm.APIConnectionError
			if errors.As(err, &connectionErr) {
				errorClass = "APIConnectionError"
				retryable = connectionErr.Retryable
			}
			message := err.Error()
			hasAllFailed = strings.Contains(message, "all TTSs failed")
			hasProviderLabel = strings.Contains(message, "primary")
		}
		waitForScenarioTTSCalls(&synthesizeCalls, 2)
		return map[string]any{
			"contract": "tts-fallback-chunked-stream-all-failed",
			"events": []map[string]any{
				{
					"name":               "chunked_stream_all_failed",
					"stream_created":     streamCreated,
					"error_class":        errorClass,
					"retryable":          retryable,
					"has_all_failed":     hasAllFailed,
					"has_provider_label": hasProviderLabel,
					"synthesize_calls":   synthesizeCalls,
				},
			},
		}, nil
	case "chunked_client_closed":
		var primaryCalls int
		var fallbackCalls int
		primary := &fakeScenarioTTS{
			label:              "primary",
			provider:           "primary",
			chunkedCalls:       &primaryCalls,
			chunkedStreamError: lkllm.NewAPIStatusError("client closed", 499, "req_499", nil),
		}
		fallback := &fakeScenarioTTS{
			label:              "fallback",
			provider:           "fallback",
			chunkedCalls:       &fallbackCalls,
			chunkedStreamError: lkllm.NewAPIStatusError("client closed", 499, "req_499", nil),
		}
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{primary, fallback})
		errorEvents := make([]string, 0)
		unsubscribe := adapter.OnError(func(err lktts.TTSError) {
			errorEvents = append(errorEvents, err.Label)
		})
		defer unsubscribe()
		stream, err := adapter.Synthesize(context.Background(), "hello")
		eof := false
		audioSeen := false
		errorClass := ""
		hasNoAudio := false
		if err == nil {
			defer stream.Close()
			for {
				audio, nextErr := stream.Next()
				if audio != nil {
					audioSeen = true
				}
				if errors.Is(nextErr, io.EOF) {
					eof = true
					break
				}
				if nextErr != nil {
					err = nextErr
					break
				}
			}
		}
		if err != nil {
			var apiErr *lkllm.APIError
			if errors.As(err, &apiErr) {
				errorClass = "APIError"
			}
			hasNoAudio = strings.Contains(err.Error(), "no audio frames were pushed")
		}
		return map[string]any{
			"contract": "tts-fallback-chunked-client-closed",
			"events": []map[string]any{
				{
					"name":           "chunked_client_closed",
					"eof":            eof,
					"audio_seen":     audioSeen,
					"error_class":    errorClass,
					"has_no_audio":   hasNoAudio,
					"primary_calls":  primaryCalls,
					"fallback_calls": fallbackCalls,
					"error_events":   errorEvents,
				},
			},
		}, nil
	case "stream_start_all_failed":
		var streamCalls int
		primary := &fakeScenarioTTS{
			label:        "primary",
			provider:     "primary",
			capabilities: lktts.TTSCapabilities{Streaming: true},
			streamError:  lkllm.NewAPIConnectionError("provider unavailable"),
			streamCalls:  &streamCalls,
		}
		adapter := lktts.NewFallbackAdapterWithOptions([]lktts.TTS{primary}, lktts.FallbackAdapterOptions{DisableRetries: true})
		stream, err := adapter.Stream(context.Background())
		streamCreated := err == nil
		errorClass := ""
		retryable := false
		hasAllFailed := false
		hasProviderLabel := false
		if err == nil {
			defer stream.Close()
			if pushErr := stream.PushText("hello"); pushErr != nil {
				err = pushErr
			} else if endErr := lktts.EndSynthesizeStreamInput(stream); endErr != nil {
				err = endErr
			} else {
				_, err = stream.Next()
			}
		}
		if err != nil {
			var connectionErr *lkllm.APIConnectionError
			if errors.As(err, &connectionErr) {
				errorClass = "APIConnectionError"
				retryable = connectionErr.Retryable
			}
			message := err.Error()
			hasAllFailed = strings.Contains(message, "all TTSs failed")
			hasProviderLabel = strings.Contains(message, "primary")
		}
		return map[string]any{
			"contract": "tts-fallback-stream-start-all-failed",
			"events": []map[string]any{
				{
					"name":               "stream_start_all_failed",
					"stream_created":     streamCreated,
					"error_class":        errorClass,
					"retryable":          retryable,
					"has_all_failed":     hasAllFailed,
					"has_provider_label": hasProviderLabel,
					"stream_calls":       streamCalls,
				},
			},
		}, nil
	case "stream_stream_all_failed":
		var streamCalls int
		primary := &fakeScenarioTTS{
			label:             "primary",
			provider:          "primary",
			capabilities:      lktts.TTSCapabilities{Streaming: true},
			streamCalls:       &streamCalls,
			streamStreamError: lkllm.NewAPIConnectionError("provider failed"),
		}
		adapter := lktts.NewFallbackAdapterWithOptions([]lktts.TTS{primary}, lktts.FallbackAdapterOptions{DisableRetries: true})
		stream, err := adapter.Stream(context.Background())
		streamCreated := err == nil
		errorClass := ""
		retryable := false
		hasAllFailed := false
		hasProviderLabel := false
		if err == nil {
			defer stream.Close()
			if pushErr := stream.PushText("hello"); pushErr != nil {
				err = pushErr
			} else if endErr := lktts.EndSynthesizeStreamInput(stream); endErr != nil {
				err = endErr
			} else {
				_, err = stream.Next()
			}
		}
		if err != nil {
			var connectionErr *lkllm.APIConnectionError
			if errors.As(err, &connectionErr) {
				errorClass = "APIConnectionError"
				retryable = connectionErr.Retryable
			}
			message := err.Error()
			hasAllFailed = strings.Contains(message, "all TTSs failed")
			hasProviderLabel = strings.Contains(message, "primary")
		}
		waitForScenarioTTSCalls(&streamCalls, 2)
		return map[string]any{
			"contract": "tts-fallback-stream-stream-all-failed",
			"events": []map[string]any{
				{
					"name":               "stream_stream_all_failed",
					"stream_created":     streamCreated,
					"error_class":        errorClass,
					"retryable":          retryable,
					"has_all_failed":     hasAllFailed,
					"has_provider_label": hasProviderLabel,
					"stream_calls":       streamCalls,
				},
			},
		}, nil
	case "stream_client_closed":
		var primaryCalls int
		var fallbackCalls int
		primary := &fakeScenarioTTS{
			label:             "primary",
			provider:          "primary",
			capabilities:      lktts.TTSCapabilities{Streaming: true},
			streamCalls:       &primaryCalls,
			streamStreamError: lkllm.NewAPIStatusError("client closed", 499, "req_499", nil),
		}
		fallback := &fakeScenarioTTS{
			label:             "fallback",
			provider:          "fallback",
			capabilities:      lktts.TTSCapabilities{Streaming: true},
			streamCalls:       &fallbackCalls,
			streamStreamError: lkllm.NewAPIStatusError("client closed", 499, "req_499", nil),
		}
		adapter := lktts.NewFallbackAdapter([]lktts.TTS{primary, fallback})
		errorEvents := make([]string, 0)
		unsubscribe := adapter.OnError(func(err lktts.TTSError) {
			errorEvents = append(errorEvents, err.Label)
		})
		defer unsubscribe()
		stream, err := adapter.Stream(context.Background())
		eof := false
		audioSeen := false
		errorClass := ""
		hasNoAudio := false
		if err == nil {
			defer stream.Close()
			if pushErr := stream.PushText("hello"); pushErr != nil {
				err = pushErr
			} else if endErr := lktts.EndSynthesizeStreamInput(stream); endErr != nil {
				err = endErr
			} else {
				for {
					audio, nextErr := stream.Next()
					if audio != nil {
						audioSeen = true
					}
					if errors.Is(nextErr, io.EOF) {
						eof = true
						break
					}
					if nextErr != nil {
						err = nextErr
						break
					}
				}
			}
		}
		if err != nil {
			var apiErr *lkllm.APIError
			if errors.As(err, &apiErr) {
				errorClass = "APIError"
			}
			hasNoAudio = strings.Contains(err.Error(), "no audio frames were pushed")
		}
		return map[string]any{
			"contract": "tts-fallback-stream-client-closed",
			"events": []map[string]any{
				{
					"name":           "stream_client_closed",
					"eof":            eof,
					"audio_seen":     audioSeen,
					"error_class":    errorClass,
					"has_no_audio":   hasNoAudio,
					"primary_calls":  primaryCalls,
					"fallback_calls": fallbackCalls,
					"error_events":   errorEvents,
				},
			},
		}, nil
	case "availability_panic_isolated":
		primary := &fakeScenarioTTS{
			provider:     "primary",
			chunkedError: errors.New("primary unavailable"),
		}
		fallback := &fakeScenarioTTS{
			provider: "fallback",
			chunkedEvents: []*lktts.SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("ok")},
			}},
		}
		adapter := lktts.NewFallbackAdapterWithOptions([]lktts.TTS{primary, fallback}, lktts.FallbackAdapterOptions{DisableRetries: true})
		delivered := make([]map[string]any, 0, 1)
		adapter.OnAvailabilityChanged(func(event lktts.AvailabilityChangedEvent) {
			panic("availability handler failed")
		})
		adapter.OnAvailabilityChanged(func(event lktts.AvailabilityChangedEvent) {
			delivered = append(delivered, map[string]any{
				"provider":  "primary",
				"available": event.Available,
			})
		})
		if err := runTTSFallbackSynthesize(adapter); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "tts-fallback-availability-panic-isolated",
			"events": []map[string]any{
				{"name": "availability_panic_isolated", "delivered": delivered},
			},
		}, nil
	case "availability_unsubscribe":
		primary := &fakeScenarioTTS{
			provider:     "primary",
			chunkedError: errors.New("primary unavailable"),
		}
		fallback := &fakeScenarioTTS{
			provider: "fallback",
			chunkedEvents: []*lktts.SynthesizedAudio{{
				Frame: &model.AudioFrame{Data: []byte("ok")},
			}},
		}
		adapter := lktts.NewFallbackAdapterWithOptions([]lktts.TTS{primary, fallback}, lktts.FallbackAdapterOptions{DisableRetries: true})
		delivered := make([]map[string]any, 0, 1)
		unsubscribe := adapter.OnAvailabilityChanged(func(event lktts.AvailabilityChangedEvent) {
			delivered = append(delivered, map[string]any{
				"provider":  "primary",
				"available": event.Available,
			})
		})
		unsubscribe()
		if err := runTTSFallbackSynthesize(adapter); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "tts-fallback-availability-unsubscribe",
			"events": []map[string]any{
				{"name": "availability_unsubscribe", "delivered": delivered},
			},
		}, nil
	case "validation":
		mode := payload.Mode
		if mode == "" {
			mode = "empty"
		}
		message := capturePanicMessage(func() {
			switch mode {
			case "empty":
				lktts.NewFallbackAdapter(nil)
			case "mixed_channels":
				lktts.NewFallbackAdapter([]lktts.TTS{
					fakeScenarioTTS{numChannels: 1},
					fakeScenarioTTS{numChannels: 2},
				})
			default:
				panic(fmt.Sprintf("unsupported TTS fallback validation mode %q", mode))
			}
		})
		return map[string]any{
			"contract": "tts-fallback",
			"events": []map[string]any{
				{
					"name":        "validation",
					"mode":        mode,
					"error":       message != "",
					"error_class": boolErrorClass(message != ""),
					"message":     message,
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported TTS fallback action %q", payload.Action)
	}
}

func runTTSStreamAdapter(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "metadata"
	}
	provider := &fakeScenarioTTS{
		model:    "voice-model",
		provider: "voice-provider",
	}
	adapter := lktts.NewStreamAdapter(provider)
	switch payload.Action {
	case "metadata":
		caps := adapter.Capabilities()
		return map[string]any{
			"contract": "tts-stream-adapter",
			"events": []map[string]any{
				{
					"name":               "metadata",
					"model":              adapter.Model(),
					"provider":           adapter.Provider(),
					"sample_rate":        adapter.SampleRate(),
					"channels":           adapter.NumChannels(),
					"streaming":          caps.Streaming,
					"aligned_transcript": caps.AlignedTranscript,
				},
			},
		}, nil
	case "prewarm":
		adapter.Prewarm()
		return map[string]any{
			"contract": "tts-stream-adapter",
			"events": []map[string]any{
				{"name": "prewarm", "prewarm_calls": provider.prewarmCalls},
			},
		}, nil
	case "close":
		if err := adapter.Close(); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "tts-stream-adapter",
			"events": []map[string]any{
				{"name": "close", "close_calls": provider.closeCalls},
			},
		}, nil
	case "forward_metrics":
		requestIDs := make([]string, 0, 1)
		unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
			requestIDs = append(requestIDs, metrics.RequestID)
		})
		defer unsubscribe()
		provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "req-1"})
		return map[string]any{
			"contract": "tts-stream-adapter",
			"events": []map[string]any{
				{"name": "forward_metrics", "request_ids": requestIDs, "count": len(requestIDs)},
			},
		}, nil
	case "close_unsubscribes_provider_metrics":
		requestIDs := make([]string, 0, 2)
		unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
			requestIDs = append(requestIDs, metrics.RequestID)
		})
		defer unsubscribe()
		provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "before"})
		if err := adapter.Close(); err != nil {
			return nil, err
		}
		provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "after"})
		adapter.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "local"})
		return map[string]any{
			"contract": "tts-stream-adapter-close-unsubscribes-provider-metrics",
			"events": []map[string]any{
				{"name": "close_unsubscribes_provider_metrics", "request_ids": requestIDs},
			},
		}, nil
	case "unsubscribe_metrics":
		requestIDs := make([]string, 0, 1)
		unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
			requestIDs = append(requestIDs, metrics.RequestID)
		})
		provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "before"})
		unsubscribe()
		provider.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "provider"})
		adapter.EmitMetricsCollected(&telemetry.TTSMetrics{RequestID: "adapter"})
		return map[string]any{
			"contract": "tts-stream-adapter-metrics-unsubscribe",
			"events": []map[string]any{
				{"name": "unsubscribe_metrics", "request_ids": requestIDs},
			},
		}, nil
	case "provider_error_not_forwarded":
		labels := make([]string, 0, 2)
		unsubscribe := adapter.OnError(func(err lktts.TTSError) {
			labels = append(labels, err.Label)
		})
		defer unsubscribe()
		provider.EmitError(lktts.TTSError{Label: "provider", Err: errors.New("provider failed")})
		adapter.EmitError(lktts.TTSError{Label: "adapter", Err: errors.New("adapter failed")})
		return map[string]any{
			"contract": "tts-stream-adapter-provider-error-not-forwarded",
			"events": []map[string]any{
				{"name": "provider_error_not_forwarded", "labels": labels},
			},
		}, nil
	case "error_unsubscribe_local":
		labels := make([]string, 0, 1)
		unsubscribe := adapter.OnError(func(err lktts.TTSError) {
			labels = append(labels, err.Label)
		})
		unsubscribe()
		adapter.EmitError(lktts.TTSError{Label: "adapter", Err: errors.New("adapter failed")})
		return map[string]any{
			"contract": "tts-stream-adapter-error-unsubscribe",
			"events": []map[string]any{
				{"name": "error_unsubscribe_local", "labels": labels},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported TTS stream adapter action %q", payload.Action)
	}
}

func hasJSONField(input json.RawMessage, name string) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return false
	}
	_, ok := fields[name]
	return ok
}

func orderedTTSReplacements(input json.RawMessage, fallback map[string]string) ([]lktts.TextReplacement, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return nil, err
	}
	raw, ok := fields["replacements"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		keys := make([]string, 0, len(fallback))
		for old := range fallback {
			keys = append(keys, old)
		}
		sort.Strings(keys)
		ordered := make([]lktts.TextReplacement, 0, len(keys))
		for _, old := range keys {
			ordered = append(ordered, lktts.TextReplacement{Old: old, New: fallback[old]})
		}
		return ordered, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("replacements must be a JSON object")
	}

	ordered := []lktts.TextReplacement{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		old, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("replacement key must be a string")
		}
		var newValue string
		if err := decoder.Decode(&newValue); err != nil {
			return nil, err
		}
		ordered = append(ordered, lktts.TextReplacement{Old: old, New: newValue})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return ordered, nil
}

func runTTSFallbackSynthesize(adapter *lktts.FallbackAdapter) error {
	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		return err
	}
	defer stream.Close()
	_, err = stream.Next()
	return err
}

func waitForScenarioTTSCalls(calls *int, want int) {
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if calls != nil && *calls >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type fakeScenarioTTS struct {
	lktts.MetricsEmitter
	lktts.ErrorEmitter

	sampleRate         int
	numChannels        int
	capabilities       lktts.TTSCapabilities
	label              string
	model              string
	provider           string
	prewarmCalls       int
	closeCalls         int
	chunkedEvents      []*lktts.SynthesizedAudio
	chunkedError       error
	chunkedCalls       *int
	chunkedStreamError error
	streamError        error
	streamCalls        *int
	streamStreamError  error
}

func (t fakeScenarioTTS) Label() string {
	if t.label != "" {
		return t.label
	}
	return "fake-scenario-tts"
}
func (t fakeScenarioTTS) Capabilities() lktts.TTSCapabilities {
	return t.capabilities
}
func (t fakeScenarioTTS) SampleRate() int {
	if t.sampleRate != 0 {
		return t.sampleRate
	}
	return 24000
}
func (t fakeScenarioTTS) NumChannels() int {
	if t.numChannels != 0 {
		return t.numChannels
	}
	return 1
}
func (t fakeScenarioTTS) Model() string {
	return t.model
}
func (t fakeScenarioTTS) Provider() string {
	return t.provider
}
func (t *fakeScenarioTTS) Prewarm() {
	t.prewarmCalls++
}
func (t *fakeScenarioTTS) Close() error {
	t.closeCalls++
	return nil
}
func (t fakeScenarioTTS) Synthesize(context.Context, string) (lktts.ChunkedStream, error) {
	if t.chunkedCalls != nil {
		(*t.chunkedCalls)++
	}
	if t.chunkedError != nil {
		return nil, t.chunkedError
	}
	if t.chunkedStreamError != nil {
		return &fakeScenarioChunkedStream{err: t.chunkedStreamError}, nil
	}
	if t.chunkedEvents != nil {
		return &fakeScenarioChunkedStream{events: append([]*lktts.SynthesizedAudio(nil), t.chunkedEvents...)}, nil
	}
	return nil, nil
}
func (t fakeScenarioTTS) Stream(context.Context) (lktts.SynthesizeStream, error) {
	if t.streamCalls != nil {
		(*t.streamCalls)++
	}
	if t.streamError != nil {
		return nil, t.streamError
	}
	if t.streamStreamError != nil {
		return &fakeScenarioSynthesizeStream{err: t.streamStreamError}, nil
	}
	return nil, nil
}

type fakeScenarioSynthesizeStream struct {
	err error
}

func (*fakeScenarioSynthesizeStream) PushText(string) error {
	return nil
}

func (*fakeScenarioSynthesizeStream) Flush() error {
	return nil
}

func (*fakeScenarioSynthesizeStream) EndInput() error {
	return nil
}

func (*fakeScenarioSynthesizeStream) Close() error {
	return nil
}

func (s *fakeScenarioSynthesizeStream) Next() (*lktts.SynthesizedAudio, error) {
	if s.err != nil {
		return nil, s.err
	}
	return nil, io.EOF
}

type fakeScenarioChunkedStream struct {
	events []*lktts.SynthesizedAudio
	err    error
	index  int
}

func (s *fakeScenarioChunkedStream) Next() (*lktts.SynthesizedAudio, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (*fakeScenarioChunkedStream) Close() error {
	return nil
}
