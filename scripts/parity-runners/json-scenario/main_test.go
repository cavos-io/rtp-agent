package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadScenarioReadsGenericFields(t *testing.T) {
	path := writeScenario(t, `{
		"name": "dev-mode-env-json-scenario",
		"case_type": "cross-runtime",
		"input": {"env_values": ["1", ""]},
		"python_entrypoint": "scripts.parity_scenario_entries:dev_mode_env_exact",
		"go_handler": "dev_mode_env_exact",
		"compare_mode": "json_equal",
		"ignored_fields": ["timestamp", "duration", "trace_id"]
	}`)

	scenario, err := loadScenario(path)
	if err != nil {
		t.Fatalf("loadScenario() error = %v", err)
	}

	if scenario.Name != "dev-mode-env-json-scenario" {
		t.Fatalf("Name = %q", scenario.Name)
	}
	if scenario.PythonEntrypoint != "scripts.parity_scenario_entries:dev_mode_env_exact" {
		t.Fatalf("PythonEntrypoint = %q", scenario.PythonEntrypoint)
	}
	if scenario.GoHandler != "dev_mode_env_exact" {
		t.Fatalf("GoHandler = %q", scenario.GoHandler)
	}
	if !strings.Contains(string(scenario.Input), "env_values") {
		t.Fatalf("Input = %s, want raw input JSON", scenario.Input)
	}
}

func TestRunScenarioDispatchesRegisteredHandlerWithInputOnly(t *testing.T) {
	path := writeScenario(t, `{
		"name": "echo-json-scenario",
		"case_type": "cross-runtime",
		"input": {"text": "hello", "trace_id": "ignored"},
		"python_entrypoint": "scripts.parity_scenario_entries:echo",
		"go_handler": "echo",
		"compare_mode": "json_equal",
		"ignored_fields": ["trace_id"]
	}`)
	registry := handlerRegistry{
		"echo": func(input json.RawMessage) (any, error) {
			var payload struct {
				Text    string `json:"text"`
				TraceID string `json:"trace_id"`
			}
			if err := json.Unmarshal(input, &payload); err != nil {
				return nil, err
			}
			return map[string]any{
				"text":     payload.Text,
				"trace_id": payload.TraceID,
			}, nil
		},
	}

	output, err := runScenario(path, registry)
	if err != nil {
		t.Fatalf("runScenario() error = %v", err)
	}

	const want = `{"text":"hello","trace_id":"ignored"}` + "\n"
	if string(output) != want {
		t.Fatalf("output = %s, want %s", output, want)
	}
}

func TestRunScenarioReportsExpectedErrorsAsJSON(t *testing.T) {
	path := writeScenario(t, `{
		"name": "expected-error-json-scenario",
		"case_type": "cross-runtime",
		"input": {"text": "boom"},
		"python_entrypoint": "scripts.parity_scenario_entries:error",
		"go_handler": "error",
		"compare_mode": "json_equal",
		"expected_error_substring": "boom"
	}`)
	registry := handlerRegistry{
		"error": func(json.RawMessage) (any, error) {
			return nil, errString("boom from handler")
		},
	}

	output, err := runScenario(path, registry)
	if err != nil {
		t.Fatalf("runScenario() error = %v", err)
	}

	const want = `{"error":"boom from handler"}` + "\n"
	if string(output) != want {
		t.Fatalf("output = %s, want %s", output, want)
	}
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func writeScenario(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenario.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
