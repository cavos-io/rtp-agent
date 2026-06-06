package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/library/utils"
)

type inputEnvelope struct {
	Contract  string   `json:"contract"`
	EnvValues []string `json:"env_values"`
}

type event struct {
	Name   string `json:"name"`
	Env    string `json:"env"`
	Result bool   `json:"result"`
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
	if input.Contract != "dev-mode-env-exact" {
		return fmt.Errorf("unsupported contract: %s", input.Contract)
	}
	if input.EnvValues == nil {
		input.EnvValues = []string{"1", "", "true", "on"}
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
		if err := os.Setenv("LIVEKIT_DEV_MODE", value); err != nil {
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
