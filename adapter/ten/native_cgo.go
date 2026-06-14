//go:build tenvad_native && linux && amd64 && cgo

package ten

/*
#cgo CFLAGS: -I${SRCDIR}/native/linux_amd64/include
#cgo LDFLAGS: -L${SRCDIR}/native/linux_amd64/lib -lten_vad -Wl,-rpath,${SRCDIR}/native/linux_amd64/lib
#include "ten_vad.h"
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
)

type nativeEstimator struct {
	mu      sync.Mutex
	handle  C.ten_vad_handle_t
	hopSize int
	initErr error
	closed  bool
}

type nativeModelResolver struct {
	workingDir string
	cleanupDir string
}

var nativeCreateMu sync.Mutex

func newNativeProbabilityEstimatorFactory(options VADOptions) (vad.ProbabilityEstimatorFactory, error) {
	if options.HopSize <= 0 {
		return nil, errors.New("hop_size must be greater than 0")
	}
	threshold := options.ActivationThreshold
	if !finiteBetweenZeroAndOne(threshold) {
		return nil, errors.New("activation_threshold must be between 0 and 1")
	}
	resolver, err := newNativeModelResolver(options.ModelPath)
	if err != nil {
		return nil, err
	}
	return func() vad.ProbabilityEstimator {
		estimator := resolver.newEstimator(options.HopSize, threshold)
		return estimator.estimate
	}, nil
}

func newNativeModelResolver(modelPath string) (*nativeModelResolver, error) {
	resolver := &nativeModelResolver{}
	if modelPath == "" {
		return resolver, nil
	}
	absoluteModelPath, err := filepath.Abs(modelPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absoluteModelPath); err != nil {
		return nil, err
	}
	if filepath.Base(filepath.Dir(absoluteModelPath)) == "onnx_model" {
		resolver.workingDir = filepath.Dir(filepath.Dir(absoluteModelPath))
		return resolver, nil
	}

	dir, err := os.MkdirTemp("", "rtp-agent-ten-vad-*")
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(dir, "onnx_model"), 0o755); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if err := copyNativeModel(absoluteModelPath, filepath.Join(dir, "onnx_model", "ten-vad.onnx")); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	resolver.workingDir = dir
	resolver.cleanupDir = dir
	runtime.SetFinalizer(resolver, func(resolver *nativeModelResolver) {
		if resolver.cleanupDir != "" {
			os.RemoveAll(resolver.cleanupDir)
		}
	})
	return resolver, nil
}

func copyNativeModel(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func (r *nativeModelResolver) newEstimator(hopSize int, threshold float64) *nativeEstimator {
	if r == nil || r.workingDir == "" {
		return newNativeEstimator(hopSize, threshold)
	}
	nativeCreateMu.Lock()
	defer nativeCreateMu.Unlock()
	currentDir, err := os.Getwd()
	if err != nil {
		return &nativeEstimator{hopSize: hopSize, initErr: err}
	}
	if err := os.Chdir(r.workingDir); err != nil {
		return &nativeEstimator{hopSize: hopSize, initErr: err}
	}
	estimator := newNativeEstimator(hopSize, threshold)
	if err := os.Chdir(currentDir); err != nil && estimator.initErr == nil {
		estimator.initErr = err
	}
	return estimator
}

func newNativeEstimator(hopSize int, threshold float64) *nativeEstimator {
	estimator := &nativeEstimator{hopSize: hopSize}
	if C.ten_vad_create(&estimator.handle, C.size_t(hopSize), C.float(threshold)) != 0 || estimator.handle == nil {
		estimator.initErr = errors.New("ten_vad_create failed")
		return estimator
	}
	runtime.SetFinalizer(estimator, func(estimator *nativeEstimator) {
		estimator.close()
	})
	return estimator
}

func (e *nativeEstimator) estimate(frame *model.AudioFrame) (float64, error) {
	if e == nil {
		return 0, errors.New("TEN VAD estimator nil")
	}
	if e.initErr != nil {
		return 0, e.initErr
	}
	samples, err := frameSamples(frame, e.hopSize)
	if err != nil {
		return 0, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.handle == nil {
		return 0, errors.New("TEN VAD estimator closed")
	}
	var probability C.float
	var flag C.int
	if C.ten_vad_process(e.handle, (*C.int16_t)(unsafe.Pointer(&samples[0])), C.size_t(len(samples)), &probability, &flag) != 0 {
		return 0, errors.New("ten_vad_process failed")
	}
	return float64(probability), nil
}

func (e *nativeEstimator) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.handle == nil {
		return
	}
	handle := e.handle
	_ = C.ten_vad_destroy(&handle)
	e.handle = nil
	e.closed = true
}

func frameSamples(frame *model.AudioFrame, hopSize int) ([]int16, error) {
	if frame == nil {
		return nil, errors.New("TEN VAD frame nil")
	}
	if frame.SampleRate != defaultSampleRate {
		return nil, fmt.Errorf("TEN VAD frame sample rate = %d, want %d", frame.SampleRate, defaultSampleRate)
	}
	if frame.NumChannels == 0 {
		return nil, errors.New("TEN VAD frame channel count zero")
	}
	if int(frame.SamplesPerChannel) != hopSize {
		return nil, fmt.Errorf("TEN VAD frame samples = %d, want %d", frame.SamplesPerChannel, hopSize)
	}
	expectedDataLength := int(frame.SamplesPerChannel) * int(frame.NumChannels) * 2
	if len(frame.Data) != expectedDataLength {
		return nil, errors.New("TEN VAD frame data length mismatch")
	}
	samples := make([]int16, hopSize)
	channels := int(frame.NumChannels)
	for i := 0; i < hopSize; i++ {
		offset := i * channels * 2
		samples[i] = int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2]))
	}
	return samples, nil
}
