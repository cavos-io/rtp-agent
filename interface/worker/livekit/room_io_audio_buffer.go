package livekit

import (
	"sync"
	"time"

	"github.com/livekit/media-sdk/jitter"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

const roomIOAudioGapTimeout = 400 * time.Millisecond

// roomIOAudioBuffer owns one track's jitter callbacks and stream finalization.
// jitter.Buffer.Close stops its timer but does not wait for active callbacks.
type roomIOAudioBuffer struct {
	buffer *jitter.Buffer
	opMu   sync.Mutex // serializes Push and stop
	mu     sync.Mutex // waits for callbacks before finalizing the stream
	closed bool
	finish func()
}

func newRoomIOAudioBuffer(onPacket func([]byte), finish func()) *roomIOAudioBuffer {
	b := &roomIOAudioBuffer{finish: finish}
	var lastSeq uint16
	var emitted bool
	b.buffer = jitter.NewBuffer(&codecs.OpusPacket{}, roomIOAudioGapTimeout, func(packets []jitter.ExtPacket) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.closed {
			return
		}
		for _, packet := range packets {
			// The upstream buffer can emit duplicates queued behind a gap.
			if emitted && packet.SequenceNumber == lastSeq {
				continue
			}
			lastSeq, emitted = packet.SequenceNumber, true
			onPacket(packet.Payload)
		}
	})
	return b
}

func (b *roomIOAudioBuffer) Push(packet *rtp.Packet) {
	b.opMu.Lock()
	defer b.opMu.Unlock()
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if !closed && packet != nil && len(packet.Payload) != 0 {
		b.buffer.Push(packet)
	}
}

func (b *roomIOAudioBuffer) stop(drain bool) {
	b.opMu.Lock()
	defer b.opMu.Unlock()
	b.mu.Lock()
	closed := b.closed
	if !drain {
		b.closed = true
	}
	b.mu.Unlock()
	if closed {
		return
	}
	b.buffer.Close()
	if drain {
		b.buffer.Flush()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if drain && b.finish != nil {
		b.finish()
	}
}

func (rio *RoomIO) registerAudioBuffer(b *roomIOAudioBuffer) bool {
	rio.mu.Lock()
	closed := rio.closed
	if !closed {
		if rio.audioBuffers == nil {
			rio.audioBuffers = make(map[*roomIOAudioBuffer]struct{})
		}
		rio.audioBuffers[b] = struct{}{}
	}
	rio.mu.Unlock()
	if closed {
		b.stop(false)
	}
	return !closed
}

func (rio *RoomIO) releaseAudioBuffer(b *roomIOAudioBuffer) {
	b.stop(false)
	rio.mu.Lock()
	delete(rio.audioBuffers, b)
	rio.mu.Unlock()
}

func (rio *RoomIO) stopAudioBuffers() {
	rio.mu.Lock()
	buffers := rio.audioBuffers
	rio.audioBuffers = nil
	rio.mu.Unlock()
	// Callbacks consult RoomIO state, so never wait while holding rio.mu.
	for b := range buffers {
		b.stop(false)
	}
}
