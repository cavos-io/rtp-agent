package nativeroom

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	model "github.com/cavos-io/rtp-agent/core/audio/model"
)

const readyMessages = `{"event":"duplex_protocol","version":2}
{"event":"subscribed"}
{"event":"output_published"}
`

func TestReadRequiresBothBarriersAndClassifiesEOF(t *testing.T) {
	for _, tc := range []struct {
		name, wire    string
		ready, normal bool
	}{
		{"subscription only", strings.Split(readyMessages, `{"event":"output_published"}`)[0], false, false},
		{"ready EOF", readyMessages, true, false},
		{"source departure", readyMessages + `{"event":"ended","reason":"participant_disconnected"}` + "\n", true, true},
		{"unsubscribe", readyMessages + `{"event":"ended","reason":"track_unsubscribed"}` + "\n", true, true},
		{"bad reason", readyMessages + `{"event":"ended","reason":"crash"}` + "\n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &client{ctx: context.Background(), ready: make(chan struct{}), frames: make(chan *model.AudioFrame, 8), ack: make(chan message, 1)}
			err := c.read(strings.NewReader(tc.wire))
			if err == nil || errors.Is(err, ErrSourceEnded) != tc.normal {
				t.Fatal(err)
			}
			select {
			case <-c.ready:
				if !tc.ready {
					t.Fatal("early ready")
				}
			default:
				if tc.ready {
					t.Fatal("not ready")
				}
			}
		})
	}
}
func TestReadBurstPreservesFrames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &client{ctx: ctx, ready: make(chan struct{}), frames: make(chan *model.AudioFrame, 8), ack: make(chan message, 1)}
	var wire strings.Builder
	wire.WriteString(readyMessages)
	for i := 0; i < 80; i++ {
		data := make([]byte, 2400)
		data[0] = byte(i)
		json.NewEncoder(&wire).Encode(message{Event: "pcm", Data: data})
	}
	done := make(chan error, 1)
	go func() { done <- c.read(strings.NewReader(wire.String())) }()
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 80; i++ {
		select {
		case f := <-c.frames:
			if f.Data[0] != byte(i) {
				t.Fatal("PCM reordered")
			}
		case <-time.After(time.Second):
			t.Fatal("burst stalled")
		}
	}
	if err := <-done; err == nil {
		t.Fatal("unexpected EOF accepted")
	}
}
func TestClientReapsStalledConsumer(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "helper")
	pcm, _ := json.Marshal(message{Event: "pcm", Data: make([]byte, 2400)})
	// Fake IPC only; this test makes no native receive-engine claim.
	script := "#!/bin/sh\nread config\ncat <<'WIRE'\n" + readyMessages + strings.Repeat(string(pcm)+"\n", 80) + "WIRE\nread command\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c, err := launch(context.Background(), Config{Binary: binary}, func(ctx context.Context, _ *model.AudioFrame) { <-ctx.Done() })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done:
	case <-time.After(6 * time.Second):
		t.Fatal("stalled child not reaped")
	}
	if c.err == nil || !strings.Contains(c.err.Error(), "consumer stalled") {
		t.Fatal(c.err)
	}
}
func TestClientNonzeroExitOverridesNormalDeparture(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "helper")
	script := "#!/bin/sh\nread config\ncat <<'WIRE'\n" + readyMessages + "{\"event\":\"ended\",\"reason\":\"participant_disconnected\"}\nWIRE\nexit 7\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c, err := launch(context.Background(), Config{Binary: binary}, func(context.Context, *model.AudioFrame) {})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("child not reaped")
	}
	if !c.cleanup.Reaped || c.cleanup.ExitCode != 7 || c.cleanup.PID <= 0 {
		t.Fatalf("missing failure diagnostics: %+v", c.cleanup)
	}
	if c.err == nil || errors.Is(c.err, ErrSourceEnded) || !strings.Contains(c.err.Error(), "exit") {
		t.Fatal(c.err)
	}
}

func TestShutdownDiagnostics(t *testing.T) {
	c := &client{ctx: context.Background(), ready: make(chan struct{}), frames: make(chan *model.AudioFrame, 8), ack: make(chan message, 1)}
	wire := readyMessages + `{"event":"shutdown","duration_ns":1000000000,"disconnect_callback":true,"retained_handles_released":true,"runtime_disposed":true,"overflow":false}` + "\n" + `{"event":"ended","reason":"track_unsubscribed"}` + "\n"
	if err := c.read(strings.NewReader(wire)); !errors.Is(err, ErrSourceEnded) {
		t.Fatal(err)
	}
	d := c.cleanup
	if !d.ShutdownSeen || !d.DisconnectCallback || !d.HandlesReleased || !d.RuntimeDisposed || d.CallbackOverflow || d.NativeCleanupMS != 1000 || d.Reaped {
		t.Fatalf("%+v", d)
	}
}

func TestCleanupDiagnosticsReportsForcedKill(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "helper")
	script := "#!/bin/sh\ntrap '' INT\nread config\ncat <<'WIRE'\n" + readyMessages + "WIRE\nwhile :; do read command; done\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := launch(ctx, Config{Binary: binary}, func(context.Context, *model.AudioFrame) {})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.ready:
	case <-time.After(time.Second):
		t.Fatal("helper not ready")
	}
	cancel()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("helper was not reaped")
	}
	d := c.cleanup
	if !d.Reaped || !d.KillAttempted || d.ExitCode == 0 || d.ShutdownSeen || d.CleanupMS < 2900 {
		t.Fatalf("forced kill hidden: %+v", d)
	}
}

func TestClientGracefulStopDrainsCleanup(t *testing.T) {
	for _, exit := range []string{"0", "7"} {
		t.Run("exit-"+exit, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "helper")
			pcm, _ := json.Marshal(message{Event: "pcm", Data: make([]byte, 2400)})
			// Fail if the parent signals rather than sending the existing stop command.
			script := "#!/bin/sh\ntrap 'exit 9' INT\nread config\ncat <<'WIRE'\n" + readyMessages + "WIRE\nread command\ncase \"$command\" in *'\"event\":\"stop\"'*) ;; *) exit 8;; esac\ncat <<'WIRE'\n" + strings.Repeat(string(pcm)+"\n", 20) + "{\"event\":\"shutdown\",\"disconnect_callback\":true,\"retained_handles_released\":true,\"runtime_disposed\":true}\nWIRE\nexit " + exit + "\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			c, err := launch(context.Background(), Config{Binary: binary}, func(context.Context, *model.AudioFrame) {})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-c.ready:
			case <-time.After(time.Second):
				t.Fatal("not ready")
			}
			err = c.close()
			if exit == "0" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("graceful close: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "exit status 7") {
				t.Fatalf("real exit hidden: %v", err)
			}
			if !c.cleanup.Reaped || c.cleanup.KillAttempted || !c.cleanup.ShutdownSeen || !c.cleanup.RuntimeDisposed {
				t.Fatalf("cleanup lost: %+v", c.cleanup)
			}
		})
	}
}

func TestClientStopOrdersAfterInFlightCapture(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "helper")
	pcm, _ := json.Marshal(message{Event: "pcm", Data: make([]byte, 2400)})
	script := "#!/bin/sh\nread config\ncat <<'WIRE'\n" + readyMessages + "WIRE\nread command\ncase \"$command\" in *'\"event\":\"capture\",\"id\":1000'*) ;; *) exit 8;; esac\ncat <<'WIRE'\n" + string(pcm) + "\nWIRE\nread command\ncase \"$command\" in *'\"event\":\"stop\",\"id\":1001'*) ;; *) exit 9;; esac\nprintf '%s\\n' '{\"event\":\"shutdown\",\"runtime_disposed\":true}'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	captured := make(chan struct{}, 1)
	c, err := launch(context.Background(), Config{Binary: binary}, func(context.Context, *model.AudioFrame) { captured <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	select {
	case <-c.ready:
	case <-time.After(time.Second):
		t.Fatal("not ready")
	}
	done := make(chan error, 1)
	go func() { done <- c.command(context.Background(), "capture", make([]byte, 2400)) }()
	select {
	case <-captured:
	case <-time.After(time.Second):
		t.Fatal("capture not received")
	}
	if err := c.close(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight command: %v", err)
	}
	if c.cleanup.ExitCode != 0 || !c.cleanup.RuntimeDisposed || c.cleanup.KillAttempted {
		t.Fatalf("stop order: %+v", c.cleanup)
	}
	if err := c.command(context.Background(), "clear", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-shutdown clear: %v", err)
	}
}
