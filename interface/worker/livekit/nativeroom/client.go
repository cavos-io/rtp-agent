package nativeroom

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"github.com/cavos-io/rtp-agent/core/agent"
	model "github.com/cavos-io/rtp-agent/core/audio/model"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

var ErrSourceEnded = errors.New("native room source ended")

type client struct {
	callControl   nativeCallControl
	publishDTMF   func(context.Context, uint32, string) error
	startCaptions func(*agent.AgentSession, string) (func(context.Context) error, error)
	cleanup       Cleanup
	send          func(context.Context, string, []byte) error
	killed        atomic.Bool
	ctx           context.Context
	cancel        context.CancelCauseFunc
	done          chan struct{}
	ready         chan struct{}
	ack           chan message
	frames        chan *model.AudioFrame
	stdin         io.WriteCloser
	commandMu     sync.Mutex
	nextID        uint64
	err           error // published by closing done
}

type Config struct {
	// ParticipantDisconnected runs off the FFI owner, before source-ended job shutdown.
	// The callback must return promptly and must not synchronously close this room.
	ParticipantDisconnected           func(string)
	captionOptions                    wl.RoomOptions
	recordAux                         func(string, *model.AudioFrame)
	Binary, Library                   string
	URL, Token, Participant, TrackSID string
}

// command dispatches playback operations to the selected transport.
func (c *client) command(ctx context.Context, event string, data []byte) error {
	if c.send != nil {
		return c.send(ctx, event, data)
	}
	return c.helperCommand(ctx, event, data)
}

func (c *client) close() error { c.cancel(context.Canceled); <-c.done; return c.err }

// nativeCallControl keeps the helper build independent of the native runtime.
type nativeCallControl interface {
	PublishData(context.Context, string, []byte) error
	ParticipantIdentities() []string
	BeginHold(context.Context) error
	EndHold(context.Context) error
	CaptureHold(context.Context, []int16) error
	SetOutputEnabled(context.Context, bool) error
}
