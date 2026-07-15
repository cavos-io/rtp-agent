//go:build ffmpeg && !cgo

package livekit

import "fmt"

const RecordingFileName = "audio.mp4"

func newRecordingWriter(string, int) (recordingWriter, error) {
	return nil, fmt.Errorf("ffmpeg recording requires CGO")
}
