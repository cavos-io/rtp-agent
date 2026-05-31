package telemetry

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestHttpServerStartBindsDynamicMetricsPort(t *testing.T) {
	server := NewHttpServer("127.0.0.1", 0)
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Stop(context.Background())

	if server.Port == 0 {
		t.Fatal("Port = 0 after Start(), want assigned listener port")
	}

	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(server.Port) + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200", resp.StatusCode)
	}
}
