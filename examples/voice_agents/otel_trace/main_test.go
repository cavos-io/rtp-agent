package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestSetupOTelExportsToConfiguredCollector(t *testing.T) {
	received := make(chan *collecttracev1.ExportTraceServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" || r.Header.Get("x-test") != "trace" {
			t.Errorf("request %s headers %v", r.URL.Path, r.Header)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		request := new(collecttracev1.ExportTraceServiceRequest)
		if err := proto.Unmarshal(data, request); err != nil {
			t.Error(err)
		}
		received <- request
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-test=trace")
	p, err := setupOTel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Shutdown(context.Background())
	_, span := p.Tracer("test").Start(context.Background(), "example")
	span.End()
	if err := p.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := <-received
	if len(request.ResourceSpans) != 1 || request.ResourceSpans[0].ScopeSpans[0].Spans[0].Name != "example" {
		t.Fatalf("trace=%v", request)
	}
}

func TestWeatherAndBidirectionalHandoffs(t *testing.T) {
	weather := &weatherTool{}
	if output, err := weather.Execute(context.Background(), `{"location":"Jakarta"}`); err != nil || output == "" {
		t.Fatalf("weather=%q, %v", output, err)
	}
	if _, err := weather.Execute(context.Background(), `{"location":" "}`); err == nil {
		t.Fatal("missing location accepted")
	}
	kelly, alloy := agent.NewAgent("Kelly"), agent.NewAgent("Alloy")
	session := agent.NewAgentSession(kelly, nil, agent.AgentSessionOptions{})
	for _, target := range []*agent.Agent{alloy, kelly} {
		tool := &handoffTool{session: session, target: func() (agent.AgentInterface, error) { return target, nil }}
		if _, err := tool.Execute(context.Background(), "{}"); err != nil {
			var stop llm.StopResponse
			if !errors.As(err, &stop) {
				t.Fatal(err)
			}
		} else {
			t.Fatal("handoff did not stop the outgoing reply")
		}
		if session.Agent != target {
			t.Fatal("handoff did not update the session")
		}
	}
}
