package nativeinprocess

import (
	"context"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"time"
)

const preConnectTopic = "lk.agent.pre-connect-audio-buffer"
const maxPreConnectBytes = 4 * 1024 * 1024

// PreConnectAudio owns a completed byte stream. Chunk boundaries are retained for
// encoded packets; decoding and replay belong to the room adapter, off the FFI loop.
type PreConnectAudio struct {
	Info     *pb.ByteStreamInfo
	Chunks   [][]byte
	Received time.Time
}
type preConnectReader struct {
	handle   uint64
	audio    PreConnectAudio
	size     int
	deadline time.Time
	timer    *time.Timer
}

func (s *Session) PreConnectAudio() <-chan PreConnectAudio { return s.preConnectAudio }

// ConnectMicrophoneCallWithPreConnect adds bounded startup audio delivery. A zero
// timeout disables it. Existing connect APIs retain their audio-only behavior.
func (r *Runtime) ConnectMicrophoneCallWithPreConnect(ctx context.Context, url, token, participant, sid string, chat, record bool, timeout time.Duration) (*Session, error) {
	return r.connectSelected(ctx, url, token, participant, sid, true, chat, record, true, timeout)
}

func (s *Session) stopPreConnect() {
	if s.preConnectReader != nil {
		s.preConnectReader.timer.Stop()
		s.preConnectReader = nil
	}
	s.preConnectComplete = true
}
func (r *Runtime) openPreConnect(s *Session, ev *pb.ByteStreamOpened) error {
	info := ev.GetReader().GetInfo()
	if !s.preConnectEnabled || s.preConnectStopped.Load() || s.preConnectComplete || s.preConnectReader != nil || s.closing || s.ctx.Err() != nil || info.GetTopic() != preConnectTopic || ev.GetParticipantIdentity() != s.participant || info.GetAttributes()["trackId"] != s.trackSID || s.trackSID == "" {
		return nil
	}
	if info.GetTotalLength() > maxPreConnectBytes {
		s.stopPreConnect()
		return nil
	}
	h := ev.GetReader().GetHandle().GetId()
	if h == 0 {
		s.stopPreConnect()
		return nil
	}
	timeout := s.preConnectTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	reader := &preConnectReader{handle: h, audio: PreConnectAudio{Info: proto.Clone(info).(*pb.ByteStreamInfo), Received: time.Now()}, deadline: time.Now().Add(timeout)}
	_, err := r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_ByteReadIncremental{ByteReadIncremental: &pb.ByteStreamReaderReadIncrementalRequest{ReaderHandle: proto.Uint64(h)}}})
	if err != nil {
		s.stopPreConnect()
		return nil
	}
	// take_handle transfers the reader to the native task, as for text streams.
	ev.Reader.Handle.Id = proto.Uint64(0)
	reader.timer = time.AfterFunc(timeout, r.notify)
	s.preConnectReader = reader
	return nil
}
func (r *Runtime) preConnectEvent(ev *pb.ByteStreamReaderEvent) error {
	for s := range r.sessions {
		reader := s.preConnectReader
		if reader == nil || reader.handle != ev.GetReaderHandle() {
			continue
		}
		if s.preConnectStopped.Load() || time.Now().After(reader.deadline) || s.closing || s.ctx.Err() != nil {
			s.stopPreConnect()
			return nil
		}
		if chunk := ev.GetChunkReceived(); chunk != nil {
			data := chunk.GetContent()
			if reader.size+len(data) > maxPreConnectBytes || len(reader.audio.Chunks) >= 4096 {
				s.stopPreConnect()
				return nil
			}
			reader.audio.Chunks = append(reader.audio.Chunks, append([]byte(nil), data...))
			reader.size += len(data)
		}
		if end := ev.GetEos(); end != nil {
			s.stopPreConnect()
			if end.GetError() == nil {
				// Routing/format attributes are taken from the header, never overwritten by trailers.
				select {
				case s.preConnectAudio <- reader.audio:
				default:
				}
			}
		}
		return nil
	}
	return nil
}

// StopPreConnectAudio stops accepting late startup streams after replay or timeout.
func (s *Session) StopPreConnectAudio() { s.preConnectStopped.Store(true); s.rt.notify() }

// InputSubscribed closes when the selected microphone first becomes available.
func (s *Session) InputSubscribed() <-chan struct{} { return s.subscribed }
