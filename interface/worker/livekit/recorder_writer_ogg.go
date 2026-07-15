//go:build !ffmpeg

package livekit

import (
	"fmt"

	"github.com/hraban/opus"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

const RecordingFileName = "audio.ogg"

type oggRecordingWriter struct {
	writer         *oggwriter.OggWriter
	encoder        *opus.Encoder
	sampleRate     int
	sequenceNumber uint16
	timestamp      uint32
}

func newRecordingWriter(outputPath string, sampleRate int) (recordingWriter, error) {
	writer, err := oggwriter.New(outputPath, uint32(sampleRate), 2)
	if err != nil {
		return nil, fmt.Errorf("failed to create ogg writer: %w", err)
	}
	encoder, err := opus.NewEncoder(sampleRate, 2, opus.AppAudio)
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("failed to create opus encoder: %w", err)
	}
	return &oggRecordingWriter{writer: writer, encoder: encoder, sampleRate: sampleRate}, nil
}

func (w *oggRecordingWriter) WritePCM(stereo []int16) (int, error) {
	chunkSamples := w.sampleRate / 50
	opusBuf := make([]byte, 4000)
	written := 0
	for i := 0; i+chunkSamples <= len(stereo)/2; i += chunkSamples {
		n, err := w.encoder.Encode(stereo[i*2:(i+chunkSamples)*2], opusBuf)
		if err != nil {
			return written, fmt.Errorf("encode opus: %w", err)
		}
		packet := &rtp.Packet{
			Header:  rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: w.sequenceNumber, Timestamp: w.timestamp},
			Payload: opusBuf[:n],
		}
		if err := w.writer.WriteRTP(packet); err != nil {
			return written, fmt.Errorf("write ogg packet: %w", err)
		}
		w.sequenceNumber++
		w.timestamp += uint32(chunkSamples)
		written += chunkSamples
	}
	return written, nil
}

func (w *oggRecordingWriter) Close() error { return w.writer.Close() }
