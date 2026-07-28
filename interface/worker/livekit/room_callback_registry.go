package livekit

import (
	"sync"

	"github.com/cavos-io/rtp-agent/library/logger"
	lkprotocol "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
)

// RoomCallbackRegistry fans one room callback out to many registered callbacks.
//
// LiveKit installs a room callback when the room is created: lksdk.NewRoom copies
// the caller's handler values into a callback it owns and never exposes again, so
// a callback handed over after that point is ignored. Callback returns a single
// stable callback whose handlers read the live registration list, so callbacks
// registered after the room exists still receive events.
//
// Handlers registered as nil are skipped. Registration order is preserved, and
// every registered callback is invoked for an event even if an earlier one is
// only partially populated.
type RoomCallbackRegistry struct {
	mu      sync.RWMutex
	entries []roomCallbackEntry
	nextID  uint64
	fanOut  *lksdk.RoomCallback
}

type roomCallbackEntry struct {
	id       uint64
	callback *lksdk.RoomCallback
}

func NewRoomCallbackRegistry() *RoomCallbackRegistry {
	registry := &RoomCallbackRegistry{}
	registry.fanOut = registry.newFanOutCallback()
	return registry
}

// Add registers cb and returns a function that removes it again. A nil callback
// is ignored and the returned function is a no-op. The remove function is safe to
// call more than once.
func (r *RoomCallbackRegistry) Add(cb *lksdk.RoomCallback) func() {
	if r == nil || cb == nil {
		return func() {}
	}
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.entries = append(r.entries, roomCallbackEntry{id: id, callback: cb})
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.remove(id)
		})
	}
}

// Callback returns the stable fan-out callback to hand to lksdk.NewRoom. It must
// be taken once, at room creation; the same pointer keeps dispatching to whatever
// is registered when an event fires.
func (r *RoomCallbackRegistry) Callback() *lksdk.RoomCallback {
	if r == nil {
		return nil
	}
	return r.fanOut
}

// Len reports how many callbacks are currently registered.
func (r *RoomCallbackRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

func (r *RoomCallbackRegistry) remove(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index, entry := range r.entries {
		if entry.id != id {
			continue
		}
		r.entries = append(r.entries[:index:index], r.entries[index+1:]...)
		return
	}
}

func (r *RoomCallbackRegistry) snapshot() []*lksdk.RoomCallback {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.entries) == 0 {
		return nil
	}
	callbacks := make([]*lksdk.RoomCallback, 0, len(r.entries))
	for _, entry := range r.entries {
		callbacks = append(callbacks, entry.callback)
	}
	return callbacks
}

// forEach invokes fn for every registered callback, recovering from a panic in
// any single callback so it cannot stop the others or crash the lksdk goroutine
// the room dispatches on. This mirrors how RoomIO isolates its own event
// listeners; the registry needs it more, because it hands the same event to
// arbitrary downstream callbacks alongside RoomIO's own audio/track handlers.
func (r *RoomCallbackRegistry) forEach(invoke func(*lksdk.RoomCallback)) {
	for _, c := range r.snapshot() {
		func(c *lksdk.RoomCallback) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Logger.Warnw("room callback panicked", nil, "panic", recovered)
				}
			}()
			invoke(c)
		}(c)
	}
}

func (r *RoomCallbackRegistry) newFanOutCallback() *lksdk.RoomCallback {
	cb := lksdk.NewRoomCallback()

	cb.OnDisconnected = func() {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnDisconnected != nil {
				c.OnDisconnected()
			}
		})
	}
	cb.OnDisconnectedWithReason = func(reason lksdk.DisconnectionReason) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnDisconnectedWithReason != nil {
				c.OnDisconnectedWithReason(reason)
			}
		})
	}
	cb.OnParticipantConnected = func(participant *lksdk.RemoteParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnParticipantConnected != nil {
				c.OnParticipantConnected(participant)
			}
		})
	}
	cb.OnParticipantDisconnected = func(participant *lksdk.RemoteParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnParticipantDisconnected != nil {
				c.OnParticipantDisconnected(participant)
			}
		})
	}
	cb.OnActiveSpeakersChanged = func(participants []lksdk.Participant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnActiveSpeakersChanged != nil {
				c.OnActiveSpeakersChanged(participants)
			}
		})
	}
	cb.OnRoomMetadataChanged = func(metadata string) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnRoomMetadataChanged != nil {
				c.OnRoomMetadataChanged(metadata)
			}
		})
	}
	cb.OnRecordingStatusChanged = func(isRecording bool) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnRecordingStatusChanged != nil {
				c.OnRecordingStatusChanged(isRecording)
			}
		})
	}
	cb.OnRoomMoved = func(roomName string, token string) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnRoomMoved != nil {
				c.OnRoomMoved(roomName, token)
			}
		})
	}
	cb.OnReconnecting = func() {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnReconnecting != nil {
				c.OnReconnecting()
			}
		})
	}
	cb.OnReconnected = func() {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnReconnected != nil {
				c.OnReconnected()
			}
		})
	}
	cb.OnLocalTrackSubscribed = func(publication *lksdk.LocalTrackPublication, lp *lksdk.LocalParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnLocalTrackSubscribed != nil {
				c.OnLocalTrackSubscribed(publication, lp)
			}
		})
	}

	cb.OnLocalTrackPublished = func(publication *lksdk.LocalTrackPublication, lp *lksdk.LocalParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnLocalTrackPublished != nil {
				c.OnLocalTrackPublished(publication, lp)
			}
		})
	}
	cb.OnLocalTrackUnpublished = func(publication *lksdk.LocalTrackPublication, lp *lksdk.LocalParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnLocalTrackUnpublished != nil {
				c.OnLocalTrackUnpublished(publication, lp)
			}
		})
	}
	cb.OnTrackMuted = func(publication lksdk.TrackPublication, participant lksdk.Participant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnTrackMuted != nil {
				c.OnTrackMuted(publication, participant)
			}
		})
	}
	cb.OnTrackUnmuted = func(publication lksdk.TrackPublication, participant lksdk.Participant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnTrackUnmuted != nil {
				c.OnTrackUnmuted(publication, participant)
			}
		})
	}
	cb.OnMetadataChanged = func(oldMetadata string, participant lksdk.Participant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnMetadataChanged != nil {
				c.OnMetadataChanged(oldMetadata, participant)
			}
		})
	}
	cb.OnAttributesChanged = func(changed map[string]string, participant lksdk.Participant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnAttributesChanged != nil {
				c.OnAttributesChanged(changed, participant)
			}
		})
	}
	cb.OnIsSpeakingChanged = func(participant lksdk.Participant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnIsSpeakingChanged != nil {
				c.OnIsSpeakingChanged(participant)
			}
		})
	}
	cb.OnConnectionQualityChanged = func(update *lkprotocol.ConnectionQualityInfo, participant lksdk.Participant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnConnectionQualityChanged != nil {
				c.OnConnectionQualityChanged(update, participant)
			}
		})
	}
	cb.OnTrackSubscribed = func(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnTrackSubscribed != nil {
				c.OnTrackSubscribed(track, publication, rp)
			}
		})
	}
	cb.OnTrackUnsubscribed = func(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnTrackUnsubscribed != nil {
				c.OnTrackUnsubscribed(track, publication, rp)
			}
		})
	}
	cb.OnTrackSubscriptionFailed = func(sid string, rp *lksdk.RemoteParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnTrackSubscriptionFailed != nil {
				c.OnTrackSubscriptionFailed(sid, rp)
			}
		})
	}
	cb.OnTrackPublished = func(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnTrackPublished != nil {
				c.OnTrackPublished(publication, rp)
			}
		})
	}
	cb.OnTrackUnpublished = func(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnTrackUnpublished != nil {
				c.OnTrackUnpublished(publication, rp)
			}
		})
	}
	cb.OnDataReceived = func(data []byte, params lksdk.DataReceiveParams) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnDataReceived != nil {
				c.OnDataReceived(data, params)
			}
		})
	}
	cb.OnDataPacket = func(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnDataPacket != nil {
				c.OnDataPacket(data, params)
			}
		})
	}
	cb.OnTranscriptionReceived = func(segments []*lksdk.TranscriptionSegment, participant lksdk.Participant, publication lksdk.TrackPublication) {
		r.forEach(func(c *lksdk.RoomCallback) {
			if c.OnTranscriptionReceived != nil {
				c.OnTranscriptionReceived(segments, participant, publication)
			}
		})
	}

	return cb
}
