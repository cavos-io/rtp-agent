package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	lktts "github.com/cavos-io/rtp-agent/core/tts"
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
	case "text_transform":
		transforms := payload.Transforms
		if len(transforms) == 0 {
			transforms = []string{"filter_markdown"}
		}
		for _, transform := range transforms {
			if transform != "filter_markdown" {
				return nil, fmt.Errorf("unsupported TTS text transform %q", transform)
			}
		}
		buffer := lktts.NewTextTransformBuffer()
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
		buffer := lktts.NewTextReplaceBuffer(payload.Replacements, payload.CaseSensitive)
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
	default:
		return nil, fmt.Errorf("unsupported TTS stream adapter action %q", payload.Action)
	}
}

type fakeScenarioTTS struct {
	sampleRate   int
	numChannels  int
	model        string
	provider     string
	prewarmCalls int
	closeCalls   int
}

func (fakeScenarioTTS) Label() string { return "fake-scenario-tts" }
func (fakeScenarioTTS) Capabilities() lktts.TTSCapabilities {
	return lktts.TTSCapabilities{}
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
func (fakeScenarioTTS) Synthesize(context.Context, string) (lktts.ChunkedStream, error) {
	return nil, nil
}
func (fakeScenarioTTS) Stream(context.Context) (lktts.SynthesizeStream, error) {
	return nil, nil
}
