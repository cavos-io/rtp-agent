//go:build ffmpeg && cgo

package ffmpegrecorder

/*
#cgo pkg-config: libavcodec libavformat libavutil libswresample
#include "recorder.h"
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

var configureLoggingOnce sync.Once

type Writer struct {
	writer    *C.rtp_mp4_writer
	frameSize int
	pending   []int16
	closed    bool
}

func New(outputPath string, sampleRate int) (*Writer, error) {
	configureLoggingOnce.Do(func() {
		C.rtp_mp4_configure_logging()
	})

	path := C.CString(outputPath)
	defer C.free(unsafe.Pointer(path))

	var writer *C.rtp_mp4_writer
	frameSize := C.rtp_mp4_open(path, C.int(sampleRate), &writer)
	if frameSize < 0 {
		return nil, ffmpegError("create MP4/AAC writer", frameSize)
	}
	return &Writer{writer: writer, frameSize: int(frameSize)}, nil
}

func (w *Writer) WritePCM(stereo []int16) (int, error) {
	if w.closed {
		return 0, fmt.Errorf("MP4/AAC writer is closed")
	}
	samples := len(stereo) / 2
	w.pending = append(w.pending, stereo...)
	frameValues := w.frameSize * 2
	for len(w.pending) >= frameValues {
		result := C.rtp_mp4_write(w.writer, (*C.int16_t)(unsafe.Pointer(&w.pending[0])), C.int(w.frameSize))
		if result < 0 {
			return 0, ffmpegError("write MP4/AAC frame", result)
		}
		w.pending = w.pending[frameValues:]
	}
	return samples, nil
}

func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if len(w.pending) > 0 {
		frame := make([]int16, w.frameSize*2)
		copy(frame, w.pending)
		result := C.rtp_mp4_write(w.writer, (*C.int16_t)(unsafe.Pointer(&frame[0])), C.int(w.frameSize))
		if result < 0 {
			C.rtp_mp4_close(w.writer)
			return ffmpegError("write final MP4/AAC frame", result)
		}
	}
	if result := C.rtp_mp4_close(w.writer); result < 0 {
		return ffmpegError("finalize MP4/AAC recording", result)
	}
	return nil
}

func ffmpegError(operation string, code C.int) error {
	buffer := make([]C.char, C.RTP_MP4_ERROR_BUFFER_SIZE)
	C.rtp_mp4_error_string(code, &buffer[0], C.size_t(len(buffer)))
	return fmt.Errorf("%s: %s", operation, C.GoString(&buffer[0]))
}
