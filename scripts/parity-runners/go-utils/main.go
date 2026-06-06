package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/library/utils"
)

type inputEnvelope struct {
	Contract   string    `json:"contract"`
	EnvValues  []*string `json:"env_values"`
	NameValues []string  `json:"name_values"`
	URLValues  []string  `json:"url_values"`
}

type event struct {
	Name   string  `json:"name"`
	Env    *string `json:"env,omitempty"`
	URL    string  `json:"url,omitempty"`
	Input  string  `json:"input,omitempty"`
	Result any     `json:"result"`
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

func ptr(value string) *string {
	return &value
}
