package livekit

import (
	"github.com/cavos-io/rtp-agent/core/agent"
	lkprotocol "github.com/livekit/protocol/livekit"
)

type LocalJobOptions struct {
	FakeJob           bool
	RoomInfo          *lkprotocol.Room
	Token             string
	RecordingOptions  agent.RecordingOptions
	SessionReportPath string
	SessionDirectory  string
}
