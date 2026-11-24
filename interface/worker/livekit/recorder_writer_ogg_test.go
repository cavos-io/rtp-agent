//go:build !ffmpeg

package livekit

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/hraban/opus"
)

func TestDefaultRecordingFileNameIsOgg(t *testing.T) {
	if RecordingFileName != "audio.ogg" {
		t.Fatalf("RecordingFileName = %q, want audio.ogg", RecordingFileName)
	}
}

// Decode the container, including its pre-skip, rather than just its packets.
func TestOggRecordingPreservesStartup(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "startup.ogg")
			w, err := newRecordingWriter(path, rate)
			if err != nil {
				t.Fatal(err)
			}
			// An 80ms startup tone followed by silence must survive container decoding.
			for frame := 0; frame < 10; frame++ {
				pcm := make([]int16, rate/50*2)
				if frame < 4 {
					for i := 0; i < len(pcm)/2; i++ {
						v := int16(12000 * math.Sin(2*math.Pi*660*float64(frame*(rate/50)+i)/float64(rate)))
						pcm[2*i], pcm[2*i+1] = v, v
					}
				}
				n, err := w.WritePCM(pcm)
				if err != nil || n != rate/50 {
					t.Fatalf("WritePCM = %d, %v", n, err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			stream, err := opus.NewStream(f)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			var decoded []int16
			buf := make([]int16, 5760*2)
			for {
				n, err := stream.Read(buf)
				decoded = append(decoded, buf[:n*2]...)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			// libopusfile always returns 48kHz, regardless of the encoder input rate.
			if len(decoded)/2 != 9600 {
				t.Fatalf("decoded duration = %d samples, want 9600", len(decoded)/2)
			}
			for _, ms := range []int{20, 40, 60} {
				var energy float64
				for i := ms * 48; i < (ms+10)*48; i++ {
					v := float64(decoded[2*i])
					energy += v * v
				}
				if rms := math.Sqrt(energy / 480); rms < 4000 {
					t.Fatalf("startup RMS at %dms = %.1f, want audible tone", ms, rms)
				}
			}
			var energy float64
			for i := 150 * 48; i < 190*48; i++ {
				v := float64(decoded[2*i])
				energy += v * v
			}
			if rms := math.Sqrt(energy / 1920); rms > 200 {
				t.Fatalf("trailing silence RMS = %.1f", rms)
			}
		})
	}
}
