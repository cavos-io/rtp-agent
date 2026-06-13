package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	lktts "github.com/cavos-io/rtp-agent/core/tts"
)

func runTTSValueObjects(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
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
