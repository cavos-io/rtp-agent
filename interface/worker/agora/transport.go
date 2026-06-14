package agora

import (
	"context"
	"fmt"
	"sync"

	"github.com/cavos-io/rtp-agent/interface/worker"
)

type EventKind string

const (
	EventConnected    EventKind = "connected"
	EventDisconnected EventKind = "disconnected"
	EventUserJoined   EventKind = "user_joined"
	EventUserLeft     EventKind = "user_left"
	EventError        EventKind = "error"
)

type Event struct {
	Kind    EventKind
	Channel string
	UserID  string
	Reason  int
	Err     error
}

type EventHandler func(Event)

type PCMFrame struct {
	Data       []byte
	SampleRate int
	Channels   int
	StartPTSMS int64
}

func (f PCMFrame) Validate() error {
	if len(f.Data) == 0 {
		return fmt.Errorf("agora PCM frame data is required")
	}
	if f.SampleRate <= 0 {
		return fmt.Errorf("agora PCM frame sample rate must be positive")
	}
	if f.Channels <= 0 {
		return fmt.Errorf("agora PCM frame channels must be positive")
	}
	if f.SampleRate%100 != 0 {
		return fmt.Errorf("agora PCM frame sample rate must produce whole 10 ms frames")
	}
	bytesPer10MS := (f.SampleRate / 100) * f.Channels * 2
	if len(f.Data)%bytesPer10MS != 0 {
		return fmt.Errorf("agora PCM frame data must be an exact multiple of 10 ms 16-bit interleaved PCM")
	}
	return nil
}

type ChannelClient interface {
	Join(context.Context, worker.AgoraOptions, EventHandler, AudioHandler) error
	Leave(context.Context) error
	PublishPCM(context.Context, PCMFrame) error
}

type Transport struct {
	opts      worker.AgoraOptions
	client    ChannelClient
	events    chan Event
	audio     AudioHandler
	closeOnce sync.Once
	mu        sync.Mutex
	closed    bool
}

func NewTransport(opts worker.AgoraOptions, client ChannelClient) *Transport {
	return &Transport{
		opts:   opts,
		client: client,
		events: make(chan Event, 16),
	}
}

func (t *Transport) Events() <-chan Event {
	if t == nil {
		events := make(chan Event)
		close(events)
		return events
	}
	return t.events
}

func (t *Transport) SetAudioHandler(handler AudioHandler) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.audio = handler
	t.mu.Unlock()
}

func (t *Transport) Join(ctx context.Context) error {
	if t == nil {
		return fmt.Errorf("agora transport is nil")
	}
	if err := t.opts.Validate(); err != nil {
		return err
	}
	if t.client == nil {
		return fmt.Errorf("agora channel client is required")
	}
	t.mu.Lock()
	audio := t.audio
	t.mu.Unlock()
	return t.client.Join(ctx, t.opts, t.emit, audio)
}

func (t *Transport) Leave(ctx context.Context) error {
	if t == nil || t.client == nil {
		return nil
	}
	return t.client.Leave(ctx)
}

func (t *Transport) PublishPCM(ctx context.Context, frame PCMFrame) error {
	if t == nil {
		return fmt.Errorf("agora transport is nil")
	}
	if err := frame.Validate(); err != nil {
		return err
	}
	if t.client == nil {
		return fmt.Errorf("agora channel client is required")
	}
	return t.client.PublishPCM(ctx, frame)
}

func (t *Transport) Close(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var err error
	t.closeOnce.Do(func() {
		err = t.Leave(ctx)
		t.mu.Lock()
		if !t.closed {
			t.closed = true
			close(t.events)
		}
		t.mu.Unlock()
	})
	return err
}

func (t *Transport) emit(event Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	select {
	case t.events <- event:
	default:
	}
}
