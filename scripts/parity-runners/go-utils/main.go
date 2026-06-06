package main

import (
	"encoding/json"
	"fmt"
	"os"

	lkmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/utils"
)

type inputEnvelope struct {
	Alpha        *float64  `json:"alpha"`
	Contract     string    `json:"contract"`
	EnvValues    []*string `json:"env_values"`
	Exp          *float64  `json:"exp"`
	Initial      *float64  `json:"initial"`
	MinVal       *float64  `json:"min_val"`
	NameValues   []string  `json:"name_values"`
	Sample       *float64  `json:"sample"`
	SampleValues []float64 `json:"sample_values"`
	URLValues    []string  `json:"url_values"`
	WindowSize   *int      `json:"window_size"`
}

type event struct {
	Avg    string  `json:"avg,omitempty"`
	Name   string  `json:"name"`
	Env    *string `json:"env,omitempty"`
	URL    string  `json:"url,omitempty"`
	Input  string  `json:"input,omitempty"`
	Result any     `json:"result,omitempty"`
	Sample string  `json:"sample,omitempty"`
	Size   *int    `json:"size,omitempty"`
}

type outputEnvelope struct {
	Contract string  `json:"contract"`
	Events   []event `json:"events"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: parity-utils INPUT_JSON")
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}

	var input inputEnvelope
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	if input.Contract == "" {
		input.Contract = "dev-mode-env-exact"
	}

	switch input.Contract {
	case "dev-mode-env-exact":
		return runDevModeEnvExact(input)
	case "hosted-env-presence":
		return runHostedEnvPresence(input)
	case "cloud-url-host-suffix":
		return runCloudURLHostSuffix(input)
	case "camel-to-snake-case":
		return runCamelToSnakeCase(input)
	case "exp-filter-initial-minimum":
		return runExpFilterInitialMinimum(input)
	case "moving-average-window":
		return runMovingAverageWindow(input)
	default:
		return fmt.Errorf("unsupported contract: %s", input.Contract)
	}
}

func runDevModeEnvExact(input inputEnvelope) error {
	if input.EnvValues == nil {
		input.EnvValues = []*string{ptr("1"), ptr(""), ptr("true"), ptr("on")}
	}
	original, originalPresent := os.LookupEnv("LIVEKIT_DEV_MODE")
	defer func() {
		if originalPresent {
			_ = os.Setenv("LIVEKIT_DEV_MODE", original)
		} else {
			_ = os.Unsetenv("LIVEKIT_DEV_MODE")
		}
	}()

	output := outputEnvelope{Contract: "dev-mode-env-exact"}
	for _, value := range input.EnvValues {
		if value == nil {
			return fmt.Errorf("dev-mode env_values must not contain null")
		}
		if err := os.Setenv("LIVEKIT_DEV_MODE", *value); err != nil {
			return err
		}
		output.Events = append(output.Events, event{
			Name:   "is_dev_mode",
			Env:    value,
			Result: utils.IsDevMode(),
		})
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output)
}

func runHostedEnvPresence(input inputEnvelope) error {
	if input.EnvValues == nil {
		input.EnvValues = []*string{nil, ptr(""), ptr("https://hosted.example")}
	}
	original, originalPresent := os.LookupEnv("LIVEKIT_REMOTE_EOT_URL")
	defer func() {
		if originalPresent {
			_ = os.Setenv("LIVEKIT_REMOTE_EOT_URL", original)
		} else {
			_ = os.Unsetenv("LIVEKIT_REMOTE_EOT_URL")
		}
	}()

	output := outputEnvelope{Contract: "hosted-env-presence"}
	for _, value := range input.EnvValues {
		if value == nil {
			if err := os.Unsetenv("LIVEKIT_REMOTE_EOT_URL"); err != nil {
				return err
			}
		} else if err := os.Setenv("LIVEKIT_REMOTE_EOT_URL", *value); err != nil {
			return err
		}
		output.Events = append(output.Events, event{
			Name:   "is_hosted",
			Env:    value,
			Result: utils.IsHosted(),
		})
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output)
}

func runCloudURLHostSuffix(input inputEnvelope) error {
	if input.URLValues == nil {
		input.URLValues = []string{
			"wss://tenant.livekit.cloud",
			"https://tenant.livekit.run/path",
			"http://localhost:7880",
			"://bad-url",
			"https://livekit.cloud.evil.example",
		}
	}

	output := outputEnvelope{Contract: "cloud-url-host-suffix"}
	for _, value := range input.URLValues {
		output.Events = append(output.Events, event{
			Name:   "is_cloud",
			URL:    value,
			Result: utils.IsCloud(value),
		})
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output)
}

func runCamelToSnakeCase(input inputEnvelope) error {
	if input.NameValues == nil {
		input.NameValues = []string{
			"HTTPServerID",
			"roomID",
			"JobContext",
			"already_ok",
			"URL",
		}
	}

	output := outputEnvelope{Contract: "camel-to-snake-case"}
	for _, value := range input.NameValues {
		output.Events = append(output.Events, event{
			Name:   "camel_to_snake_case",
			Input:  value,
			Result: utils.CamelToSnakeCase(value),
		})
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output)
}

func runMovingAverageWindow(input inputEnvelope) error {
	windowSize := intValue(input.WindowSize, 3)
	samples := input.SampleValues
	if samples == nil {
		samples = []float64{1, 2, 3, 4}
	}

	average := lkmath.NewMovingAverage(windowSize)
	output := outputEnvelope{Contract: "moving-average-window"}
	output.Events = append(output.Events, event{
		Name: "initial",
		Avg:  fmt.Sprintf("%g", average.GetAvg()),
		Size: intPtr(average.Size()),
	})
	for _, sample := range samples {
		average.AddSample(sample)
		output.Events = append(output.Events, event{
			Name:   "add_sample",
			Sample: fmt.Sprintf("%g", sample),
			Avg:    fmt.Sprintf("%g", average.GetAvg()),
			Size:   intPtr(average.Size()),
		})
	}
	average.Reset()
	output.Events = append(output.Events, event{
		Name: "reset",
		Avg:  fmt.Sprintf("%g", average.GetAvg()),
		Size: intPtr(average.Size()),
	})

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output)
}

func runExpFilterInitialMinimum(input inputEnvelope) error {
	alpha := floatValue(input.Alpha, 0.5)
	initial := floatValue(input.Initial, 10)
	minimum := floatValue(input.MinVal, 6)
	exp := floatValue(input.Exp, 1)
	sample := floatValue(input.Sample, 2)

	filter, err := lkmath.NewExpFilterWithOptions(alpha, lkmath.ExpFilterOptions{
		Initial: &initial,
		MinVal:  &minimum,
	})
	if err != nil {
		return err
	}
	applied := filter.Apply(exp, sample)
	value, ok := filter.Value()
	if !ok {
		return fmt.Errorf("filter value is unset after apply")
	}

	output := outputEnvelope{Contract: "exp-filter-initial-minimum"}
	output.Events = append(output.Events, event{
		Name:   "apply",
		Input:  fmt.Sprintf("alpha=%g,initial=%g,min=%g,exp=%g,sample=%g", alpha, initial, minimum, exp, sample),
		Result: fmt.Sprintf("%g", applied),
	})
	output.Events = append(output.Events, event{
		Name:   "value",
		Result: fmt.Sprintf("%g", value),
	})

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output)
}

func floatValue(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

func intValue(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func ptr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}
