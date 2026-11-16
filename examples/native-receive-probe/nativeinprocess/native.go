package nativeinprocess

/*
#cgo linux LDFLAGS: -ldl
#include "bridge.h"
#include <stdlib.h>
*/
import "C"
import (
	"errors"
	"fmt"
	"sync/atomic"
	"unsafe"

	pb "native-receive-probe/ffipb"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
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

//export nipEvent
func nipEvent(p *C.uint8_t, n C.size_t) {
	at := nowNS()

	callbacks.push(unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n)), at)
}
func nowNS() int64 { return int64(C.nip_now()) }
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
func request(req *pb.FfiRequest) (*pb.FfiResponse, error) {
	b, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	var p *C.uint8_t
	var n C.size_t
	h := C.nip_request((*C.uint8_t)(unsafe.Pointer(&b[0])), C.size_t(len(b)), &p, &n)
	if h == 0 {
		return nil, errors.New("native request failed")
	}
	data := C.GoBytes(unsafe.Pointer(p), C.int(n))
	if !bool(C.nip_drop(h)) {
		return nil, errors.New("response handle release failed")
	}
	res := new(pb.FfiResponse)
	err = proto.Unmarshal(data, res)
	return res, err
}

// startNative initializes the process-global FFI; room cycles may share it.
func startNative(pathname string) error {
	path := C.CString(pathname)
	defer C.free(unsafe.Pointer(path))
	if e := C.nip_load(path); e != nil {
		return errors.New(C.GoString(e))
	}
	C.nip_init()
	return nil
}
func disposeNative() { C.nip_dispose() }

func dropHandle(h uint64) error {
	if h != 0 && !bool(C.nip_drop(C.uint64_t(h))) {
		return fmt.Errorf("drop handle %d failed", h)
	}
	return nil
}
func copyPCM(info *pb.AudioFrameBufferInfo) ([]int16, error) {
	n := int(info.GetSamplesPerChannel())
	if info.GetSampleRate() != 24000 || info.GetNumChannels() != 1 || n < 1 || n > 24000 || info.GetDataPtr() == 0 {
		return nil, errors.New("invalid native PCM")
	}
	data := C.GoBytes(C.nip_pointer(C.uint64_t(info.GetDataPtr())), C.int(n*2))
	pcm := make([]int16, n)
	for i := range pcm {
		pcm[i] = int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8)
	}
	return pcm, nil
}
func capture(source, id uint64, pcm []int16) error {
	data := make([]byte, len(pcm)*2)
	for i, v := range pcm {
		data[i*2] = byte(v)
		data[i*2+1] = byte(uint16(v) >> 8)
	}
	ptr := C.CBytes(data)
	defer C.free(ptr)
	_, err := request(&pb.FfiRequest{Message: &pb.FfiRequest_CaptureAudioFrame{CaptureAudioFrame: &pb.CaptureAudioFrameRequest{SourceHandle: proto.Uint64(source), RequestAsyncId: proto.Uint64(id), Buffer: &pb.AudioFrameBufferInfo{DataPtr: proto.Uint64(uint64(uintptr(ptr))), SampleRate: proto.Uint32(24000), NumChannels: proto.Uint32(1), SamplesPerChannel: proto.Uint32(uint32(len(pcm)))}}}})
	return err
}
