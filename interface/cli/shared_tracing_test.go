package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cavos-io/rtp-agent/interface/worker"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type exitTraceProcessor struct{ path string }

func (*exitTraceProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (*exitTraceProcessor) OnEnd(sdktrace.ReadOnlySpan)                     {}
func (*exitTraceProcessor) ForceFlush(context.Context) error                { return nil }
func (p *exitTraceProcessor) Shutdown(context.Context) error {
	return os.WriteFile(p.path, []byte("closed"), 0600)
}

func TestCLIClosesSharedTracing(t *testing.T) {
	if path := os.Getenv("RTP_TRACE_EXIT_TEST_PATH"); path != "" {
		server := worker.NewAgentServer(worker.WorkerOptions{})
		if err := server.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(&exitTraceProcessor{path: path}))); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("RTP_TRACE_EXIT_TEST_MODE") == "error" {
			os.Args = []string{"test", "start", "--unknown-argument"}
		} else {
			os.Args = []string{"test", "eval"}
		}
		RunAppWithOptions(server, WithEvalRunner(func(context.Context) (string, error) { return "ok", nil }))
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"normal", "error"} {
		t.Run(mode, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "shutdown")
			cmd := exec.Command(executable, "-test.run=^TestCLIClosesSharedTracing$")
			cmd.Env = append(os.Environ(), "RTP_TRACE_EXIT_TEST_PATH="+marker, "RTP_TRACE_EXIT_TEST_MODE="+mode)
			output, err := cmd.CombinedOutput()
			if (err != nil) != (mode == "error") {
				t.Fatalf("exit = %v: %s", err, output)
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "closed" {
				t.Fatalf("provider not closed: %q %v: %s", data, err, output)
			}
		})
	}
}
