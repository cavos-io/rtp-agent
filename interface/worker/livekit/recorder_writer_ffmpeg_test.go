//go:build ffmpeg && cgo

package livekit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFFmpegRecordingWriterCreatesFastStartMP4AAC(t *testing.T) {
	if RecordingFileName != "audio.mp4" {
		t.Fatalf("RecordingFileName = %q, want audio.mp4", RecordingFileName)
	}
	if RecordingSampleRate != 24000 {
		t.Fatalf("RecordingSampleRate = %d, want 24000", RecordingSampleRate)
	}
	path := filepath.Join(t.TempDir(), RecordingFileName)
	writer, err := newRecordingWriter(path, RecordingSampleRate)
	if err != nil {
		t.Fatalf("newRecordingWriter() error = %v", err)
	}
	const durationSeconds = 10
	pcm := make([]int16, RecordingSampleRate*durationSeconds*2)
	for i := 0; i < RecordingSampleRate*durationSeconds; i++ {
		pcm[i*2] = int16((i*997)%65536 - 32768)
		pcm[i*2+1] = -pcm[i*2]
	}
	if written, err := writer.WritePCM(pcm); err != nil || written != RecordingSampleRate*durationSeconds {
		t.Fatalf("WritePCM() = (%d, %v), want (%d, nil)", written, err, RecordingSampleRate*durationSeconds)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	ftyp := bytes.Index(data, []byte("ftyp"))
	moov := bytes.Index(data, []byte("moov"))
	mdat := bytes.Index(data, []byte("mdat"))
	if ftyp < 0 || moov < 0 || mdat < 0 {
		t.Fatalf("MP4 boxes missing: ftyp=%d moov=%d mdat=%d", ftyp, moov, mdat)
	}
	if moov > mdat {
		t.Fatalf("moov box offset %d follows mdat offset %d; faststart not applied", moov, mdat)
	}
	if !bytes.Contains(data, []byte("mp4a")) {
		t.Fatal("MP4 does not contain an AAC mp4a sample entry")
	}
	if len(data) >= 110000 {
		t.Fatalf("10-second 64 kbps AAC recording size = %d bytes, want less than 110000", len(data))
	}
	t.Logf("10-second 24 kHz/64 kbps AAC recording size: %d bytes", len(data))
}
