//go:build !ffmpeg

package livekit

import "testing"

func TestDefaultRecordingFileNameIsOgg(t *testing.T) {
	if RecordingFileName != "audio.ogg" {
		t.Fatalf("RecordingFileName = %q, want audio.ogg", RecordingFileName)
	}
	if RecordingSampleRate != 48000 {
		t.Fatalf("RecordingSampleRate = %d, want 48000", RecordingSampleRate)
	}
}
