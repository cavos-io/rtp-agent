package main

/*
#cgo linux LDFLAGS: -ldl
#include "bridge.h"
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	model "github.com/cavos-io/rtp-agent/core/audio/model"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	pb "native-receive-probe/internal/ffipb"
)

type callbackEvent struct {
	data []byte
	at   int64
}
type eventQueue struct {
	events   chan callbackEvent
	overflow atomic.Bool
}

func newEventQueue(n int) *eventQueue { return &eventQueue{events: make(chan callbackEvent, n)} }
func (q *eventQueue) push(p []byte, at int64) {
	e := callbackEvent{append([]byte(nil), p...), at}
	select {
	case q.events <- e:
	default:
		q.overflow.Store(true)
	}
}

// One FFI runtime per process, like the reference SDK. Created before initialize;
// queue is never closed, including while dispose drains native callbacks.
var callbacks = newEventQueue(4096)

//export goEvent
func goEvent(p *C.uint8_t, n C.size_t) {
	at := nowNS()
	callbacks.push(unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n)), at)
}
func nowNS() int64 { return int64(C.probe_now()) }

func ownedHandles(m protoreflect.Message) []uint64 {
	var ids []uint64
	if m.Descriptor().FullName() == "livekit.proto.FfiOwnedHandle" {
		return []uint64{m.Get(m.Descriptor().Fields().ByName("id")).Uint()}
	}
	m.Range(func(f protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if f.Kind() != protoreflect.MessageKind {
			return true
		}
		if f.IsMap() {
			if f.MapValue().Kind() == protoreflect.MessageKind {
				v.Map().Range(func(_ protoreflect.MapKey, item protoreflect.Value) bool {
					ids = append(ids, ownedHandles(item.Message())...)
					return true
				})
			}
		} else if f.IsList() {
			for i := 0; i < v.List().Len(); i++ {
				ids = append(ids, ownedHandles(v.List().Get(i).Message())...)
			}
		} else {
			ids = append(ids, ownedHandles(v.Message())...)
		}
		return true
	})
	return ids
}
func release(m proto.Message, keep ...uint64) error {
	for _, id := range ownedHandles(m.ProtoReflect()) {
		retained := false
		for _, k := range keep {
			if id == k {
				retained = true
			}
		}
		if id != 0 && !retained && !bool(C.probe_drop(C.uint64_t(id))) {
			return fmt.Errorf("drop handle %d failed", id)
		}
	}
	return nil
}
func request(req *pb.FfiRequest) (*pb.FfiResponse, error) {
	b, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	var p *C.uint8_t
	var n C.size_t
	h := C.probe_request((*C.uint8_t)(unsafe.Pointer(&b[0])), C.size_t(len(b)), &p, &n)
	if h == 0 {
		return nil, errors.New("native request failed")
	}
	data := C.GoBytes(unsafe.Pointer(p), C.int(n))
	C.probe_drop(h)
	res := new(pb.FfiResponse)
	err = proto.Unmarshal(data, res)
	return res, err
}
func nativeReceive(ctx context.Context, url, token string, sink *recorder) (err error) {
	path := C.CString(os.Getenv("LIVEKIT_LIB_PATH"))
	defer C.free(unsafe.Pointer(path))
	if e := C.probe_load(path); e != nil {
		return errors.New(C.GoString(e))
	}
	baselineGoroutines := runtime.NumGoroutine()
	C.probe_init()
	var room, stream, track uint64
	defer func() {
		start := nowNS()
		releasesOK := true
		if stream != 0 {
			releasesOK = bool(C.probe_drop(C.uint64_t(stream))) && releasesOK
		}
		disconnected := room == 0
		if room != 0 {
			_, e := request(&pb.FfiRequest{Message: &pb.FfiRequest_Disconnect{Disconnect: &pb.DisconnectRequest{RoomHandle: proto.Uint64(room), RequestAsyncId: proto.Uint64(2)}}})
			if e != nil && err == nil {
				err = e
			}
			deadline := time.NewTimer(1500 * time.Millisecond)
		wait:
			for !disconnected {
				select {
				case raw := <-callbacks.events:
					ev := new(pb.FfiEvent)
					if e := proto.Unmarshal(raw.data, ev); e != nil {
						if err == nil {
							err = e
						}
						continue
					}
					sink.event(map[string]any{"event": "shutdown_callback", "type": fmt.Sprintf("%T", ev.Message), "async_id": ev.GetDisconnect().GetAsyncId()})
					if d := ev.GetDisconnect(); d != nil && d.GetAsyncId() == 2 {
						disconnected = true
					}
					if e := release(ev, room, track); e != nil && err == nil {
						err = e
					}
				case <-deadline.C:
					break wait
				}
			}
			deadline.Stop()
			if track != 0 {
				releasesOK = bool(C.probe_drop(C.uint64_t(track))) && releasesOK
			}
			releasesOK = bool(C.probe_drop(C.uint64_t(room))) && releasesOK
		}
		// Also destroys handles from queued callbacks after disconnect or overflow.
		C.probe_dispose()
		if !releasesOK && err == nil {
			err = errors.New("retained handle release failed")
		}
		if !disconnected && err == nil {
			err = errors.New("disconnect callback timed out")
		}
		if callbacks.overflow.Load() && err == nil {
			err = errors.New("native callback queue overflow")
		}
		sink.event(map[string]any{"event": "shutdown", "at": nowNS(), "duration_ns": nowNS() - start, "disconnect_callback": disconnected, "overflow": callbacks.overflow.Load(), "retained_handles_released": releasesOK, "go_goroutines_before": baselineGoroutines, "go_goroutines_after": runtime.NumGoroutine()})
	}()
	sink.event(map[string]any{"event": "connect_start", "at": nowNS()})
	_, err = request(&pb.FfiRequest{Message: &pb.FfiRequest_Connect{Connect: &pb.ConnectRequest{Url: proto.String(url), Token: proto.String(token), Options: &pb.RoomOptions{AutoSubscribe: proto.Bool(true)}, RequestAsyncId: proto.Uint64(1)}}})
	if err != nil {
		return err
	}
	for {
		if callbacks.overflow.Load() {
			return errors.New("native callback queue overflow")
		}
		select {
		case <-ctx.Done():
			return nil
		case raw := <-callbacks.events:
			ev := new(pb.FfiEvent)
			if err = proto.Unmarshal(raw.data, ev); err != nil {
				return err
			}
			if c := ev.GetConnect(); c != nil {
				if c.GetAsyncId() != 1 {
					return errors.New("unexpected connect id")
				}
				if c.GetError() != "" {
					return errors.New(c.GetError())
				}
				room = c.GetResult().GetRoom().GetHandle().GetId()
				if _, e := request(&pb.FfiRequest{Message: &pb.FfiRequest_ReadyForRoomEvent{ReadyForRoomEvent: &pb.ReadyForRoomEventRequest{RoomHandle: proto.Uint64(room)}}}); e != nil {
					return e
				}
				sink.event(map[string]any{"event": "ready", "at": raw.at})
			}
			if r := ev.GetRoomEvent(); r != nil {
				if fail := r.GetTrackSubscriptionFailed(); fail != nil {
					return errors.New(fail.GetError())
				}
				if tr := r.GetTrackSubscribed(); tr != nil && stream == 0 {
					track = tr.GetTrack().GetHandle().GetId()
					res, e := request(&pb.FfiRequest{Message: &pb.FfiRequest_NewAudioStream{NewAudioStream: &pb.NewAudioStreamRequest{TrackHandle: proto.Uint64(tr.GetTrack().GetHandle().GetId()), Type: pb.AudioStreamType_AUDIO_STREAM_NATIVE.Enum(), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), FrameSizeMs: proto.Uint32(50), QueueSizeFrames: proto.Uint32(0)}}})
					if e != nil {
						return e
					}
					stream = res.GetNewAudioStream().GetStream().GetHandle().GetId()
					if stream == 0 {
						return errors.New("missing audio stream handle")
					}
					sink.event(map[string]any{"event": "subscribed", "at": raw.at})
				}
				if r.GetDisconnected() != nil {
					return errors.New("unexpected room disconnect")
				}
			}
			if a := ev.GetAudioStreamEvent(); a != nil && a.GetFrameReceived() != nil {
				info := a.GetFrameReceived().GetFrame().GetInfo()
				size := uint64(info.GetSamplesPerChannel()) * uint64(info.GetNumChannels()) * 2
				if size > 1024*1024 || info.GetDataPtr() == 0 {
					return errors.New("invalid native PCM buffer")
				}
				frame := &model.AudioFrame{Data: C.GoBytes(C.probe_pointer(C.uint64_t(info.GetDataPtr())), C.int(size)), SampleRate: info.GetSampleRate(), NumChannels: info.GetNumChannels(), SamplesPerChannel: info.GetSamplesPerChannel()}
				if err = sink.frame(raw.at, frame); err != nil {
					return err
				}
			}
			if p := ev.GetPanic(); p != nil {
				return fmt.Errorf("native panic: %v", p)
			}
			if err = release(ev, room, track); err != nil {
				return err
			}
		}
	}
}
