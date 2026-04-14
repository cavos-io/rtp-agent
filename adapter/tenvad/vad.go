package tenvad

/*
#cgo CFLAGS: -I../../include
#cgo LDFLAGS: -L../../bin -lten_vad
#include "ten_vad.h"
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
	"unsafe"

	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/model"
)

type VADOptions struct {
	HopSize             int
	ActivationThreshold float64
	SampleRate          int
}

func DefaultVADOptions() VADOptions {
	return VADOptions{
		HopSize:             160, // 10ms at 16kHz
		ActivationThreshold: 0.5,
		SampleRate:          16000,
	}
}

type TenVAD struct {
	options VADOptions
}

func NewTenVAD(opts ...func(*VADOptions)) *TenVAD {
	options := DefaultVADOptions()
	for _, opt := range opts {
		opt(&options)
	}

	return &TenVAD{
		options: options,
	}
}

func (v *TenVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	handle := C.ten_vad_create(C.int(v.options.HopSize), C.float(v.options.ActivationThreshold))
	if handle == nil {
		return nil, fmt.Errorf("failed to create ten_vad instance")
	}

	return &tenvadStream{
		ctx:     ctx,
		handle:  handle,
		events:  make(chan *vad.VADEvent, 10),
		options: v.options,
	}, nil
}

type tenvadStream struct {
	ctx     context.Context
	handle  C.ten_vad_handle_t
	events  chan *vad.VADEvent
	options VADOptions

	mu               sync.Mutex
	speaking         bool
	samplesProcessed int
}

func (s *tenvadStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Convert bytes to int16
	srcPcm := make([]int16, len(frame.Data)/2)
	for i := 0; i < len(srcPcm); i++ {
		srcPcm[i] = int16(frame.Data[i*2]) | (int16(frame.Data[i*2+1]) << 8)
	}

	// Resample from 24kHz to 16kHz
	dstPcm := make([]int16, len(srcPcm)*2/3)
	for i := 0; i < len(dstPcm); i++ {
		srcIdx := float64(i) * 1.5
		idx := int(srcIdx)
		if idx+1 < len(srcPcm) {
			frac := srcIdx - float64(idx)
			dstPcm[i] = int16(float64(srcPcm[idx])*(1-frac) + float64(srcPcm[idx+1])*frac)
		} else {
			dstPcm[i] = srcPcm[idx]
		}
	}

	var probability C.float
	var speaking C.bool

	C.ten_vad_process(s.handle, (*C.int16_t)(unsafe.Pointer(&dstPcm[0])), C.int(len(dstPcm)), &probability, &speaking)

	s.samplesProcessed += len(dstPcm)
	timestamp := float64(s.samplesProcessed) / float64(s.options.SampleRate)

	isSpeaking := bool(speaking)

	if isSpeaking && !s.speaking {
		s.speaking = true
		s.events <- &vad.VADEvent{
			Type:      vad.VADEventStartOfSpeech,
			Timestamp: timestamp,
			Speaking:  true,
		}
	} else if !isSpeaking && s.speaking {
		s.speaking = false
		s.events <- &vad.VADEvent{
			Type:      vad.VADEventEndOfSpeech,
			Timestamp: timestamp,
			Speaking:  false,
		}
	}

	s.events <- &vad.VADEvent{
		Type:        vad.VADEventInferenceDone,
		Timestamp:   timestamp,
		Probability: float64(probability),
		Speaking:    s.speaking,
	}

	return nil
}

func (s *tenvadStream) Flush() error {
	return nil
}

func (s *tenvadStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.handle != nil {
		C.ten_vad_destroy(s.handle)
		s.handle = nil
	}
	close(s.events)
	return nil
}

func (s *tenvadStream) Next() (*vad.VADEvent, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case event, ok := <-s.events:
		if !ok {
			return nil, fmt.Errorf("stream closed")
		}
		return event, nil
	}
}
