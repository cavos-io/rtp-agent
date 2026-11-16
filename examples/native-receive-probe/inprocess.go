//go:build nativeinprocess

package main

/*
#include "bridge.h"
#include <stdlib.h>
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"google.golang.org/protobuf/proto"
	pb "native-receive-probe/internal/ffipb"
)

// Experimental single-owner dispatcher. Native rooms run concurrently; only
// callback handling is serialized. No receive timing or playout policy lives here.
// This is compiled only for the isolated in-process experiment.
type processRoom struct {
	name                                       string
	room, local, track, stream, source, output uint64
	closed, closing, published                 bool
	samples, first, last, maxGap               int64
	frames, markers, captures                  int
	onPCM                                      func(int64, []int16) error
}
type processRequest struct {
	room *processRoom
	kind string
}
type processRuntime struct {
	next    uint64
	pending map[uint64]processRequest
	rooms   map[uint64]*processRoom
	streams map[uint64]*processRoom
}

func newProcessRuntime() *processRuntime {
	return &processRuntime{pending: make(map[uint64]processRequest), rooms: make(map[uint64]*processRoom), streams: make(map[uint64]*processRoom)}
}
func (p *processRuntime) id(r *processRoom, kind string) uint64 {
	p.next++
	p.pending[p.next] = processRequest{r, kind}
	return p.next
}
func (p *processRuntime) connect(url, jwt string, r *processRoom) error {
	_, err := request(&pb.FfiRequest{Message: &pb.FfiRequest_Connect{Connect: &pb.ConnectRequest{Url: proto.String(url), Token: proto.String(jwt), Options: &pb.RoomOptions{AutoSubscribe: proto.Bool(true)}, RequestAsyncId: proto.Uint64(p.id(r, "connect"))}}})
	return err
}
func dropProcessHandle(h uint64) error {
	if h != 0 && !bool(C.probe_drop(C.uint64_t(h))) {
		return fmt.Errorf("drop %d failed", h)
	}
	return nil
}
func (p *processRuntime) closeRoom(r *processRoom) error {
	if r.closing || r.closed {
		return nil
	}
	r.closing = true
	_, err := request(&pb.FfiRequest{Message: &pb.FfiRequest_Disconnect{Disconnect: &pb.DisconnectRequest{RoomHandle: proto.Uint64(r.room), RequestAsyncId: proto.Uint64(p.id(r, "disconnect"))}}})
	return err
}
func (p *processRuntime) resolve(id uint64, kind string) (*processRoom, error) {
	item, ok := p.pending[id]
	if !ok || item.kind != kind {
		return nil, fmt.Errorf("unmatched %s callback %d", kind, id)
	}
	delete(p.pending, id)
	return item.room, nil
}
func (p *processRuntime) keep() []uint64 {
	var h []uint64
	for _, r := range p.rooms {
		h = append(h, r.room, r.local, r.track, r.stream, r.source, r.output)
	}
	return h
}
func (p *processRuntime) dispatch(raw callbackEvent) (err error) {
	ev := new(pb.FfiEvent)
	if err = proto.Unmarshal(raw.data, ev); err != nil {
		return err
	}
	// Always release callback-owned handles, including on errors and late frames.
	defer func() { err = errors.Join(err, release(ev, p.keep()...)) }()
	if panicEvent := ev.GetPanic(); panicEvent != nil {
		return fmt.Errorf("native panic: %v", panicEvent)
	}
	if c := ev.GetConnect(); c != nil {
		r, e := p.resolve(c.GetAsyncId(), "connect")
		if e != nil {
			return e
		}
		if c.GetError() != "" {
			return errors.New(c.GetError())
		}
		r.room = c.GetResult().GetRoom().GetHandle().GetId()
		r.local = c.GetResult().GetLocalParticipant().GetHandle().GetId()
		p.rooms[r.room] = r
		res, e := request(&pb.FfiRequest{Message: &pb.FfiRequest_NewAudioSource{NewAudioSource: &pb.NewAudioSourceRequest{Type: pb.AudioSourceType_AUDIO_SOURCE_NATIVE.Enum(), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), QueueSizeMs: proto.Uint32(200)}}})
		if e != nil {
			return e
		}
		r.source = res.GetNewAudioSource().GetSource().GetHandle().GetId()
		res, e = request(&pb.FfiRequest{Message: &pb.FfiRequest_CreateAudioTrack{CreateAudioTrack: &pb.CreateAudioTrackRequest{Name: proto.String("inprocess-output"), SourceHandle: proto.Uint64(r.source)}}})
		if e != nil {
			return e
		}
		r.output = res.GetCreateAudioTrack().GetTrack().GetHandle().GetId()
		_, e = request(&pb.FfiRequest{Message: &pb.FfiRequest_PublishTrack{PublishTrack: &pb.PublishTrackRequest{LocalParticipantHandle: proto.Uint64(r.local), TrackHandle: proto.Uint64(r.output), Options: &pb.TrackPublishOptions{Source: pb.TrackSource_SOURCE_MICROPHONE.Enum(), Dtx: proto.Bool(false)}, RequestAsyncId: proto.Uint64(p.id(r, "publish"))}}})
		if e != nil {
			return e
		}
		_, e = request(&pb.FfiRequest{Message: &pb.FfiRequest_ReadyForRoomEvent{ReadyForRoomEvent: &pb.ReadyForRoomEventRequest{RoomHandle: proto.Uint64(r.room)}}})
		return e
	}
	if c := ev.GetPublishTrack(); c != nil {
		r, e := p.resolve(c.GetAsyncId(), "publish")
		if e != nil {
			return e
		}
		if c.GetError() != "" {
			return errors.New(c.GetError())
		}
		r.published = true
	}
	if c := ev.GetCaptureAudioFrame(); c != nil {
		r, e := p.resolve(c.GetAsyncId(), "capture")
		if e != nil {
			return e
		}
		if c.GetError() != "" {
			return errors.New(c.GetError())
		}
		r.captures++
	}
	if c := ev.GetDisconnect(); c != nil {
		r, e := p.resolve(c.GetAsyncId(), "disconnect")
		if e != nil {
			return e
		}
		for _, h := range []uint64{r.stream, r.track, r.output, r.source, r.local, r.room} {
			if e = dropProcessHandle(h); e != nil {
				return e
			}
		}
		delete(p.streams, r.stream)
		delete(p.rooms, r.room)
		r.closed = true
	}
	if event := ev.GetRoomEvent(); event != nil {
		r := p.rooms[event.GetRoomHandle()]
		if r == nil || r.closing {
			return nil
		}
		if tr := event.GetTrackSubscribed(); tr != nil && r.stream == 0 {
			r.track = tr.GetTrack().GetHandle().GetId()
			res, e := request(&pb.FfiRequest{Message: &pb.FfiRequest_NewAudioStream{NewAudioStream: &pb.NewAudioStreamRequest{TrackHandle: proto.Uint64(r.track), Type: pb.AudioStreamType_AUDIO_STREAM_NATIVE.Enum(), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), FrameSizeMs: proto.Uint32(20), QueueSizeFrames: proto.Uint32(0)}}})
			if e != nil {
				return e
			}
			r.stream = res.GetNewAudioStream().GetStream().GetHandle().GetId()
			p.streams[r.stream] = r
		}
		if event.GetDisconnected() != nil {
			return errors.New("unexpected room disconnect")
		}
		if failure := event.GetTrackSubscriptionFailed(); failure != nil {
			return errors.New(failure.GetError())
		}
	}
	if audio := ev.GetAudioStreamEvent(); audio != nil && audio.GetFrameReceived() != nil {
		r := p.streams[audio.GetStreamHandle()]
		if r == nil || r.closing {
			return nil
		}
		info := audio.GetFrameReceived().GetFrame().GetInfo()
		n := int(info.GetSamplesPerChannel())
		if info.GetSampleRate() != 24000 || info.GetNumChannels() != 1 || n < 1 || n > 24000 || info.GetDataPtr() == 0 {
			return errors.New("invalid PCM")
		}
		data := C.GoBytes(C.probe_pointer(C.uint64_t(info.GetDataPtr())), C.int(n*2))
		pcm := make([]int16, n)
		for i := range pcm {
			pcm[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
		}
		if r.first == 0 {
			r.first = raw.at
		}
		if r.last != 0 && raw.at-r.last > r.maxGap {
			r.maxGap = raw.at - r.last
		}
		r.last = raw.at
		r.samples += int64(n)
		r.frames++
		if r.onPCM != nil {
			if e := r.onPCM(raw.at, pcm); e != nil {
				return e
			}
		}
		// Echo is test stimulus, not a receive playout policy. Native owns output pacing.
		if r.published {
			memory := C.CBytes(data)
			defer C.free(memory)
			_, e := request(&pb.FfiRequest{Message: &pb.FfiRequest_CaptureAudioFrame{CaptureAudioFrame: &pb.CaptureAudioFrameRequest{SourceHandle: proto.Uint64(r.source), RequestAsyncId: proto.Uint64(p.id(r, "capture")), Buffer: &pb.AudioFrameBufferInfo{DataPtr: proto.Uint64(uint64(uintptr(unsafe.Pointer(memory)))), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), SamplesPerChannel: proto.Uint32(uint32(n))}}}})
			return e // Pinned FFI copies PCM synchronously before returning.
		}
	}
	return nil
}
