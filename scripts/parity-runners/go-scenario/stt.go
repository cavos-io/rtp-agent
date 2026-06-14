package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	audiomodel "github.com/cavos-io/rtp-agent/core/audio/model"
	lkstt "github.com/cavos-io/rtp-agent/core/stt"
	lkvad "github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func runSTTValueObjects(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "speech_data_metadata"
	}
	switch payload.Action {
	case "metadata_defaults":
		provider := &fakeScenarioSTT{}
		lkstt.Prewarm(provider)
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":          "metadata_defaults",
					"model":         lkstt.Model(provider),
					"provider":      lkstt.Provider(provider),
					"prewarm_calls": provider.prewarmCalls,
				},
			},
		}, nil
	case "speech_data_metadata":
		data := lkstt.SpeechData{
			Language: "en",
			Text:     "hello",
			Words: []lkstt.TimedString{{
				Text:            "hello",
				StartTime:       0.1,
				EndTime:         0.4,
				Confidence:      0.95,
				StartTimeOffset: 1.2,
				SpeakerID:       "speaker-a",
			}},
			SourceLanguages: []string{"en-US"},
			SourceTexts:     []string{"hello"},
			TargetLanguages: []string{"es"},
			TargetTexts:     []string{"hola"},
			Metadata:        map[string]any{"provider": "test"},
		}
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":            "speech_data_metadata",
					"language":        data.Language,
					"text":            data.Text,
					"word_text":       data.Words[0].Text,
					"word_start":      data.Words[0].StartTime,
					"word_end":        data.Words[0].EndTime,
					"word_confidence": data.Words[0].Confidence,
					"word_offset":     data.Words[0].StartTimeOffset,
					"word_speaker":    data.Words[0].SpeakerID,
					"source_language": data.SourceLanguages[0],
					"source_text":     data.SourceTexts[0],
					"target_language": data.TargetLanguages[0],
					"target_text":     data.TargetTexts[0],
					"metadata":        data.Metadata["provider"],
				},
			},
		}, nil
	case "speech_data_optional_speaker":
		data, marshalErr := json.Marshal(lkstt.SpeechData{
			Language: "en",
			Text:     "hello",
			Words: []lkstt.TimedString{{
				Text: "hello",
			}},
		})
		if marshalErr != nil {
			return nil, marshalErr
		}
		var payload map[string]any
		if unmarshalErr := json.Unmarshal(data, &payload); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		words, _ := payload["words"].([]any)
		word, _ := words[0].(map[string]any)
		return map[string]any{
			"contract": "stt-speech-data-optional-speaker",
			"events": []map[string]any{
				{
					"name":                 "speech_data_optional_speaker",
					"speaker_id":           payload["speaker_id"],
					"speaker_is_none":      payload["speaker_id"] == nil,
					"word_speaker_id":      word["speaker_id"],
					"word_speaker_is_none": word["speaker_id"] == nil,
				},
			},
		}, nil
	case "speech_data_required_fields":
		requiredFields := []string{"language", "text"}
		base := map[string]any{"language": "", "text": ""}
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
			var speechData lkstt.SpeechData
			err = json.Unmarshal(data, &speechData)
			if err != nil && strings.Contains(err.Error(), fieldName) {
				missingFields = append(missingFields, fieldName)
			}
		}
		var speechData lkstt.SpeechData
		if err := json.Unmarshal([]byte(`{"language":"","text":""}`), &speechData); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "stt-speech-data-required-fields",
			"events": []map[string]any{
				{
					"name":           "speech_data_required_fields",
					"missing_fields": missingFields,
					"language":       speechData.Language,
					"text":           speechData.Text,
				},
			},
		}, nil
	case "timed_string_text":
		timed := lkstt.TimedString{
			Text:            "hello",
			StartTime:       0.25,
			EndTime:         0.5,
			Confidence:      0.875,
			StartTimeOffset: 1.25,
			SpeakerID:       "speaker-a",
		}
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":                   "timed_string_text",
					"text":                   fmt.Sprint(timed),
					"repr_includes_metadata": false,
				},
			},
		}, nil
	case "timed_string_required_text":
		var missing lkstt.TimedString
		err := json.Unmarshal([]byte(`{"start_time":0.25}`), &missing)
		var timed lkstt.TimedString
		if unmarshalErr := json.Unmarshal([]byte(`{"text":"hello"}`), &timed); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		return map[string]any{
			"contract": "stt-timed-string-required-text",
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
	case "timed_string_json":
		data, marshalErr := json.Marshal(lkstt.TimedString{
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
			"contract": "stt-value-objects",
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
	case "speech_event_usage":
		startTime := 42.5
		event := lkstt.SpeechEvent{
			Type:      lkstt.SpeechEventRecognitionUsage,
			RequestID: "req-1",
			RecognitionUsage: &lkstt.RecognitionUsage{
				AudioDuration: 1.25,
				InputTokens:   3,
				OutputTokens:  5,
			},
			SpeechStartTime: &startTime,
		}
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":              "speech_event_usage",
					"type":              event.Type,
					"request_id":        event.RequestID,
					"audio_duration":    event.RecognitionUsage.AudioDuration,
					"input_tokens":      event.RecognitionUsage.InputTokens,
					"output_tokens":     event.RecognitionUsage.OutputTokens,
					"speech_start_time": *event.SpeechStartTime,
				},
			},
		}, nil
	case "recognition_usage_required_duration":
		var missing lkstt.RecognitionUsage
		err := json.Unmarshal([]byte(`{"input_tokens":3,"output_tokens":5}`), &missing)
		missingField := ""
		if err != nil && strings.Contains(err.Error(), "audio_duration") {
			missingField = "audio_duration"
		}
		var zero lkstt.RecognitionUsage
		if err := json.Unmarshal([]byte(`{"audio_duration":0}`), &zero); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "stt-recognition-usage-required-field",
			"events": []map[string]any{
				{
					"name":                "recognition_usage_required_duration",
					"missing_required":    missingField == "audio_duration",
					"missing_field":       missingField,
					"zero_audio_duration": zero.AudioDuration,
					"zero_input_tokens":   zero.InputTokens,
					"zero_output_tokens":  zero.OutputTokens,
				},
			},
		}, nil
	case "speech_event_json_fields":
		isPrimary := true
		speechStartTime := 12.5
		event := lkstt.SpeechEvent{
			Type:      lkstt.SpeechEventRecognitionUsage,
			RequestID: "req-1",
			Alternatives: []lkstt.SpeechData{{
				Language:         "en",
				Text:             "hello",
				StartTime:        1.0,
				EndTime:          2.0,
				Confidence:       0.9,
				SpeakerID:        "speaker-a",
				IsPrimarySpeaker: &isPrimary,
				Words: []lkstt.TimedString{{
					Text:            "hello",
					StartTime:       1.0,
					EndTime:         2.0,
					Confidence:      0.9,
					StartTimeOffset: 0.25,
					SpeakerID:       "speaker-a",
				}},
				SourceLanguages: []string{"en-US"},
				SourceTexts:     []string{"hello"},
				TargetLanguages: []string{"es"},
				TargetTexts:     []string{"hola"},
				Metadata:        map[string]any{"provider": "test"},
			}},
			RecognitionUsage: &lkstt.RecognitionUsage{AudioDuration: 1.25, InputTokens: 3, OutputTokens: 5},
			SpeechStartTime:  &speechStartTime,
		}
		data, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		alternatives, _ := payload["alternatives"].([]any)
		alternative, _ := alternatives[0].(map[string]any)
		words, _ := alternative["words"].([]any)
		word, _ := words[0].(map[string]any)
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":                           "speech_event_json_fields",
					"type":                           payload["type"],
					"request_id":                     payload["request_id"],
					"speech_start_time":              payload["speech_start_time"],
					"has_recognition_usage":          payload["recognition_usage"] != nil,
					"has_camel_case":                 hasAnyKey(payload, "RequestID") || hasAnyKey(alternative, "StartTime"),
					"has_target_only_interrupted":    hasAnyKey(payload, "interrupted"),
					"alternative_start_time":         alternative["start_time"],
					"alternative_end_time":           alternative["end_time"],
					"alternative_speaker_id":         alternative["speaker_id"],
					"alternative_is_primary_speaker": alternative["is_primary_speaker"],
					"word_start_time_offset":         word["start_time_offset"],
					"word_speaker_id":                word["speaker_id"],
				},
			},
		}, nil
	case "speech_event_empty_alternatives_marshal":
		data, err := json.Marshal(lkstt.SpeechEvent{Type: lkstt.SpeechEventEndOfSpeech})
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		alternatives, ok := payload["alternatives"].([]any)
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":                 "speech_event_empty_alternatives_marshal",
					"type":                 payload["type"],
					"alternatives_is_list": ok,
					"alternatives_length":  len(alternatives),
				},
			},
		}, nil
	case "speech_event_empty_alternatives_unmarshal":
		var event lkstt.SpeechEvent
		data := []byte(`{"type":"end_of_speech","request_id":"req-1"}`)
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":                 "speech_event_empty_alternatives_unmarshal",
					"type":                 event.Type,
					"request_id":           event.RequestID,
					"alternatives_is_list": event.Alternatives != nil,
					"alternatives_length":  len(event.Alternatives),
				},
			},
		}, nil
	case "speech_event_required_type":
		var missing lkstt.SpeechEvent
		err := json.Unmarshal([]byte(`{"request_id":"req-1"}`), &missing)
		missingField := ""
		if err != nil && strings.Contains(err.Error(), "type") {
			missingField = "type"
		}
		var event lkstt.SpeechEvent
		if err := json.Unmarshal([]byte(`{"type":"end_of_speech","request_id":"req-1"}`), &event); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "stt-speech-event-required-type",
			"events": []map[string]any{
				{
					"name":                 "speech_event_required_type",
					"missing_required":     missingField == "type",
					"missing_field":        missingField,
					"type":                 event.Type,
					"request_id":           event.RequestID,
					"alternatives_is_list": event.Alternatives != nil,
					"alternatives_length":  len(event.Alternatives),
				},
			},
		}, nil
	case "stt_error_payload":
		underlying := errors.New("provider disconnected")
		sttErr := lkstt.NewSTTError("provider.STT", underlying, true)
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":               "stt_error_payload",
					"type":               sttErr.Type,
					"label":              sttErr.Label,
					"recoverable":        sttErr.Recoverable,
					"timestamp_positive": sttErr.Timestamp.UnixNano() > 0,
					"error_message":      sttErr.Error(),
				},
			},
		}, nil
	case "stt_error_json":
		sttErr := lkstt.NewSTTError("provider.STT", errors.New("provider disconnected"), true)
		data, err := json.Marshal(sttErr)
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		timestamp, _ := payload["timestamp"].(float64)
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":               "stt_error_json",
					"type":               payload["type"],
					"label":              payload["label"],
					"recoverable":        payload["recoverable"],
					"timestamp_positive": timestamp > 0,
					"has_error_field":    hasAnyKey(payload, "error"),
					"has_err_field":      hasAnyKey(payload, "err"),
				},
			},
		}, nil
	case "capabilities_json":
		data, err := json.Marshal(lkstt.STTCapabilities{
			Streaming:         true,
			InterimResults:    true,
			Diarization:       true,
			AlignedTranscript: "word",
			OfflineRecognize:  true,
		})
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":               "capabilities_json",
					"streaming":          payload["streaming"],
					"interim_results":    payload["interim_results"],
					"diarization":        payload["diarization"],
					"aligned_transcript": payload["aligned_transcript"],
					"offline_recognize":  payload["offline_recognize"],
					"has_camel_case":     hasAnyKey(payload, "Streaming", "InterimResults", "AlignedTranscript"),
				},
			},
		}, nil
	case "capabilities_missing_aligned":
		data, err := json.Marshal(lkstt.STTCapabilities{
			Streaming:        true,
			InterimResults:   true,
			OfflineRecognize: true,
		})
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{"name": "capabilities_missing_aligned", "aligned_transcript": payload["aligned_transcript"]},
			},
		}, nil
	case "capabilities_unmarshal_defaults":
		var caps lkstt.STTCapabilities
		data := []byte(`{"streaming":true,"interim_results":true,"diarization":false,"aligned_transcript":false}`)
		if err := json.Unmarshal(data, &caps); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "stt-value-objects",
			"events": []map[string]any{
				{
					"name":               "capabilities_unmarshal_defaults",
					"streaming":          caps.Streaming,
					"interim_results":    caps.InterimResults,
					"diarization":        caps.Diarization,
					"aligned_transcript": caps.AlignedTranscript,
					"offline_recognize":  caps.OfflineRecognize,
				},
			},
		}, nil
	case "capabilities_required_fields":
		requiredFields := []string{"streaming", "interim_results"}
		base := map[string]any{"streaming": true, "interim_results": true}
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
			var caps lkstt.STTCapabilities
			err = json.Unmarshal(data, &caps)
			if err != nil && strings.Contains(err.Error(), fieldName) {
				missingFields = append(missingFields, fieldName)
			}
		}
		var caps lkstt.STTCapabilities
		if err := json.Unmarshal([]byte(`{"streaming":true,"interim_results":true}`), &caps); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "stt-capabilities-required-fields",
			"events": []map[string]any{
				{
					"name":               "capabilities_required_fields",
					"missing_fields":     missingFields,
					"streaming":          caps.Streaming,
					"interim_results":    caps.InterimResults,
					"diarization":        caps.Diarization,
					"aligned_transcript": caps.AlignedTranscript,
					"offline_recognize":  caps.OfflineRecognize,
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported STT value object action %q", payload.Action)
	}
}

func runSTTFallback(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
		Mode   string `json:"mode"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "metadata"
	}
	switch payload.Action {
	case "metadata":
		adapter := lkstt.NewFallbackAdapter([]lkstt.STT{fakeScenarioSTT{label: "primary", capabilities: lkstt.STTCapabilities{Streaming: true}}})
		return map[string]any{
			"contract": "stt-fallback",
			"events": []map[string]any{
				{
					"name":     "metadata",
					"model":    lkstt.Model(adapter),
					"provider": lkstt.Provider(adapter),
				},
			},
		}, nil
	case "option_defaults":
		options := lkstt.DefaultFallbackAdapterOptions()
		return map[string]any{
			"contract": "stt-fallback",
			"events": []map[string]any{
				{
					"name":                    "option_defaults",
					"max_retry_per_stt":       options.MaxRetryPerSTT,
					"attempt_timeout_seconds": options.AttemptTimeout.Seconds(),
					"retry_interval_seconds":  options.RetryInterval.Seconds(),
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
				lkstt.NewFallbackAdapter(nil)
			case "nonstreaming":
				lkstt.NewFallbackAdapter([]lkstt.STT{fakeScenarioSTT{label: "offline", capabilities: lkstt.STTCapabilities{OfflineRecognize: true}}})
			case "all_nonstreaming":
				lkstt.NewFallbackAdapter([]lkstt.STT{
					fakeScenarioSTT{label: "offline-a", capabilities: lkstt.STTCapabilities{OfflineRecognize: true}},
					fakeScenarioSTT{label: "offline-b", capabilities: lkstt.STTCapabilities{OfflineRecognize: true}},
				})
			default:
				panic(fmt.Sprintf("unsupported STT fallback validation mode %q", mode))
			}
		})
		return map[string]any{
			"contract": "stt-fallback",
			"events": []map[string]any{
				{
					"name":        "validation_" + mode,
					"error":       message != "",
					"error_class": sttFallbackErrorClass(message != ""),
					"message":     message,
				},
			},
		}, nil
	case "capabilities":
		mode := payload.Mode
		if mode == "" {
			mode = "aggregate"
		}
		var adapter *lkstt.FallbackAdapter
		switch mode {
		case "aggregate":
			adapter = lkstt.NewFallbackAdapter([]lkstt.STT{
				fakeScenarioSTT{label: "primary", capabilities: lkstt.STTCapabilities{
					Streaming:        true,
					InterimResults:   true,
					Diarization:      true,
					OfflineRecognize: false,
				}},
				fakeScenarioSTT{label: "fallback", capabilities: lkstt.STTCapabilities{
					Streaming:        true,
					InterimResults:   false,
					Diarization:      false,
					OfflineRecognize: true,
				}},
			})
		case "offline_advertised":
			adapter = lkstt.NewFallbackAdapter([]lkstt.STT{
				fakeScenarioSTT{label: "primary", capabilities: lkstt.STTCapabilities{Streaming: true, OfflineRecognize: false}},
				fakeScenarioSTT{label: "fallback", capabilities: lkstt.STTCapabilities{Streaming: true, OfflineRecognize: false}},
			})
		case "aligned_primary":
			adapter = lkstt.NewFallbackAdapter([]lkstt.STT{
				fakeScenarioSTT{label: "primary", capabilities: lkstt.STTCapabilities{Streaming: true, AlignedTranscript: "word"}},
				fakeScenarioSTT{label: "fallback", capabilities: lkstt.STTCapabilities{Streaming: true, AlignedTranscript: "chunk"}},
			})
		case "aligned_cleared":
			adapter = lkstt.NewFallbackAdapter([]lkstt.STT{
				fakeScenarioSTT{label: "primary", capabilities: lkstt.STTCapabilities{Streaming: true, AlignedTranscript: "word"}},
				fakeScenarioSTT{label: "fallback", capabilities: lkstt.STTCapabilities{Streaming: true}},
			})
		default:
			return nil, fmt.Errorf("unsupported STT fallback capabilities mode %q", mode)
		}
		caps := adapter.Capabilities()
		return map[string]any{
			"contract": "stt-fallback",
			"events": []map[string]any{
				{
					"name":               "capabilities_" + mode,
					"streaming":          caps.Streaming,
					"interim_results":    caps.InterimResults,
					"diarization":        caps.Diarization,
					"aligned_transcript": caps.AlignedTranscript,
					"offline_recognize":  caps.OfflineRecognize,
				},
			},
		}, nil
	case "vad_wrap":
		adapter := lkstt.NewFallbackAdapterWithVAD(
			[]lkstt.STT{fakeScenarioSTT{label: "offline", capabilities: lkstt.STTCapabilities{OfflineRecognize: true}}},
			fakeScenarioVAD{},
		)
		caps := adapter.Capabilities()
		return map[string]any{
			"contract": "stt-fallback",
			"events": []map[string]any{
				{
					"name":               "vad_wrap",
					"streaming":          caps.Streaming,
					"interim_results":    caps.InterimResults,
					"diarization":        caps.Diarization,
					"aligned_transcript": caps.AlignedTranscript,
					"offline_recognize":  caps.OfflineRecognize,
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported STT fallback action %q", payload.Action)
	}
}

type fakeScenarioVAD struct{}

func (fakeScenarioVAD) Label() string                       { return "scenario.VAD" }
func (fakeScenarioVAD) Model() string                       { return "" }
func (fakeScenarioVAD) Provider() string                    { return "" }
func (fakeScenarioVAD) Capabilities() lkvad.VADCapabilities { return lkvad.VADCapabilities{} }
func (fakeScenarioVAD) OnMetricsCollected(lkvad.VADMetricsHandler) func() {
	return func() {}
}
func (fakeScenarioVAD) Stream(context.Context) (lkvad.VADStream, error) {
	return fakeScenarioVADStream{}, nil
}

type fakeScenarioVADStream struct{}

func (fakeScenarioVADStream) PushFrame(*audiomodel.AudioFrame) error { return nil }
func (fakeScenarioVADStream) Flush() error                           { return nil }
func (fakeScenarioVADStream) EndInput() error                        { return nil }
func (fakeScenarioVADStream) Close() error                           { return nil }
func (fakeScenarioVADStream) Next() (*lkvad.VADEvent, error)         { return nil, io.EOF }

func runSTTStreamAdapter(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "capabilities"
	}
	switch payload.Action {
	case "capabilities":
		adapter := lkstt.NewStreamAdapter(fakeScenarioSTT{label: "wrapped", capabilities: lkstt.STTCapabilities{OfflineRecognize: true}}, nil)
		caps := adapter.Capabilities()
		return map[string]any{
			"contract": "stt-stream-adapter",
			"events": []map[string]any{
				{
					"name":               "capabilities",
					"streaming":          caps.Streaming,
					"interim_results":    caps.InterimResults,
					"diarization":        caps.Diarization,
					"aligned_transcript": caps.AlignedTranscript,
					"offline_recognize":  caps.OfflineRecognize,
				},
			},
		}, nil
	case "wrapped":
		wrapped := &fakeScenarioSTT{label: "wrapped", capabilities: lkstt.STTCapabilities{OfflineRecognize: true}}
		adapter := lkstt.NewStreamAdapter(wrapped, nil)
		return map[string]any{
			"contract": "stt-stream-adapter",
			"events": []map[string]any{
				{
					"name":          "wrapped",
					"same_instance": adapter.WrappedSTT() == wrapped,
					"wrapped_label": adapter.WrappedSTT().Label(),
				},
			},
		}, nil
	case "public_wrapper":
		var wrapper *lkstt.StreamAdapterWrapper
		_, isRecognizeStream := any(wrapper).(lkstt.RecognizeStream)
		_, hasTiming := any(wrapper).(lkstt.StreamTiming)
		_, hasEndInput := any(wrapper).(lkstt.InputEnding)
		return map[string]any{
			"contract": "stt-stream-adapter",
			"events": []map[string]any{
				{
					"name":                  "public_wrapper",
					"type_name":             "StreamAdapterWrapper",
					"is_recognize_stream":   isRecognizeStream,
					"has_push_frame":        isRecognizeStream,
					"has_flush":             isRecognizeStream,
					"has_end_input":         hasEndInput,
					"has_start_time_offset": hasTiming,
					"has_start_time":        hasTiming,
				},
			},
		}, nil
	case "forward_metrics":
		wrapped := &fakeScenarioSTT{label: "wrapped", capabilities: lkstt.STTCapabilities{OfflineRecognize: true}}
		adapter := lkstt.NewStreamAdapter(wrapped, nil)
		requestIDs := make([]string, 0, 1)
		unsubscribe := adapter.OnMetricsCollected(func(metrics *telemetry.STTMetrics) {
			requestIDs = append(requestIDs, metrics.RequestID)
		})
		defer unsubscribe()
		wrapped.EmitMetricsCollected(&telemetry.STTMetrics{RequestID: "req-1"})
		return map[string]any{
			"contract": "stt-stream-adapter",
			"events": []map[string]any{
				{
					"name":        "forward_metrics",
					"request_ids": requestIDs,
					"count":       len(requestIDs),
				},
			},
		}, nil
	case "provider_error_not_forwarded":
		wrapped := &fakeScenarioSTT{label: "wrapped", capabilities: lkstt.STTCapabilities{OfflineRecognize: true}}
		adapter := lkstt.NewStreamAdapter(wrapped, nil)
		labels := make([]string, 0, 2)
		unsubscribe := adapter.OnError(func(err *lkstt.STTError) {
			labels = append(labels, err.Label)
		})
		defer unsubscribe()
		wrapped.EmitError(lkstt.NewSTTError("wrapped", errors.New("wrapped stt failed"), true))
		adapter.EmitError(lkstt.NewSTTError("adapter", errors.New("adapter failed"), true))
		return map[string]any{
			"contract": "stt-stream-adapter-provider-error-not-forwarded",
			"events": []map[string]any{
				{"name": "provider_error_not_forwarded", "labels": labels},
			},
		}, nil
	case "metadata":
		adapter := lkstt.NewStreamAdapter(fakeScenarioSTT{
			label:        "wrapped",
			model:        "wrapped-model",
			provider:     "wrapped-provider",
			capabilities: lkstt.STTCapabilities{OfflineRecognize: true},
		}, nil)
		return map[string]any{
			"contract": "stt-stream-adapter",
			"events": []map[string]any{
				{
					"name":     "metadata",
					"model":    lkstt.Model(adapter),
					"provider": lkstt.Provider(adapter),
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported STT stream adapter action %q", payload.Action)
	}
}

type fakeScenarioSTT struct {
	lkstt.MetricsEmitter
	lkstt.ErrorEmitter
	label        string
	model        string
	provider     string
	capabilities lkstt.STTCapabilities
	prewarmCalls int
}

func sttFallbackErrorClass(ok bool) string {
	if ok {
		return "ValueError"
	}
	return ""
}

func (s fakeScenarioSTT) Label() string {
	if s.label != "" {
		return s.label
	}
	return "scenario.STT"
}

func (s fakeScenarioSTT) Capabilities() lkstt.STTCapabilities {
	return s.capabilities
}

func (s fakeScenarioSTT) Model() string {
	return s.model
}

func (s fakeScenarioSTT) Provider() string {
	return s.provider
}

func (s *fakeScenarioSTT) Prewarm() {
	s.prewarmCalls++
}

func (s fakeScenarioSTT) Stream(context.Context, string) (lkstt.RecognizeStream, error) {
	return nil, errors.New("stream unsupported")
}

func (s fakeScenarioSTT) Recognize(context.Context, []*audiomodel.AudioFrame, string) (*lkstt.SpeechEvent, error) {
	return &lkstt.SpeechEvent{Type: lkstt.SpeechEventFinalTranscript}, nil
}
