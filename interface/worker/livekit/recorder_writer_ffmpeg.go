//go:build ffmpeg && cgo

package livekit

import "github.com/cavos-io/rtp-agent/interface/worker/livekit/internal/ffmpegrecorder"

const RecordingFileName = "audio.mp4"

func newRecordingWriter(outputPath string, sampleRate int) (recordingWriter, error) {
	return ffmpegrecorder.New(outputPath, sampleRate)
}
