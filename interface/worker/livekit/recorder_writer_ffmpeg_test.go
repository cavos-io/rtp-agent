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
	path := filepath.Join(t.TempDir(), RecordingFileName)
	writer, err := newRecordingWriter(path, 48000)
	if err != nil {
		t.Fatalf("newRecordingWriter() error = %v", err)
	}
	pcm := make([]int16, 48000*2)
	for i := 0; i < 48000; i++ {
		pcm[i*2] = int16(i % 2048)
		pcm[i*2+1] = -pcm[i*2]
	}
	if written, err := writer.WritePCM(pcm); err != nil || written != 48000 {
		t.Fatalf("WritePCM() = (%d, %v), want (48000, nil)", written, err)
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
}
