package slng

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const slngParityScenarioEnv = "SLNG_PARITY_SCENARIO_PATH"

type slngParitySuite struct {
	Name        string           `json:"name"`
	CaseType    string           `json:"case_type"`
	CompareMode string           `json:"compare_mode"`
	Input       []slngParityCase `json:"input"`
}

type slngParityCase struct {
	Name      string          `json:"name"`
	Operation string          `json:"operation"`
	Input     json.RawMessage `json:"input"`
	Expected  json.RawMessage `json:"expected"`
}

type slngParityError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type slngParityCaseOutput struct {
	CaseType string           `json:"case_type"`
	Name     string           `json:"name"`
	Result   any              `json:"result"`
	Error    *slngParityError `json:"error"`
}

type slngParityExpected struct {
	Result any              `json:"result"`
	Error  *slngParityError `json:"error"`
}

type slngParityOutput struct {
	CaseType string `json:"case_type"`
	Name     string `json:"name"`
	Result   struct {
		Scenarios []slngParityCaseOutput `json:"scenarios"`
	} `json:"result"`
	Error any `json:"error"`
}

func TestSLNGParityScenario(t *testing.T) {
	path := os.Getenv(slngParityScenarioEnv)
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite slngParitySuite
	if err := json.Unmarshal(data, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Name == "" || suite.CaseType != "cross-runtime" || suite.CompareMode != "json_equal" {
		t.Fatalf("invalid scenario suite metadata: %#v", suite)
	}

	output := slngParityOutput{CaseType: "cross-runtime", Name: suite.Name}
	for _, scenario := range suite.Input {
		actual := runSLNGParityCase(t, scenario)
		output.Result.Scenarios = append(output.Result.Scenarios, actual)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SLNG_PARITY_OUTPUT=%s\n", encoded)
}

func runSLNGParityCase(t *testing.T, scenario slngParityCase) slngParityCaseOutput {
	t.Helper()
	result, err := runSLNGParityOperation(scenario.Operation, scenario.Input)
	actual := slngParityExpected{Result: result}
	if err != nil {
		actual.Result = nil
		actual.Error = &slngParityError{Type: "ValueError", Message: err.Error()}
	}
	var expected slngParityExpected
	if err := json.Unmarshal(scenario.Expected, &expected); err != nil {
		t.Fatalf("[%s] decode expected: %v", scenario.Name, err)
	}
	actualJSON, _ := json.Marshal(actual)
	expectedJSON, _ := json.Marshal(expected)
	if string(actualJSON) != string(expectedJSON) {
		t.Errorf("[%s] actual %s != expected %s", scenario.Name, actualJSON, expectedJSON)
	}
	return slngParityCaseOutput{
		CaseType: "cross-runtime",
		Name:     scenario.Name,
		Result:   actual.Result,
		Error:    actual.Error,
	}
}

func runSLNGParityOperation(operation string, input json.RawMessage) (any, error) {
	switch operation {
	case "bridge_endpoint":
		var args struct {
			BaseURL string `json:"base_url"`
			Service string `json:"service"`
			Model   string `json:"model"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return bridgeEndpoint(args.BaseURL, args.Service, args.Model)
	case "validate_model_identifier":
		var args struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		if _, err := parseModelRef(args.Model); err != nil {
			return nil, err
		}
		return args.Model, nil
	case "bridge_model":
		var args struct {
			Endpoint string `json:"endpoint"`
			Service  string `json:"service"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return bridgeModel(args.Endpoint, args.Service)
	case "normalize_region_override":
		var args struct {
			RegionOverride any `json:"region_override"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return normalizeRegionOverride(args.RegionOverride), nil
	case "normalize_world_part_override":
		var args struct {
			WorldPartOverride string `json:"world_part_override"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		headers, err := (gatewayHeaders{WorldPartOverride: args.WorldPartOverride}).build(nil)
		return headers.Get("X-World-Part-Override"), err
	case "external_tracking_headers":
		var args struct {
			ExternalAgentID   *string `json:"external_agent_id"`
			ExternalSessionID *string `json:"external_session_id"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		var agentID, sessionID string
		if args.ExternalAgentID != nil {
			agentID = *args.ExternalAgentID
		}
		if args.ExternalSessionID != nil {
			sessionID = *args.ExternalSessionID
		}
		headers, err := (gatewayHeaders{
			ExternalAgentID:   agentID,
			ExternalSessionID: sessionID,
		}).build(nil)
		if err != nil {
			return nil, err
		}
		result := map[string]string{}
		for _, key := range []string{"X-SLNG-Agent-Id", "X-SLNG-Session-Id"} {
			if value := headers.Get(key); value != "" {
				result[strings.ToLower(key)] = value
			}
		}
		return result, nil
	case "extract_error_status":
		var args struct {
			Frame map[string]any `json:"frame"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return extractSLNGErrorStatus(args.Frame), nil
	case "build_stt_init_payload":
		var args struct {
			Model                   string         `json:"model"`
			Language                string         `json:"language"`
			SampleRate              int            `json:"sample_rate"`
			Encoding                string         `json:"encoding"`
			VADThreshold            float64        `json:"vad_threshold"`
			VADMinSilenceDurationMS int            `json:"vad_min_silence_duration_ms"`
			VADSpeechPadMS          int            `json:"vad_speech_pad_ms"`
			EnableDiarization       bool           `json:"enable_diarization"`
			EnablePartials          bool           `json:"enable_partial_transcripts"`
			MinSpeakers             int            `json:"min_speakers"`
			MaxSpeakers             int            `json:"max_speakers"`
			ModelOptions            map[string]any `json:"model_options"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		provider := NewSTT("parity",
			WithSTTModel(args.Model),
			WithSTTLanguage(args.Language),
			WithSTTSampleRate(args.SampleRate),
			WithSTTEncoding(args.Encoding),
			WithSTTVADThreshold(args.VADThreshold),
			WithSTTVADMinSilenceDurationMS(args.VADMinSilenceDurationMS),
			WithSTTVADSpeechPadMS(args.VADSpeechPadMS),
			WithSTTPartialTranscripts(args.EnablePartials),
			WithSTTDiarization(args.EnableDiarization, args.MinSpeakers, args.MaxSpeakers),
			WithSTTModelOptions(args.ModelOptions),
		)
		return decodeSLNGParityPayload(buildSTTInitPayload(provider))
	case "build_tts_init_payload":
		var args struct {
			Model        string         `json:"model"`
			Voice        string         `json:"voice"`
			Language     string         `json:"language"`
			SampleRate   int            `json:"sample_rate"`
			Encoding     string         `json:"encoding"`
			Speed        float64        `json:"speed"`
			ModelOptions map[string]any `json:"model_options"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		if args.Encoding != defaultSLNGTTSEncoding {
			return nil, fmt.Errorf("unsupported TTS parity encoding %q", args.Encoding)
		}
		provider := NewTTS("parity",
			WithTTSModel(args.Model),
			WithTTSVoice(args.Voice),
			WithTTSLanguage(args.Language),
			WithTTSSampleRate(args.SampleRate),
			WithTTSSpeed(args.Speed),
			WithTTSModelOptions(args.ModelOptions),
		)
		return decodeSLNGParityPayload(buildTTSInitPayload(provider))
	case "candidate_primary_recovery":
		var args struct {
			Count           int64 `json:"count"`
			CooldownSeconds int64 `json:"cooldown_seconds"`
			FailedAtSeconds int64 `json:"failed_at_seconds"`
			BeforeSeconds   int64 `json:"before_seconds"`
			AfterSeconds    int64 `json:"after_seconds"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		state := newCandidateState(int(args.Count), time.Duration(args.CooldownSeconds)*time.Second)
		next, ok := state.advance(0, time.Unix(args.FailedAtSeconds, 0))
		if !ok {
			return nil, fmt.Errorf("primary failure did not advance")
		}
		return map[string]int{
			"next_after_primary_failure": next,
			"before_cooldown":            state.start(time.Unix(args.BeforeSeconds, 0)),
			"after_cooldown":             state.start(time.Unix(args.AfterSeconds, 0)),
		}, nil
	default:
		return nil, fmt.Errorf("unknown operation %q", operation)
	}
}

func decodeSLNGParityPayload(payload []byte) (any, error) {
	var result any
	err := json.Unmarshal(payload, &result)
	return result, err
}
