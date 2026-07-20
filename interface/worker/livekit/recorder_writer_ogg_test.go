//go:build !ffmpeg

package livekit

import "testing"

func TestDefaultRecordingFileNameIsOgg(t *testing.T) {
	if RecordingFileName != "audio.ogg" {
		t.Fatalf("RecordingFileName = %q, want audio.ogg", RecordingFileName)
	}
}
