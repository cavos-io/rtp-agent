package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/library/utils"
)

func registeredHandlers() handlerRegistry {
	return handlerRegistry{
		"dev_mode_env_exact": runDevModeEnvExact,
	}
}

func runDevModeEnvExact(input json.RawMessage) (any, error) {
	var payload struct {
		EnvValues []string `json:"env_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.EnvValues == nil {
		payload.EnvValues = []string{"1", "", "true", "on"}
	}

	original, originalPresent := os.LookupEnv("LIVEKIT_DEV_MODE")
	defer func() {
		if originalPresent {
			_ = os.Setenv("LIVEKIT_DEV_MODE", original)
		} else {
			_ = os.Unsetenv("LIVEKIT_DEV_MODE")
		}
	}()

	events := make([]map[string]any, 0, len(payload.EnvValues))
	for _, value := range payload.EnvValues {
		if err := os.Setenv("LIVEKIT_DEV_MODE", value); err != nil {
			return nil, fmt.Errorf("set LIVEKIT_DEV_MODE: %w", err)
		}
		events = append(events, map[string]any{
			"name":   "is_dev_mode",
			"env":    value,
			"result": utils.IsDevMode(),
		})
	}
	return map[string]any{
		"contract": "dev-mode-env-exact",
		"events":   events,
	}, nil
}
