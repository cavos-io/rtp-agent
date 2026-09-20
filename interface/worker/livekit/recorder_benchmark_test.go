//go:build ffmpeg && cgo

package livekit

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/interface/worker/livekit/internal/ffmpegrecorder"
)

const (
	recorderBenchmarkDuration = 120 * time.Second
	recorderBenchmarkFrame    = 20 * time.Millisecond
	recorderBenchmarkTurn     = 5 * time.Second
	recorderBenchmarkFlush    = 2500 * time.Millisecond
)

type recorderBenchmarkChunk struct {
	inFrames  []recordedAudioFrame
	outFrames []recordedAudioFrame
	endTime   time.Time
}

func TestRecorderBenchmarkChunksAlternateFiveSecondTurns(t *testing.T) {
	tests := []struct {
		elapsed time.Duration
		input   bool
	}{
		{elapsed: 0, input: true},
		{elapsed: 4999 * time.Millisecond, input: true},
		{elapsed: 5 * time.Second, input: false},
		{elapsed: 9999 * time.Millisecond, input: false},
		{elapsed: 10 * time.Second, input: true},
	}
	for _, test := range tests {
		if input := recorderBenchmarkInputTurn(test.elapsed); input != test.input {
			t.Fatalf("input turn at %s = %t, want %t", test.elapsed, input, test.input)
		}
	}
}

func BenchmarkRecorderFlushConfigurations(b *testing.B) {
	configs := []struct {
		name       string
		sampleRate int
		bitRate    int
	}{
		{name: "48kHz_128kbps", sampleRate: 48000, bitRate: 128000},
		{name: "24kHz_64kbps", sampleRate: 24000, bitRate: 64000},
	}

	timelineStart := time.Unix(0, 0)
	chunks, inputBytes := recorderBenchmarkChunks(b, timelineStart)
	for _, config := range configs {
		b.Run(config.name, func(b *testing.B) {
			dir := b.TempDir()
			var lastPath string

			b.ReportAllocs()
			b.SetBytes(inputBytes)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lastPath = filepath.Join(dir, fmt.Sprintf("recording-%d.mp4", i))
				writer, err := ffmpegrecorder.New(lastPath, config.sampleRate, config.bitRate)
				if err != nil {
					b.Fatalf("New() error = %v", err)
				}
				recorder := &RecorderIO{writer: writer, timelineStart: &timelineStart}
				for _, chunk := range chunks {
					recorder.inFrames = chunk.inFrames
					recorder.outFrames = chunk.outFrames
					recorder.flush(config.sampleRate, chunk.endTime)
				}
				if err := recorder.recordingError(); err != nil {
					b.Fatalf("recording error = %v", err)
				}
				expectedSamples := int64(config.sampleRate) * int64(recorderBenchmarkDuration/time.Second)
				if recorder.writtenSamples != expectedSamples {
					b.Fatalf("written samples = %d, want %d", recorder.writtenSamples, expectedSamples)
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
			if err := exec.Command("ffprobe", "-v", "error", lastPath).Run(); err != nil {
				b.Fatalf("ffprobe benchmark output: %v", err)
			}
			b.ReportMetric(float64(info.Size()), "encoded-B/op")
			b.ReportMetric(recorderBenchmarkDuration.Seconds(), "audio-s/op")
		})
	}
}

func recorderBenchmarkChunks(tb testing.TB, timelineStart time.Time) ([]recorderBenchmarkChunk, int64) {
	tb.Helper()
	inputSpeech := recorderBenchmarkSpeech(tb, 48000)
	outputSpeech := recorderBenchmarkSpeech(tb, 24000)
	chunkCount := int(recorderBenchmarkDuration / recorderBenchmarkFlush)
	chunks := make([]recorderBenchmarkChunk, chunkCount)
	frames := int(recorderBenchmarkDuration / recorderBenchmarkFrame)
	var inputBytes int64
	for i := 0; i < frames; i++ {
		elapsed := time.Duration(i) * recorderBenchmarkFrame
		receivedAt := timelineStart.Add(elapsed)
		chunk := &chunks[int(elapsed/recorderBenchmarkFlush)]
		if recorderBenchmarkInputTurn(elapsed) {
			frame := recorderBenchmarkFrameFromSpeech(inputSpeech, 48000, i)
			chunk.inFrames = append(chunk.inFrames, recordedAudioFrame{frame: frame, receivedAt: receivedAt})
			inputBytes += int64(len(frame.Data))
		} else {
			frame := recorderBenchmarkFrameFromSpeech(outputSpeech, 24000, i)
			chunk.outFrames = append(chunk.outFrames, recordedAudioFrame{frame: frame, receivedAt: receivedAt})
			inputBytes += int64(len(frame.Data))
		}
	}
	for i := range chunks {
		chunks[i].endTime = timelineStart.Add(time.Duration(i+1) * recorderBenchmarkFlush)
	}
	return chunks, inputBytes
}

func recorderBenchmarkInputTurn(elapsed time.Duration) bool {
	return (elapsed/recorderBenchmarkTurn)%2 == 0
}

func recorderBenchmarkSpeech(tb testing.TB, sampleRate int) []int16 {
	tb.Helper()
	fixture := filepath.Join("..", "..", "..", "testdata", "audio", "long.mp3")
	command := exec.Command(
		"ffmpeg", "-nostdin", "-v", "error", "-i", fixture,
		"-f", "s16le", "-acodec", "pcm_s16le",
		"-ar", strconv.Itoa(sampleRate), "-ac", "1", "pipe:1",
	)
	raw, err := command.Output()
	if err != nil {
		tb.Fatalf("decode speech fixture: %v", err)
	}
	if len(raw) < 2 || len(raw)%2 != 0 {
		tb.Fatalf("decoded speech fixture has %d bytes", len(raw))
	}
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return pcm
}

func recorderBenchmarkFrameFromSpeech(speech []int16, sampleRate, frameIndex int) *model.AudioFrame {
	samples := sampleRate / int(time.Second/recorderBenchmarkFrame)
	data := make([]byte, samples*2)
	start := frameIndex * samples
	for i := 0; i < samples; i++ {
		sample := speech[(start+i)%len(speech)]
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
	}
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        uint32(sampleRate),
		NumChannels:       1,
		SamplesPerChannel: uint32(samples),
	}
}
