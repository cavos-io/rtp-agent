package rnnoise

/*
#cgo LDFLAGS: -lrnnoise
#include <rnnoise.h>
*/
import "C"
import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/cavos-io/rtp-agent/model"
)

const (
	// RNNoise expects 480 samples per frame (10ms at 48kHz)
	rnnoiseSamples = 480
)

type RNNoiseOptions struct {
	SampleRate uint32
}

type RNNoiseSuppressor struct {
	state  unsafe.Pointer
	mu     sync.Mutex
	closed bool
	opts   RNNoiseOptions

	// Buffer for remaining samples if input doesn't match 10ms windows
	pcmBuf []float32
}

func NewRNNoiseSuppressor(opts RNNoiseOptions) (*RNNoiseSuppressor, error) {
	state := C.rnnoise_create(nil)
	if state == nil {
		return nil, fmt.Errorf("failed to create rnnoise state")
	}

	return &RNNoiseSuppressor{
		state: unsafe.Pointer(state),
		opts:  opts,
	}, nil
}

func (r *RNNoiseSuppressor) Process(frame *model.AudioFrame) (*model.AudioFrame, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, fmt.Errorf("suppressor closed")
	}

	// RNNoise is hardcoded to 48kHz. If the frame is not 48kHz, 
	// the results will be incorrect.
	if frame.SampleRate != 48000 {
		return nil, fmt.Errorf("RNNoise only supports 48000Hz, got %d", frame.SampleRate)
	}

	// Convert bytes (int16) to float32
	samples := int16BytesToFloat32(frame.Data)
	r.pcmBuf = append(r.pcmBuf, samples...)

	processedSamples := make([]float32, 0, len(r.pcmBuf))
	
	// Process in chunks of 480 samples
	for len(r.pcmBuf) >= rnnoiseSamples {
		chunk := r.pcmBuf[:rnnoiseSamples]
		outChunk := make([]float32, rnnoiseSamples)
		
		C.rnnoise_process_frame((*C.DenoiseState)(r.state), (*C.float)(&outChunk[0]), (*C.float)(&chunk[0]))
		
		processedSamples = append(processedSamples, outChunk...)
		r.pcmBuf = r.pcmBuf[rnnoiseSamples:]
	}

	// Convert back to bytes (int16)
	cleanedData := float32ToInt16Bytes(processedSamples)

	return &model.AudioFrame{
		Data:              cleanedData,
		SampleRate:        frame.SampleRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: uint32(len(processedSamples)),
	}, nil
}

func (r *RNNoiseSuppressor) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true
	C.rnnoise_destroy((*C.DenoiseState)(r.state))
	return nil
}

// Helpers

func int16BytesToFloat32(data []byte) []float32 {
	numSamples := len(data) / 2
	pcm := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		sample := int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8)
		pcm[i] = float32(sample)
	}
	return pcm
}

func float32ToInt16Bytes(pcm []float32) []byte {
	data := make([]byte, len(pcm)*2)
	for i, sample := range pcm {
		// Clamp to int16 range
		if sample > 32767 {
			sample = 32767
		} else if sample < -32768 {
			sample = -32768
		}
		s := int16(sample)
		data[i*2] = byte(s & 0xff)
		data[i*2+1] = byte(s >> 8)
	}
	return data
}
