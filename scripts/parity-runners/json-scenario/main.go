package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	caseTypeCrossRuntime = "cross-runtime"
	compareModeJSONEqual = "json_equal"
)

type scenario struct {
	Name                   string          `json:"name"`
	CaseType               string          `json:"case_type"`
	Input                  json.RawMessage `json:"input"`
	PythonEntrypoint       string          `json:"python_entrypoint"`
	GoHandler              string          `json:"go_handler"`
	CompareMode            string          `json:"compare_mode"`
	IgnoredFields          []string        `json:"ignored_fields"`
	ExpectedErrorSubstring string          `json:"expected_error_substring"`
}

type handler func(json.RawMessage) (any, error)

type handlerRegistry map[string]handler

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: json-scenario SCENARIO_JSON")
		os.Exit(2)
	}

	output, err := runScenario(os.Args[1], registeredHandlers())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Print(string(output))
}

func loadScenario(path string) (scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return scenario{}, err
	}

	var loaded scenario
	if err := json.Unmarshal(data, &loaded); err != nil {
		return scenario{}, err
	}
	if err := loaded.validate(); err != nil {
		return loaded, err
	}
	return loaded, nil
}

func runScenario(path string, registry handlerRegistry) ([]byte, error) {
	scenario, err := loadScenario(path)
	if err != nil {
		return nil, err
	}

	handler := registry[scenario.GoHandler]
	if handler == nil {
		return nil, fmt.Errorf("[%s] missing Go handler %q", scenario.Name, scenario.GoHandler)
	}

	result, err := handler(scenario.Input)
	if err != nil {
		if scenario.ExpectedErrorSubstring == "" {
			return nil, err
		}
		if !strings.Contains(err.Error(), scenario.ExpectedErrorSubstring) {
			return nil, fmt.Errorf("[%s] Go error %q does not contain expected substring %q", scenario.Name, err.Error(), scenario.ExpectedErrorSubstring)
		}
		result = map[string]string{"error": err.Error()}
	} else if scenario.ExpectedErrorSubstring != "" {
		return nil, fmt.Errorf("[%s] expected Go error containing %q", scenario.Name, scenario.ExpectedErrorSubstring)
	}

	output, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	output = append(output, '\n')
	return output, nil
}

func (s scenario) validate() error {
	switch {
	case s.Name == "":
		return fmt.Errorf("scenario name is required")
	case s.CaseType != caseTypeCrossRuntime:
		return fmt.Errorf("[%s] case_type = %q, want %q", s.Name, s.CaseType, caseTypeCrossRuntime)
	case len(s.Input) == 0:
		return fmt.Errorf("[%s] input is required", s.Name)
	case s.PythonEntrypoint == "":
		return fmt.Errorf("[%s] python_entrypoint is required", s.Name)
	case s.GoHandler == "":
		return fmt.Errorf("[%s] go_handler is required", s.Name)
	case s.CompareMode != compareModeJSONEqual:
		return fmt.Errorf("[%s] compare_mode = %q, want %q", s.Name, s.CompareMode, compareModeJSONEqual)
	default:
		return nil
	}
}
