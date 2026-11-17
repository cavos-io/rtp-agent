//go:build nativeroomintegration

package main

import (
	"context"
	"encoding/json"
	"github.com/cavos-io/rtp-agent/core/agent"
	tools "github.com/cavos-io/rtp-agent/core/beta/tools"
	pb "github.com/livekit/protocol/livekit"
	lk "github.com/livekit/server-sdk-go/v2"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type dtmfObservation struct {
	Digit, Sender string
	Code          uint32
}
type nativeDTMFCheck struct {
	t       *testing.T
	enabled bool
	events  chan dtmfObservation
}

func newNativeDTMFCheck(t *testing.T, cb *lk.RoomCallback) *nativeDTMFCheck {
	c := &nativeDTMFCheck{t: t, enabled: os.Getenv("NATIVE_TEST_DTMF") == "1", events: make(chan dtmfObservation, 32)}
	if c.enabled {
		cb.OnDataPacket = func(packet lk.DataPacket, p lk.DataReceiveParams) {
			if d, ok := packet.(*pb.SipDTMF); ok {
				select {
				case c.events <- dtmfObservation{d.Digit, p.SenderIdentity, d.Code}:
				default:
				}
			}
		}
	}
	return c
}
func (c *nativeDTMFCheck) verify(ctx context.Context, s *agent.AgentSession, room any, caller *lk.Room, dir string) {
	if !c.enabled {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	incoming, off := s.SubscribeSipDTMFEvents()
	defer off()
	var seen []dtmfObservation
	for _, d := range []dtmfObservation{{"1", "caller-test", 1}, {"1", "caller-test", 1}, {"#", "caller-test", 11}} {
		if e := caller.LocalParticipant.PublishDataPacket(&pb.SipDTMF{Code: d.Code, Digit: d.Digit}, lk.WithDataPublishReliable(true)); e != nil {
			c.t.Fatal(e)
		}
		select {
		case v := <-incoming:
			if v.Digit != d.Digit || v.Code != d.Code || v.SenderIdentity != d.Sender {
				c.t.Fatal("incoming mismatch", v)
			}
			seen = append(seen, d)
		case <-ctx.Done():
			c.t.Fatal("incoming DTMF timeout")
		}
	}
	p, ok := room.(tools.DtmfPublisher)
	if !ok {
		c.t.Fatal("missing DTMF tool contract")
	}
	output, e := tools.NewSendDTMFTool(p).Execute(ctx, `{"events":["2","2","*"]}`)
	if e != nil || output != "Successfully sent DTMF events: 2, 2, *" {
		c.t.Fatal(output, e)
	}
	for _, d := range []dtmfObservation{{"2", "primary-agent", 2}, {"2", "primary-agent", 2}, {"*", "primary-agent", 10}} {
		select {
		case v := <-c.events:
			if v != d {
				c.t.Fatal("outgoing mismatch", v)
			}
			seen = append(seen, v)
		case <-ctx.Done():
			c.t.Fatal("outgoing DTMF timeout")
		}
	}
	b, _ := json.MarshalIndent(seen, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "dtmf.json"), b, 0600); e != nil {
		c.t.Fatal(e)
	}
}
func (c *nativeDTMFCheck) endCall(ctx context.Context, room any) {
	shutter, ok := room.(tools.Shutter)
	if !ok {
		c.t.Fatal("missing call control")
	}
	if _, e := tools.NewEndCallTool(shutter, tools.EndCallToolOptions{}).Execute(ctx, `{}`); e != nil {
		c.t.Fatal(e)
	}
}
