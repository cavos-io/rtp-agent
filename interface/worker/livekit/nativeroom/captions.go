//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cavos-io/rtp-agent/core/agent"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/google/uuid"
	lk "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/proto"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
	native "github.com/cavos-io/rtp-agent/interface/worker/livekit/nativeinprocess"
)

type captionPublisher struct {
	ctx      context.Context
	send     func(context.Context, *pb.FfiRequest) error
	identity string
}

func (p *captionPublisher) request(q *pb.FfiRequest) error {
	ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
	defer cancel()
	err := p.send(ctx, q)
	if err != nil && p.ctx.Err() == nil && !errors.Is(err, native.ErrSourceEnded) && !errors.Is(err, native.ErrClosed) {
		logger.Logger.Warnw("native caption publication failed", err)
	}
	return err
}
func (p *captionPublisher) packet(t *lk.Transcription) error {
	q := &pb.PublishTranscriptionRequest{ParticipantIdentity: proto.String(t.TranscribedParticipantIdentity), TrackId: proto.String(t.TrackId)}
	for _, s := range t.Segments {
		q.Segments = append(q.Segments, &pb.TranscriptionSegment{Id: proto.String(s.Id), Text: proto.String(s.Text), StartTime: proto.Uint64(s.StartTime), EndTime: proto.Uint64(s.EndTime), Final: proto.Bool(s.Final), Language: proto.String(s.Language)})
	}
	err := p.request(&pb.FfiRequest{Message: &pb.FfiRequest_PublishTranscription{PublishTranscription: q}})
	if p.ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
func (p *captionPublisher) open(o lksdk.StreamTextOptions) wl.CaptionWriter {
	id := uuid.NewString()
	q := &pb.SendStreamHeaderRequest{SenderIdentity: proto.String(p.identity), Header: &pb.DataStream_Header{StreamId: proto.String(id), Timestamp: proto.Int64(time.Now().UnixMilli()), MimeType: proto.String("text/plain"), Topic: proto.String(o.Topic), Attributes: maps.Clone(o.Attributes), ContentHeader: &pb.DataStream_Header_TextHeader{TextHeader: &pb.DataStream_TextHeader{OperationType: pb.DataStream_CREATE.Enum()}}}}
	if e := p.request(&pb.FfiRequest{Message: &pb.FfiRequest_SendStreamHeader{SendStreamHeader: q}}); e != nil {
		return nil
	}
	return &captionWriter{p: p, id: id}
}
func (p *captionPublisher) text(text string, o lksdk.StreamTextOptions) {
	if w := p.open(o); w != nil {
		w.Write(text)
		w.Close()
	}
}

// No worker goroutine or unbounded queue per caption: each stream serializes
// bounded acknowledged writes. A failed stream cannot resume with missing chunks.
type captionWriter struct {
	mu             sync.Mutex
	p              *captionPublisher
	id             string
	index          uint64
	closed, failed bool
}

func (w *captionWriter) Write(text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.failed {
		return
	}
	for len(text) > 0 {
		n := min(len(text), 12000)
		for n < len(text) && n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		if n == 0 {
			w.failed = true
			return
		}
		q := &pb.SendStreamChunkRequest{SenderIdentity: proto.String(w.p.identity), Chunk: &pb.DataStream_Chunk{StreamId: proto.String(w.id), ChunkIndex: proto.Uint64(w.index), Content: []byte(text[:n])}}
		if e := w.p.request(&pb.FfiRequest{Message: &pb.FfiRequest_SendStreamChunk{SendStreamChunk: q}}); e != nil {
			w.failed = true
			return
		}
		w.index++
		text = text[n:]
	}
}
func (w *captionWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	reason := ""
	if w.failed {
		reason = "caption publication failed"
	}
	_ = w.p.request(&pb.FfiRequest{Message: &pb.FfiRequest_SendStreamTrailer{SendStreamTrailer: &pb.SendStreamTrailerRequest{SenderIdentity: proto.String(w.p.identity), Trailer: &pb.DataStream_Trailer{StreamId: proto.String(w.id), Reason: proto.String(reason)}}}})
}
func startNativeCaptions(ctx context.Context, s *native.Session, session *agent.AgentSession, identity, caller, track string, opts wl.RoomOptions) (func(context.Context) error, error) {
	sid, err := s.OutputTrackSID(ctx)
	if err != nil {
		return nil, err
	}
	if sid == "" {
		return nil, errors.New("native captions require published output track SID")
	}
	captionCtx, cancel := context.WithCancel(ctx)
	p := &captionPublisher{ctx: captionCtx, send: s.PublishCaption, identity: identity}
	f := wl.NewCaptionForwarder(session, wl.CaptionTransport{DisableTranscriptionOutput: opts.DisableTranscriptionOutput, TranscriptionNextOutput: opts.TranscriptionNextOutput, AgentIdentity: identity, AgentTrack: sid, CallerIdentity: caller, CallerTrack: track, PublishPacket: func(t *lk.Transcription) error {
		if t.TranscribedParticipantIdentity == caller {
			t = proto.Clone(t).(*lk.Transcription)
			t.TrackId = s.InputTrackSID()
		}
		return p.packet(t)
	}, PublishText: func(text string, o lksdk.StreamTextOptions) {
		if o.Attributes[wl.RoomIOTranscriptionTrackIDAttribute] == track {
			o.Attributes = maps.Clone(o.Attributes)
			o.Attributes[wl.RoomIOTranscriptionTrackIDAttribute] = s.InputTrackSID()
		}
		p.text(text, o)
	}, OpenText: p.open, Usable: func() bool { return ctx.Err() == nil }})
	return func(closeCtx context.Context) error {
		stop := context.AfterFunc(closeCtx, cancel)
		defer stop()
		defer cancel()
		return f.Close(closeCtx)
	}, nil
}
