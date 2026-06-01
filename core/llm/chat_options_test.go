package llm

import (
	"testing"
	"time"
)

func TestDefaultAPIConnectOptionsMatchReferenceDefaults(t *testing.T) {
	options := DefaultAPIConnectOptions()

	if options.MaxRetry != 3 {
		t.Fatalf("MaxRetry = %d, want 3", options.MaxRetry)
	}
	if options.RetryInterval != 2*time.Second {
		t.Fatalf("RetryInterval = %v, want 2s", options.RetryInterval)
	}
	if options.Timeout != 10*time.Second {
		t.Fatalf("Timeout = %v, want 10s", options.Timeout)
	}
	if err := options.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestAPIConnectOptionsRejectNegativeValues(t *testing.T) {
	tests := []struct {
		name    string
		options APIConnectOptions
	}{
		{name: "max retry", options: APIConnectOptions{MaxRetry: -1}},
		{name: "retry interval", options: APIConnectOptions{RetryInterval: -time.Nanosecond}},
		{name: "timeout", options: APIConnectOptions{Timeout: -time.Nanosecond}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.options.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want negative value rejection")
			}
		})
	}
}

func TestAPIConnectOptionsIntervalForRetry(t *testing.T) {
	options := APIConnectOptions{RetryInterval: 3 * time.Second}

	if got := options.IntervalForRetry(0); got != 100*time.Millisecond {
		t.Fatalf("IntervalForRetry(0) = %v, want 100ms", got)
	}
	if got := options.IntervalForRetry(1); got != 3*time.Second {
		t.Fatalf("IntervalForRetry(1) = %v, want configured retry interval", got)
	}
}

func TestWithConnectOptionsStoresOptions(t *testing.T) {
	connectOptions := APIConnectOptions{
		MaxRetry:      1,
		RetryInterval: 50 * time.Millisecond,
		Timeout:       time.Second,
	}
	options := &ChatOptions{}

	WithConnectOptions(connectOptions)(options)

	if options.ConnectOptions == nil {
		t.Fatal("ConnectOptions = nil, want configured options")
	}
	if *options.ConnectOptions != connectOptions {
		t.Fatalf("ConnectOptions = %#v, want %#v", *options.ConnectOptions, connectOptions)
	}
}

func TestChatOptionsEffectiveConnectOptionsDefaultsWhenUnset(t *testing.T) {
	options := &ChatOptions{}

	got, err := options.EffectiveConnectOptions()
	if err != nil {
		t.Fatalf("EffectiveConnectOptions() error = %v, want nil", err)
	}
	want := DefaultAPIConnectOptions()
	if got != want {
		t.Fatalf("EffectiveConnectOptions() = %#v, want default %#v", got, want)
	}
}

func TestChatOptionsEffectiveConnectOptionsValidatesConfiguredOptions(t *testing.T) {
	options := &ChatOptions{
		ConnectOptions: &APIConnectOptions{Timeout: -time.Nanosecond},
	}

	if _, err := options.EffectiveConnectOptions(); err == nil {
		t.Fatal("EffectiveConnectOptions() error = nil, want invalid connect options error")
	}
}

func TestWithExtraParamsStoresClone(t *testing.T) {
	params := map[string]any{
		"temperature": 0.7,
	}
	options := &ChatOptions{}

	WithExtraParams(params)(options)
	params["temperature"] = 1.0

	if options.ExtraParams["temperature"] != 0.7 {
		t.Fatalf("ExtraParams[temperature] = %v, want 0.7", options.ExtraParams["temperature"])
	}
}

func TestWithResponseFormatStoresClone(t *testing.T) {
	format := map[string]any{
		"type": "json_object",
	}
	options := &ChatOptions{}

	WithResponseFormat(format)(options)
	format["type"] = "text"

	if options.ResponseFormat["type"] != "json_object" {
		t.Fatalf("ResponseFormat[type] = %v, want json_object", options.ResponseFormat["type"])
	}
}
