//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	"github.com/cavos-io/rtp-agent/core/agent"
	tools "github.com/cavos-io/rtp-agent/core/beta/tools"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"github.com/livekit/protocol/livekit"
	native "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeinprocess"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeDTMFToolPreservesRepeatsAndErrors(t *testing.T) {
	var digits []string
	r := &Room{child: &client{publishDTMF: func(ctx context.Context, code uint32, digit string) error {
		digits = append(digits, digit)
		if code == 11 {
			return errors.New("denied")
		}
		return nil
	}}}
	tool := tools.NewSendDTMFTool(r)
	result, e := tool.Execute(context.Background(), `{"events":["1","1"]}`)
	if e != nil || result == "" || len(digits) != 2 || digits[0] != "1" || digits[1] != "1" {
		t.Fatal(result, e, digits)
	}
	if e = r.PublishDTMF(11, "#"); e == nil {
		t.Fatal("failure hidden")
	}
	r.closed = true
	if e = r.PublishDTMF(1, "1"); !errors.Is(e, native.ErrClosed) {
		t.Fatal(e)
	}
	if len(digits) != 3 {
		t.Fatal("send after close")
	}
}
func TestNativeCallToolsAreExplicitAndSessionBound(t *testing.T) {
	a, b := &Room{}, &Room{}
	at, bt := a.CallTools(), b.CallTools()
	if len(at) != 2 || at[0].ID() != "send_dtmf_events" || at[1].ID() != "end_call" || at[0] == bt[0] {
		t.Fatal("tool registration")
	}
	if e := a.PublishDTMF(1, "1"); e == nil {
		t.Fatal("unavailable transport accepted")
	}
	if e := a.DeleteRoom(context.Background()); e == nil {
		t.Fatal("missing job accepted")
	}
}

func TestNativeDTMFBridgeOrderAndCancel(t *testing.T) {
	s := agent.NewAgentSession(agent.NewAgent("dtmf"), nil, agent.AgentSessionOptions{})
	received, off := s.SubscribeSipDTMFEvents()
	defer off()
	events := make(chan native.DTMF, 32)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	closeBridge := startDTMF(ctx, events, s, cancel)
	for _, digit := range []string{"1", "1", "2"} {
		events <- native.DTMF{Code: uint32(digit[0] - '0'), Digit: digit, Sender: "caller"}
		select {
		case got := <-received:
			if got.Digit != digit || got.SenderIdentity != "caller" {
				t.Fatal(got)
			}
		case <-time.After(time.Second):
			t.Fatal("delivery blocked")
		}
	}
	// Fill the core subscription, then leave another delivery blocked.
	for i := 0; i < 20; i++ {
		events <- native.DTMF{Code: 1, Digit: "1"}
	}
	stop, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if e := closeBridge(stop); e != nil {
		t.Fatal(e)
	}
	if context.Cause(ctx) != nil {
		t.Fatal("normal cancellation failed session", context.Cause(ctx))
	}
}

func TestNativeDTMFStalledConsumerFailsSession(t *testing.T) {
	s := agent.NewAgentSession(agent.NewAgent("dtmf"), nil, agent.AgentSessionOptions{})
	s.SipDTMFEvents()
	events := make(chan native.DTMF, 32)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	closeBridge := startDTMF(ctx, events, s, cancel)
	defer closeBridge(context.Background())
	for i := 0; i < 11; i++ {
		events <- native.DTMF{Code: 1, Digit: "1"}
	}
	select {
	case <-ctx.Done():
		if context.Cause(ctx) == context.Canceled {
			t.Fatal("failure hidden")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("stalled consumer not detected")
	}
}

func TestNativeCallDeleteFailureVisible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"permission_denied","msg":"denied"}`))
	}))
	defer server.Close()
	job := worker.NewJobContext(&livekit.Job{Room: &livekit.Room{Name: "call"}}, server.URL, "test-key", "test-secret")
	r := &Room{Job: job}
	if e := r.DeleteRoom(context.Background()); e == nil {
		t.Fatal("room deletion failure hidden")
	}
	if !r.closed {
		t.Fatal("intentional shutdown not marked")
	}
}
