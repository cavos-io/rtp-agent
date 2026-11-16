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
	if c.err == nil || errors.Is(c.err, ErrSourceEnded) || !strings.Contains(c.err.Error(), "exit") {
		t.Fatal(c.err)
	}
}
