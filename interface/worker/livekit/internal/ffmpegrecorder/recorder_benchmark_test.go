//go:build ffmpeg && cgo

package ffmpegrecorder

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

const benchmarkAudioSeconds = 120

func BenchmarkFFmpegWriterConfigurations(b *testing.B) {
	configs := []struct {
		name       string
		sampleRate int
		bitRate    int
	}{
		{name: "48kHz_128kbps", sampleRate: 48000, bitRate: 128000},
		{name: "24kHz_64kbps", sampleRate: 24000, bitRate: 64000},
	}

	for _, config := range configs {
		b.Run(config.name, func(b *testing.B) {
			pcm := benchmarkSpeechPCM(b, config.sampleRate, 2, benchmarkAudioSeconds)
			dir := b.TempDir()
			var lastPath string

			b.ReportAllocs()
			b.SetBytes(int64(len(pcm) * 2))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lastPath = filepath.Join(dir, fmt.Sprintf("recording-%d.mp4", i))
				writer, err := New(lastPath, config.sampleRate, config.bitRate)
				if err != nil {
					b.Fatalf("New() error = %v", err)
				}
				if _, err := writer.WritePCM(pcm); err != nil {
					b.Fatalf("WritePCM() error = %v", err)
				}
				if err := writer.Close(); err != nil {
					b.Fatalf("Close() error = %v", err)
				}
			}
			b.StopTimer()

			info, err := os.Stat(lastPath)
			if err != nil {
				b.Fatalf("Stat() error = %v", err)
			}
			validateBenchmarkMP4(b, lastPath, config.sampleRate, benchmarkAudioSeconds)
			b.ReportMetric(float64(info.Size()), "encoded-B/op")
			b.ReportMetric(benchmarkAudioSeconds, "audio-s/op")
		})
	}
}

func benchmarkSpeechPCM(tb testing.TB, sampleRate, channels, durationSeconds int) []int16 {
	tb.Helper()
	fixture := filepath.Join("..", "..", "..", "..", "..", "testdata", "audio", "long.mp3")
	command := exec.Command(
		"ffmpeg", "-nostdin", "-v", "error", "-i", fixture,
		"-f", "s16le", "-acodec", "pcm_s16le",
		"-ar", strconv.Itoa(sampleRate), "-ac", strconv.Itoa(channels), "pipe:1",
	)
	raw, err := command.Output()
	if err != nil {
		tb.Fatalf("decode speech fixture: %v", err)
	}
	if len(raw) < 2 || len(raw)%2 != 0 {
		tb.Fatalf("decoded speech fixture has %d bytes", len(raw))
	}

	fixturePCM := make([]int16, len(raw)/2)
	for i := range fixturePCM {
		fixturePCM[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	pcm := make([]int16, sampleRate*channels*durationSeconds)
	for i := range pcm {
		pcm[i] = fixturePCM[i%len(fixturePCM)]
	}
	return pcm
}

func validateBenchmarkMP4(tb testing.TB, path string, sampleRate, durationSeconds int) {
	tb.Helper()
	command := exec.Command(
		"ffprobe", "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name,sample_rate,channels:format=duration",
		"-of", "json", path,
	)
	output, err := command.Output()
	if err != nil {
		tb.Fatalf("ffprobe benchmark output: %v", err)
	}
	var metadata struct {
		Streams []struct {
			CodecName  string `json:"codec_name"`
			SampleRate string `json:"sample_rate"`
			Channels   int    `json:"channels"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		tb.Fatalf("decode ffprobe output: %v", err)
	}
	if len(metadata.Streams) != 1 {
		tb.Fatalf("audio streams = %d, want 1", len(metadata.Streams))
	}
	stream := metadata.Streams[0]
	if stream.CodecName != "aac" || stream.SampleRate != strconv.Itoa(sampleRate) || stream.Channels != 2 {
		tb.Fatalf("audio stream = codec %q, rate %q, channels %d", stream.CodecName, stream.SampleRate, stream.Channels)
	}
	duration, err := strconv.ParseFloat(metadata.Format.Duration, 64)
	if err != nil {
		tb.Fatalf("parse duration %q: %v", metadata.Format.Duration, err)
	}
	if duration < float64(durationSeconds)-0.1 || duration > float64(durationSeconds)+0.2 {
		tb.Fatalf("duration = %.3fs, want approximately %ds", duration, durationSeconds)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("ReadFile() error = %v", err)
	}
	moov := bytes.Index(data, []byte("moov"))
	mdat := bytes.Index(data, []byte("mdat"))
	if moov < 0 || mdat < 0 || moov > mdat {
		tb.Fatalf("MP4 is not fast-start: moov=%d mdat=%d", moov, mdat)
	}
}
