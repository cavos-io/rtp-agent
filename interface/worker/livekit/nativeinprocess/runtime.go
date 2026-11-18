// Package nativeinprocess is an isolated, experimental process-lifetime FFI owner.
// The starter uses it only with the explicit experimental opt-in. Only one owner may initialize the
// process-global LiveKit FFI; other FFI clients must not coexist in that process.
package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
)

var (
	ErrBackpressure = errors.New("native session PCM delivery queue full")
	ErrClosed       = errors.New("native runtime/session closed")
	ErrCaptureBusy  = errors.New("native session capture already in flight")
	ErrControlBusy  = errors.New("native playback control already pending")
	ErrSourceEnded  = errors.New("native input source ended")
	nativeClaimed   atomic.Bool
)

// Frame owns Go memory; PCM is mono PCM16 at 24 kHz. At is native callback
// monotonic nanoseconds, not a wall timestamp or an RTP timestamp.
type Frame struct {
	At  int64
	PCM []int16
}
type Session struct {
	participants                          map[string]bool // protected by mu
	departures                            chan string
	departuresEnabled                     bool
	holdSource, holdTrack                 uint64
	holdClear                             *command
	dataPending                           bool
	holdPublished, holdBusy, holdStarting bool
	recordRemote                          bool
	recordings                            chan RecordingFrame
	recordingTracks                       map[string]recordingTrack
	recordingParticipants                 map[string]*pb.ParticipantInfo
	recordingPublications                 map[string]recordingPublication
	chatEnabled                           bool // configured before session submission
	chats                                 chan Chat
	chatReaders                           map[uint64]*chatReader
	ctx                                   context.Context
	cancel                                context.CancelFunc
	frames                                chan Frame
	stateErrors                           chan error
	dtmf                                  chan DTMF
	ready, done                           chan struct{}
	mu                                    sync.Mutex
	err                                   error
	rt                                    *Runtime
	stopWake                              func() bool
	explicitSelection                     bool
	microphone                            bool
	microphones                           map[string]bool
	inputSID                              atomic.Value
	outputSubscribed                      chan struct{}
	outputSubscribedOnce                  bool
	outputSubscriptionSID                 string
	subscribed                            chan struct{}
	subscribedOnce                        bool
	// Everything below is accessed only by the runtime owner goroutine.
	room, local, track, stream, source, output  uint64
	captionPending                              int
	dtmfPending                                 bool
	stateDesired, stateActive                   *ClientState
	stateDirty, statePending                    bool
	stateStopped                                bool
	stateStep                                   int
	stateTimer                                  *time.Timer
	outputSID                                   string // published before ready closes; immutable thereafter
	connecting, closing, published, captureBusy bool
	paused                                      bool
	parked, control                             *command
	participant, trackSID                       string
	reconnecting, awaitingTrack                 bool
	rebindDeadline                              time.Time
	rebindTimer                                 *time.Timer
}

func newSession(ctx context.Context, capacity int) *Session {
	ctx, cancel := context.WithCancel(ctx)
	return &Session{participants: make(map[string]bool), departures: make(chan string, 32), recordings: make(chan RecordingFrame, 100), recordingTracks: make(map[string]recordingTrack), recordingParticipants: make(map[string]*pb.ParticipantInfo), recordingPublications: make(map[string]recordingPublication), chats: make(chan Chat, 8), chatReaders: make(map[uint64]*chatReader), microphones: make(map[string]bool), outputSubscribed: make(chan struct{}), ctx: ctx, cancel: cancel, dtmf: make(chan DTMF, 32), stateErrors: make(chan error, 1), frames: make(chan Frame, capacity), ready: make(chan struct{}), subscribed: make(chan struct{}), done: make(chan struct{})}
}
func (s *Session) Frames() <-chan Frame  { return s.frames }
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Err() error            { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Session) setError(e error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = e
	}
}
func (s *Session) deliver(f Frame) error {
	if e := s.ctx.Err(); e != nil {
		return e
	}
	select {
	case s.frames <- f:
		return nil
	default:
		return ErrBackpressure
	}
}
func (s *Session) finish(e error) {
	s.setError(e)
	if s.stopWake != nil {
		s.stopWake()
	}
	s.cancel()
	if s.rebindTimer != nil {
		s.rebindTimer.Stop()
	}
	for _, c := range []*command{s.parked, s.control, s.holdClear} {
		if c != nil {
			c.reply <- errors.Join(ErrClosed, e)
		}
	}
	s.parked = nil
	s.control = nil
	close(s.departures)
	close(s.recordings)
	close(s.chats)
	close(s.dtmf)
	close(s.frames)
	close(s.done)
}
func (s *Session) WaitReady(ctx context.Context) error {
	select {
	case <-s.done:
		return errors.Join(ErrClosed, s.Err())
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ready:
		return nil
	}
}

// Close cancels this session and waits for native disconnect/handle release.
// A timed-out wait does not drop the runtime's ownership of a late connection.
func (s *Session) Close(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
		e := s.Err()
		if errors.Is(e, context.Canceled) {
			return nil
		}
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Capture waits for native acknowledgement. Cancellation stops waiting, not an
// already-submitted native capture. A second in-flight capture is rejected.
func (s *Session) Capture(ctx context.Context, pcm []int16) error {
	if len(pcm) < 1 || len(pcm) > 1200 {
		return errors.New("capture requires 1..1200 mono 24kHz samples")
	}
	stop := context.AfterFunc(ctx, s.rt.notify)
	defer stop()
	c := command{s: s, pcm: append([]int16(nil), pcm...), reply: make(chan error, 1), ctx: ctx}
	if e := s.rt.submit(ctx, c); e != nil {
		return e
	}
	select {
	case e := <-c.reply:
		return e
	case <-s.done:
		return errors.Join(ErrClosed, s.Err())
	case <-ctx.Done():
		return ctx.Err()
	}
}

type nativeBackend struct {
	events       <-chan callbackEvent
	request      func(*pb.FfiRequest) (*pb.FfiResponse, error)
	drop         func(uint64) error
	pcm          func(*pb.AudioFrameBufferInfo) ([]int16, error)
	capture      func(uint64, uint64, []int16) error
	publishState func(uint64, uint64, []byte) error
	publishData  func(uint64, uint64, string, []byte) error
	dispose      func()
	overflow     func() bool
}
type command struct {
	data        *DataPacket
	caption     *pb.FfiRequest
	dtmf        *DTMF
	clientState *ClientState
	stopState   bool
	s           *Session
	url, token  string
	kind        string
	pcm         []int16
	reply       chan error
	ctx         context.Context
}
type pendingRequest struct {
	s     *Session
	kind  string
	reply chan error
}
type Runtime struct {
	auxStreams     map[uint64]*Session
	b              nativeBackend
	commands       chan command
	wake           chan struct{}
	done           chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	err            error
	next           uint64
	pending        map[uint64]pendingRequest
	sessions       map[*Session]bool
	rooms, streams map[uint64]*Session
}

// Open claims the native runtime for the process lifetime. Reinitialization and
// unloading are deliberately unsupported. Native threads can outlive dispose.
func Open(path string) (*Runtime, error) {
	if path == "" {
		return nil, errors.New("native library path required")
	}
	if !nativeClaimed.CompareAndSwap(false, true) {
		return nil, errors.New("native runtime already claimed")
	}
	if e := startNative(path); e != nil {
		return nil, e
	}
	return startRuntime(nativeBackend{events: callbacks.events, request: request, drop: dropHandle, pcm: copyPCM, capture: capture, publishState: publishStateData, publishData: publishData, dispose: disposeNative, overflow: callbacks.overflow.Load}), nil
}
func startRuntime(b nativeBackend) *Runtime {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{auxStreams: make(map[uint64]*Session), b: b, commands: make(chan command), wake: make(chan struct{}, 1), done: make(chan struct{}), ctx: ctx, cancel: cancel, pending: make(map[uint64]pendingRequest), sessions: make(map[*Session]bool), rooms: make(map[uint64]*Session), streams: make(map[uint64]*Session)}
	go r.run()
	return r
}
func (r *Runtime) notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
func (r *Runtime) submit(ctx context.Context, c command) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if r.ctx.Err() != nil {
		return ErrClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return ErrClosed
	case <-r.ctx.Done():
		return ErrClosed
	case r.commands <- c:
		return nil
	}
}

// Connect starts asynchronous room setup. ctx governs the session lifetime.
// Delivery is bounded to 25 native 20ms frames (500ms), with no silent drops:
// saturation fails/disconnects only this session, never pads or retimes PCM.
func (r *Runtime) Connect(ctx context.Context, url, token string) (*Session, error) {
	return r.ConnectSelected(ctx, url, token, "", "")
}

// ConnectSelected pins the caller and publication discovered by the agent.
func (r *Runtime) ConnectSelected(ctx context.Context, url, token, participant, sid string) (*Session, error) {
	return r.connectSelected(ctx, url, token, participant, sid, false, false, false, false)
}
func (r *Runtime) connectSelected(ctx context.Context, url, token, participant, sid string, microphone, chat, record, departures bool) (*Session, error) {
	if url == "" || token == "" {
		return nil, errors.New("URL and token required")
	}
	s := newSession(ctx, 25)
	s.rt = r
	s.microphone = microphone
	s.chatEnabled = chat
	s.recordRemote = record
	s.departuresEnabled = departures
	s.inputSID.Store(sid)
	if microphone {
		s.microphones[sid] = true
	}
	s.participant = participant
	s.trackSID = sid
	s.explicitSelection = participant != "" || sid != ""
	if s.explicitSelection && (participant == "" || sid == "") {
		s.finish(errors.New("participant and track SID required together"))
		return nil, s.Err()
	}
	s.stopWake = context.AfterFunc(s.ctx, r.notify)
	if e := r.submit(ctx, command{s: s, url: url, token: token, ctx: ctx}); e != nil {
		s.finish(e)
		return nil, e
	}
	return s, nil
}
func (r *Runtime) Close(ctx context.Context) error {
	r.cancel()
	r.notify()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.err
	}
}
func (r *Runtime) id(s *Session, kind string, reply chan error) uint64 {
	r.next++
	r.pending[r.next] = pendingRequest{s, kind, reply}
	return r.next
}
func (r *Runtime) resolve(id uint64, kind string) (pendingRequest, error) {
	p, ok := r.pending[id]
	if !ok || p.kind != kind {
		return p, fmt.Errorf("unexpected %s callback %d", kind, id)
	}
	delete(r.pending, id)
	return p, nil
}
func (r *Runtime) keep() []uint64 {
	var out []uint64
	for s := range r.sessions {
		for _, tr := range s.recordingTracks {
			out = append(out, tr.track, tr.stream)
		}
		out = append(out, s.room, s.local, s.track, s.stream, s.source, s.output, s.holdSource, s.holdTrack)
	}
	return out
}
func (r *Runtime) release(ev proto.Message) error {
	keep := r.keep()
	for _, h := range ownedHandles(ev.ProtoReflect()) {
		found := h == 0
		for _, v := range keep {
			if v == h {
				found = true
				break
			}
		}
		if !found {
			if e := r.b.drop(h); e != nil {
				return e
			}
		}
	}
	return nil
}
func (r *Runtime) finish(s *Session, e error) {
	r.closeChatReaders(s)
	s.setError(r.closeRecordingTracks(s))
	if s.stateTimer != nil {
		s.stateTimer.Stop()
		s.stateTimer = nil
	}
	for id, p := range r.pending {
		if p.s == s && p.kind == "client-state" {
			delete(r.pending, id)
		}
		if p.s == s && (p.kind == "caption" || p.kind == "dtmf" || p.kind == "hold-publish" || p.kind == "hold-capture" || p.kind == "data") {
			delete(r.pending, id)
			p.reply <- errors.Join(ErrClosed, e)
		}
	}
	s.captionPending = 0
	delete(r.sessions, s)
	s.finish(e)
}
func (r *Runtime) failSession(s *Session, e error) error {
	s.setError(e)
	s.cancel()
	r.closeChatReaders(s)
	if s.connecting || s.closing {
		return nil
	}
	if s.room == 0 {
		r.finish(s, e)
		return nil
	}
	s.closing = true
	id := r.id(s, "disconnect", nil)
	_, err := r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_Disconnect{Disconnect: &pb.DisconnectRequest{RoomHandle: proto.Uint64(s.room), RequestAsyncId: proto.Uint64(id)}}})
	if err != nil {
		delete(r.pending, id)
	}
	return err
}
func (r *Runtime) handle(c command) error {
	s := c.s
	if isCallControl(c.kind) {
		return r.handleCallControl(c)
	}
	if c.data != nil {
		return r.handleData(c)
	}
	if c.dtmf != nil {
		return r.handleDTMF(c)
	}
	if c.stopState {
		s.stateStopped = true
		s.stateActive = nil
		s.stateDesired = nil
		s.stateDirty = false
		if s.stateTimer != nil {
			s.stateTimer.Stop()
			s.stateTimer = nil
		}
		c.reply <- nil
		return nil
	}
	if c.clientState != nil {
		return r.handleClientState(c)
	}
	if c.caption != nil {
		return r.handleCaption(c)
	}
	if c.kind != "" {
		return r.playbackControl(c)
	}
	if c.pcm != nil {
		e := c.ctx.Err()
		if e == nil && (!r.sessions[s] || s.closing || s.ctx.Err() != nil) {
			e = ErrClosed
		}
		if e == nil && !s.published {
			e = errors.New("native output not ready")
		}
		if e == nil && s.control != nil {
			e = ErrControlBusy
		}
		if e == nil && (s.captureBusy || s.parked != nil) {
			e = ErrCaptureBusy
		}
		if e != nil {
			c.reply <- e
			return nil
		}
		if s.paused {
			s.parked = &c
			return nil
		}
		id := r.id(s, "capture", c.reply)
		s.captureBusy = true
		if e = r.b.capture(s.source, id, c.pcm); e != nil {
			delete(r.pending, id)
			s.captureBusy = false
			c.reply <- e
			return r.failSession(s, e)
		}
		return nil
	}
	r.sessions[s] = true
	if r.ctx.Err() != nil || s.ctx.Err() != nil {
		return r.failSession(s, context.Canceled)
	}
	s.connecting = true
	id := r.id(s, "connect", nil)
	_, e := r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_Connect{Connect: &pb.ConnectRequest{Url: proto.String(c.url), Token: proto.String(c.token), Options: nativeRoomOptions(s.chatEnabled), RequestAsyncId: proto.Uint64(id)}}})
	if e != nil {
		delete(r.pending, id)
		s.connecting = false
		return r.failSession(s, e)
	}
	return nil
}
func (r *Runtime) run() {
	defer close(r.done)
	defer func() {
		r.cancel()
		// Native dispose is intentionally on the owner, never on a callback thread.
		// A hung native call cannot be force-killed in-process; Close's context bounds waiting.
		for s := range r.sessions {
			s.setError(r.closeRecordingTracks(s))
		}
		r.b.dispose()
		for s := range r.sessions {
			r.finish(s, errors.Join(ErrClosed, r.err))
		}
		for {
			select {
			case c := <-r.commands:
				if c.data != nil || c.pcm != nil || c.kind != "" || c.caption != nil || c.dtmf != nil || c.clientState != nil || c.stopState {
					c.reply <- ErrClosed
				} else {
					c.s.finish(ErrClosed)
				}
			default:
				return
			}
		}
	}()
	for {
		var err error
		for s := range r.sessions {
			for _, reader := range s.chatReaders {
				if !time.Now().Before(reader.deadline) {
					if e := r.failSession(s, errors.New("native chat stream timed out")); e != nil {
						err = e
					}
					break
				}
			}
			if err != nil {
				break
			}
			if s.parked != nil && s.parked.ctx.Err() != nil {
				s.parked.reply <- s.parked.ctx.Err()
				s.parked = nil
			}
			if !s.rebindDeadline.IsZero() && !time.Now().Before(s.rebindDeadline) {
				if e := r.failSession(s, errors.New("native track resubscription timed out")); e != nil {
					err = e
					break
				}
			}
			if s.ctx.Err() != nil || r.ctx.Err() != nil {
				if e := r.failSession(s, context.Canceled); e != nil {
					err = e
					break
				}
			}
		}
		if err == nil && r.b.overflow() {
			err = errors.New("native callback queue overflow")
		}
		if err != nil {
			r.mu.Lock()
			r.err = err
			r.mu.Unlock()
			return
		}
		if r.ctx.Err() != nil && len(r.sessions) == 0 && len(r.pending) == 0 {
			return
		}
		select {
		case <-r.wake:
		case c := <-r.commands:
			err = r.handle(c)
		case raw := <-r.b.events:
			err = r.dispatch(raw)
		}
		if err != nil {
			r.mu.Lock()
			r.err = err
			r.mu.Unlock()
			return
		}
	}
}

// WaitSubscribed waits for the selected native input sink, not the first packet.
func (s *Session) WaitSubscribed(ctx context.Context) error {
	select {
	case <-s.done:
		return errors.Join(ErrClosed, s.Err())
	case <-ctx.Done():
		return ctx.Err()
	case <-s.subscribed:
		return nil
	}
}
