package livekit

import (
	"context"
	"sync"

	"github.com/cavos-io/rtp-agent/core/agent"
	lk "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// CaptionWriter is a transport-owned, ordered text stream. Implementations must
// bound writes and close, including when the room disconnects.
type CaptionWriter interface {
	Write(string)
	Close()
}

// CaptionTransport supplies publication only; RoomIO retains caption semantics.
// Functions must be safe for concurrent caller and agent events.
type CaptionTransport struct {
	AgentIdentity, AgentTrack, CallerIdentity, CallerTrack string
	PublishPacket                                          func(*lk.Transcription) error
	PublishText                                            func(string, lksdk.StreamTextOptions)
	OpenText                                               func(lksdk.StreamTextOptions) CaptionWriter
	Usable                                                 func() bool
}

// CaptionForwarder reuses default room captions without creating a room, audio
// codec, recorder, or playback controller. Close it before closing the transport.
type CaptionForwarder struct {
	rio  *RoomIO
	once sync.Once
	err  error
}

func NewCaptionForwarder(session *agent.AgentSession, transport CaptionTransport) *CaptionForwarder {
	r := &RoomIO{AgentSession: session, audioTrackID: transport.AgentTrack,
		userTranscriptionTrackID: transport.CallerTrack, userTranscriptionParticipantID: transport.CallerIdentity,
		transcriptionPacketPublisher: transport.PublishPacket, transcriptionTextPublisher: transport.PublishText,
		transcriptionParticipantIdentity: func() string { return transport.AgentIdentity },
		publicationTransportUsable:       transport.Usable,
	}
	if transport.OpenText != nil {
		r.agentTextStreamOpener = func(o lksdk.StreamTextOptions) roomIOTextStreamWriter { return transport.OpenText(o) }
	}
	r.startUserTranscriptionListener()
	r.startAgentTranscriptionListener()
	return &CaptionForwarder{rio: r}
}

func (f *CaptionForwarder) Close(ctx context.Context) error {
	f.once.Do(func() {
		usable := f.rio.publicationTransportUsable == nil || f.rio.publicationTransportUsable()
		if usable {
			f.err = f.rio.shutdownPublications(ctx, true)
		} else {
			f.rio.BeginShutdown()
			f.rio.stopPublicationListeners()
			f.err = f.rio.waitForPublicationListeners(ctx)
			f.rio.abandonAgentTextStream()
		}
	})
	return f.err
}
