//go:build ffmpeg && cgo

package ffmpegrecorder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriterUsesRequestedBitRate(t *testing.T) {
	const (
		sampleRate      = 24000
		durationSeconds = 10
	)
	pcm := make([]int16, sampleRate*durationSeconds*2)
	for i := 0; i < sampleRate*durationSeconds; i++ {
		pcm[i*2] = int16((i*997)%65536 - 32768)
		pcm[i*2+1] = int16((i*619)%65536 - 32768)
	}

	encode := func(name string, bitRate int) int64 {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		writer, err := New(path, sampleRate, bitRate)
		if err != nil {
			t.Fatalf("New(%d) error = %v", bitRate, err)
		}
		if _, err := writer.WritePCM(pcm); err != nil {
			t.Fatalf("WritePCM(%d) error = %v", bitRate, err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("Close(%d) error = %v", bitRate, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%d) error = %v", bitRate, err)
		}
		return info.Size()
	}

	low := encode("64k.mp4", 64000)
	high := encode("128k.mp4", 128000)
	if high < low*3/2 {
		t.Fatalf("128 kbps output = %d bytes, 64 kbps output = %d bytes; want at least 1.5x larger", high, low)
	}
}
