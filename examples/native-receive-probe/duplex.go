package main

/*
#include "bridge.h"
#include <stdlib.h>
*/
import "C"

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"google.golang.org/protobuf/proto"
	pb "github.com/cavos-io/rtp-agent/interface/worker/livekit/ffipb"
)

// Explicit prototype protocol. The ordinary receive-only helper never enables it.
type duplexCommand struct {
	Event string `json:"event"`
	ID    uint64 `json:"id"`
	Data  []byte `json:"data,omitempty"`
	err   error
}
type duplexOutput struct {
	commands             <-chan duplexCommand
	sink                 *helperSink
	local, source, track uint64
	published            bool
	lastID               uint64
	pending              map[uint64]bool
}

func (d *duplexOutput) handles() []uint64 { return []uint64{d.local, d.source, d.track} }
func (d *duplexOutput) close() error {
	var err error
	for _, h := range []uint64{d.track, d.source, d.local} {
		if h != 0 && !bool(C.probe_drop(C.uint64_t(h))) {
			err = errors.Join(err, fmt.Errorf("duplex release %d failed", h))
		}
	}
	return err
}
func (d *duplexOutput) connect(local uint64) error {
	d.local = local
	res, err := request(&pb.FfiRequest{Message: &pb.FfiRequest_NewAudioSource{NewAudioSource: &pb.NewAudioSourceRequest{Type: pb.AudioSourceType_AUDIO_SOURCE_NATIVE.Enum(), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), QueueSizeMs: proto.Uint32(200)}}})
	if err != nil {
		return err
	}
	d.source = res.GetNewAudioSource().GetSource().GetHandle().GetId()
	if d.source == 0 {
		return errors.New("missing native source")
	}
	res, err = request(&pb.FfiRequest{Message: &pb.FfiRequest_CreateAudioTrack{CreateAudioTrack: &pb.CreateAudioTrackRequest{Name: proto.String("agent-output"), SourceHandle: proto.Uint64(d.source)}}})
	if err != nil {
		return err
	}
	d.track = res.GetCreateAudioTrack().GetTrack().GetHandle().GetId()
	if d.track == 0 {
		return errors.New("missing native output track")
	}
	_, err = request(&pb.FfiRequest{Message: &pb.FfiRequest_PublishTrack{PublishTrack: &pb.PublishTrackRequest{LocalParticipantHandle: proto.Uint64(local), TrackHandle: proto.Uint64(d.track), Options: &pb.TrackPublishOptions{Source: pb.TrackSource_SOURCE_MICROPHONE.Enum(), Dtx: proto.Bool(false), Red: proto.Bool(false)}, RequestAsyncId: proto.Uint64(3)}}})
	return err
}
func validateDuplexCommand(c duplexCommand) error {
	switch c.Event {
	case "capture":
		if len(c.Data) == 0 || len(c.Data) > 2400 || len(c.Data)%2 != 0 {
			return errors.New("capture requires 1..1200 samples of 24kHz mono PCM16")
		}
	case "clear", "stop":
		if len(c.Data) != 0 {
			return errors.New("control command cannot contain PCM")
		}
	default:
		return errors.New("unknown duplex command")
	}
	if c.ID < 1000 {
		return errors.New("command id must be >=1000")
	}
	return nil
}
func (d *duplexOutput) command(c duplexCommand) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	if err := validateDuplexCommand(c); err != nil {
		return false, err
	}
	if c.ID <= d.lastID {
		return false, errors.New("command ids must increase")
	}
	d.lastID = c.ID
	if c.Event == "stop" {
		return true, nil
	}
	if !d.published {
		return false, errors.New("output is not published")
	}
	switch c.Event {
	case "clear":
		// Never fabricate a playout timeline: native owns this source queue.
		// Require capture completion before clear so no pending capture can refill it.
		if len(d.pending) != 0 {
			return false, errors.New("await capture completion before clear")
		}
		_, err := request(&pb.FfiRequest{Message: &pb.FfiRequest_ClearAudioBuffer{ClearAudioBuffer: &pb.ClearAudioBufferRequest{SourceHandle: proto.Uint64(d.source)}}})
		if err != nil {
			return false, err
		}
		d.sink.event(map[string]any{"event": "cleared", "id": c.ID, "at": nowNS()})
	case "capture":
		if len(d.pending) != 0 {
			return false, errors.New("only one capture may be in flight")
		}
		p := C.CBytes(c.Data)
		// Pinned FFI capture_frame copies with to_vec() before returning the request.
		defer C.free(p)
		_, err := request(&pb.FfiRequest{Message: &pb.FfiRequest_CaptureAudioFrame{CaptureAudioFrame: &pb.CaptureAudioFrameRequest{SourceHandle: proto.Uint64(d.source), RequestAsyncId: proto.Uint64(c.ID), Buffer: &pb.AudioFrameBufferInfo{DataPtr: proto.Uint64(uint64(uintptr(unsafe.Pointer(p)))), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), SamplesPerChannel: proto.Uint32(uint32(len(c.Data) / 2))}}}})
		if err != nil {
			return false, err
		}
		d.pending[c.ID] = true
	}
	return false, d.sink.err
}
func (d *duplexOutput) event(ev *pb.FfiEvent) error {
	if p := ev.GetPublishTrack(); p != nil {
		if p.GetAsyncId() != 3 || p.GetError() != "" {
			return fmt.Errorf("publish callback: %s", p.GetError())
		}
		d.published = true
		d.sink.event(map[string]any{"event": "output_published", "sid": p.GetPublication().GetInfo().GetSid(), "at": nowNS()})
	}
	if c := ev.GetCaptureAudioFrame(); c != nil {
		if !d.pending[c.GetAsyncId()] {
			return errors.New("unexpected capture callback")
		}
		delete(d.pending, c.GetAsyncId())
		if c.GetError() != "" {
			return errors.New(c.GetError())
		}
		d.sink.event(map[string]any{"event": "captured", "id": c.GetAsyncId(), "at": nowNS()})
	}
	return d.sink.err
}
func runDuplexHelper(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 65536)
	if !scanner.Scan() {
		return errors.New("missing duplex configuration")
	}
	var config struct{ URL, Token, Participant, TrackSID string }
	if err := json.Unmarshal(scanner.Bytes(), &config); err != nil {
		return errors.New("invalid duplex configuration")
	}
	if config.URL == "" || config.Token == "" || config.Participant == "" || config.TrackSID == "" {
		return errors.New("incomplete duplex configuration")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	commands := make(chan duplexCommand, 8)
	go func() {
		for scanner.Scan() {
			var c duplexCommand
			if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
				c.err = errors.New("invalid duplex command JSON")
			}
			select {
			case commands <- c:
			case <-runCtx.Done():
				return
			}
		}
		select {
		case commands <- duplexCommand{err: errors.New("duplex control pipe ended")}:
		case <-runCtx.Done():
		}
	}()
	sink := &helperSink{participant: config.Participant, trackSID: config.TrackSID, encoder: json.NewEncoder(os.Stdout)}
	sink.duplex = &duplexOutput{commands: commands, sink: sink, pending: make(map[uint64]bool)}
	sink.event(map[string]any{"event": "duplex_protocol", "version": 2})
	return sink.finish(nativeReceive(runCtx, config.URL, config.Token, sink))
}
