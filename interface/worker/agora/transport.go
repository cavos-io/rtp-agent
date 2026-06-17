package agora

import (
	"context"
	"fmt"
	"sync"
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
	if len(f.Data) != bytesPer10MS {
		return fmt.Errorf("agora PCM frame data must be exactly 10 ms of 16-bit interleaved PCM")
	}
	return nil
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type ChannelClient interface {
	Join(context.Context, Options, EventHandler, AudioHandler) error
	Leave(context.Context) error
	PublishPCM(context.Context, PCMFrame) error
}

type Transport struct {
	opts       Options
	client     ChannelClient
	events     chan Event
	audio      AudioHandler
	closeOnce  sync.Once
	mu         sync.Mutex
	joinCancel context.CancelFunc
	joinSeq    uint64
	closing    bool
	closed     bool
}

func NewTransport(opts Options, client ChannelClient) *Transport {
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
	opts, err := ResolveJoinOptions(t.opts)
	if err != nil {
		return err
	}
	if t.client == nil {
		return fmt.Errorf("agora channel client is required")
	}
	ctx = normalizeContext(ctx)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	joinCtx, cancel := context.WithCancel(ctx)
	t.mu.Lock()
	if t.closing || t.closed {
		t.mu.Unlock()
		cancel()
		return fmt.Errorf("agora transport is closed")
	}
	audio := t.audio
	t.joinSeq++
	joinSeq := t.joinSeq
	t.joinCancel = cancel
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		if t.joinSeq == joinSeq {
			t.joinCancel = nil
		}
		t.mu.Unlock()
		cancel()
	}()
	return t.client.Join(joinCtx, opts, t.emit, audio)
}

func (t *Transport) Leave(ctx context.Context) error {
	if t == nil || t.client == nil {
		return nil
	}
	return t.client.Leave(normalizeContext(ctx))
}

func (t *Transport) PublishPCM(ctx context.Context, frame PCMFrame) error {
	if t == nil {
		return fmt.Errorf("agora transport is nil")
	}
	if err := frame.Validate(); err != nil {
		return err
	}
	ctx = normalizeContext(ctx)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	t.mu.Lock()
	closed := t.closing || t.closed
	t.mu.Unlock()
	if closed {
		return fmt.Errorf("agora transport is closed")
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
		t.mu.Lock()
		cancelJoin := t.joinCancel
		t.closing = true
		t.mu.Unlock()
		if cancelJoin != nil {
			cancelJoin()
		}
		err = t.Leave(normalizeContext(ctx))
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
		if event.Kind != EventError {
			return
		}
		select {
		case <-t.events:
		default:
		}
		select {
		case t.events <- event:
		default:
		}
	}
}
