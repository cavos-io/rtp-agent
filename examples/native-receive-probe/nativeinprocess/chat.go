package nativeinprocess

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	"google.golang.org/protobuf/proto"
	"maps"
	"strings"
	"time"
)

// Chat is a completed lk.chat stream; metadata and text are owned Go values.
type Chat struct {
	Text, Sender string
	Info         *pb.TextStreamInfo
}
type chatReader struct {
	info     *pb.TextStreamInfo
	sender   string
	text     strings.Builder
	deadline time.Time
	timer    *time.Timer
}

const maxChatBytes = 64 * 1024

func (s *Session) Chats() <-chan Chat { return s.chats }
func (r *Runtime) openChat(s *Session, ev *pb.TextStreamOpened) error {
	info := ev.GetReader().GetInfo()
	if !s.chatEnabled || s.closing || s.ctx.Err() != nil || info.GetTopic() != "lk.chat" || ev.GetParticipantIdentity() != s.participant {
		return nil
	}
	if len(s.chatReaders) >= 8 || info.GetTotalLength() > maxChatBytes {
		return r.failSession(s, errors.New("native chat stream limit exceeded"))
	}
	h := ev.GetReader().GetHandle().GetId()
	if h == 0 {
		return r.failSession(s, errors.New("native chat reader missing handle"))
	}
	reader := &chatReader{info: proto.Clone(info).(*pb.TextStreamInfo), sender: ev.GetParticipantIdentity(), deadline: time.Now().Add(30 * time.Second)}
	reader.timer = time.AfterFunc(30*time.Second, r.notify)
	_, e := r.b.request(&pb.FfiRequest{Message: &pb.FfiRequest_TextReadIncremental{TextReadIncremental: &pb.TextStreamReaderReadIncrementalRequest{ReaderHandle: proto.Uint64(h)}}})
	if e != nil {
		reader.timer.Stop()
		return r.failSession(s, e)
	}
	// Native take_handle transfers ownership to its reader task. Suppress
	// dispatch release; only Go correlation state remains here.
	ev.Reader.Handle.Id = proto.Uint64(0)
	s.chatReaders[h] = reader
	return nil
}
func (r *Runtime) chatEvent(ev *pb.TextStreamReaderEvent) error {
	for s := range r.sessions {
		h := ev.GetReaderHandle()
		reader := s.chatReaders[h]
		if reader == nil {
			continue
		}
		if c := ev.GetChunkReceived(); c != nil {
			if reader.text.Len()+len(c.GetContent()) > maxChatBytes {
				return r.failSession(s, errors.New("native chat payload limit exceeded"))
			}
			reader.text.WriteString(c.GetContent())
		}
		if end := ev.GetEos(); end != nil {
			reader.timer.Stop()
			delete(s.chatReaders, h)
			if end.GetError() != nil {
				return r.failSession(s, fmt.Errorf("native chat read failed: %v", end.GetError()))
			}
			if s.closing || s.ctx.Err() != nil {
				return nil
			}
			if reader.info.Attributes == nil {
				reader.info.Attributes = make(map[string]string)
			}
			maps.Copy(reader.info.Attributes, end.GetAttributes())
			select {
			case s.chats <- Chat{Text: reader.text.String(), Sender: reader.sender, Info: reader.info}:
				return nil
			default:
				return r.failSession(s, errors.New("native chat delivery queue full"))
			}
		}
		return nil
	}
	return nil // late reader event after cleanup
}
func (r *Runtime) closeChatReaders(s *Session) {
	for h, reader := range s.chatReaders {
		reader.timer.Stop()
		delete(s.chatReaders, h)
	}
}

// ConnectMicrophoneChat enables bounded lk.chat receive before connecting.
// Existing connect methods retain their audio-only behavior.
func (r *Runtime) ConnectMicrophoneChat(ctx context.Context, url, token, participant, sid string, enabled bool) (*Session, error) {
	return r.connectSelected(ctx, url, token, participant, sid, true, enabled, false)
}
func nativeRoomOptions(chat bool) *pb.RoomOptions {
	opts := &pb.RoomOptions{AutoSubscribe: proto.Bool(true)}
	if chat {
		opts.DataStream = &pb.RoomDataStreamOptions{MaxPayloadByteLength: proto.Uint64(maxChatBytes)}
	}
	return opts
}
