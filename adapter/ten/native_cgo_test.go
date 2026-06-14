//go:build tenvad_native && linux && amd64 && cgo

package ten

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

func TestNativeProbabilityEstimatorProcessesTenFrame(t *testing.T) {
	options := DefaultVADOptions()
	options.ModelPath = copyNativeModelToPluginLayout(t)

	factory, err := newNativeProbabilityEstimatorFactory(options)
	if err != nil {
		t.Fatalf("newNativeProbabilityEstimatorFactory() error = %v", err)
	}
	estimator := factory()
	if estimator == nil {
		t.Fatal("factory() = nil, want estimator")
	}

	probability, err := estimator(&model.AudioFrame{
		Data:              make([]byte, 256*2),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 256,
	})
	if err != nil {
		t.Fatalf("estimator() error = %v", err)
	}
	if probability < 0 || probability > 1 {
		t.Fatalf("probability = %v, want value in [0, 1]", probability)
	}
}

func TestNativeVADStreamsThroughSimpleVAD(t *testing.T) {
	modelPath := copyNativeModelToPluginLayout(t)

	detector, err := NewVADWithOptions(
		WithModelPath(modelPath),
		WithMinSpeechDuration(0.016),
	)
	if err != nil {
		t.Fatalf("NewVADWithOptions() error = %v", err)
	}
	stream, err := detector.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.PushFrame(testAudioFrame(16000, 256, 0)); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	event := nextVADEvent(t, stream)
	if event.Probability < 0 || event.Probability > 1 {
		t.Fatalf("event probability = %v, want value in [0, 1]", event.Probability)
	}
}

func TestNativeReferenceTestsetPrecisionRecall(t *testing.T) {
	options := DefaultVADOptions()
	options.ModelPath = copyNativeModelToPluginLayout(t)
	factory, err := newNativeProbabilityEstimatorFactory(options)
	if err != nil {
		t.Fatalf("newNativeProbabilityEstimatorFactory() error = %v", err)
	}
	estimator := factory()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	wavPath := filepath.Join(wd, "..", "..", "refs", "ten-vad", "testset", "testset-audio-01.wav")
	labelPath := filepath.Join(wd, "..", "..", "refs", "ten-vad", "testset", "testset-audio-01.scv")
	samples, sampleRate := readMonoPCM16WAV(t, wavPath)
	if sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", sampleRate)
	}
	labels := readFramewiseLabels(t, labelPath, 256)
	frameCount := min(len(labels), len(samples)/256)

	var truePositive, falsePositive, falseNegative int
	for frameIndex := 1; frameIndex < frameCount; frameIndex++ {
		data := make([]byte, 256*2)
		frameSamples := samples[frameIndex*256 : (frameIndex+1)*256]
		for i, sample := range frameSamples {
			binary.LittleEndian.PutUint16(data[i*2:i*2+2], uint16(sample))
		}
		probability, err := estimator(&model.AudioFrame{
			Data:              data,
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: 256,
		})
		if err != nil {
			t.Fatalf("estimator(frame %d) error = %v", frameIndex, err)
		}
		predictedSpeech := probability >= 0.5
		referenceSpeech := labels[frameIndex-1]
		switch {
		case predictedSpeech && referenceSpeech:
			truePositive++
		case predictedSpeech && !referenceSpeech:
			falsePositive++
		case !predictedSpeech && referenceSpeech:
			falseNegative++
		}
	}

	precision := float64(truePositive) / float64(truePositive+falsePositive)
	recall := float64(truePositive) / float64(truePositive+falseNegative)
	if precision < 0.75 || recall < 0.75 {
		t.Fatalf("precision/recall = %.3f/%.3f, want both >= 0.75", precision, recall)
	}
}

func copyNativeModelToPluginLayout(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	source := filepath.Join(wd, "..", "..", "refs", "ten-vad", "src", "onnx_model", "ten-vad.onnx")
	model, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", source, err)
	}

	dir := t.TempDir()
	targetDir := filepath.Join(dir, "resources", "models")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	target := filepath.Join(targetDir, "ten-vad.onnx")
	if err := os.WriteFile(target, model, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return target
}

func readMonoPCM16WAV(t *testing.T, path string) ([]int16, int) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("%q is not a RIFF WAVE file", path)
	}

	var channels uint16
	var sampleRate uint32
	var bitsPerSample uint16
	var pcm []byte
	for offset := 12; offset+8 <= len(data); {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		chunkStart := offset + 8
		chunkEnd := chunkStart + chunkSize
		if chunkEnd > len(data) {
			t.Fatalf("%q has truncated %s chunk", path, chunkID)
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				t.Fatalf("%q has short fmt chunk", path)
			}
			format := binary.LittleEndian.Uint16(data[chunkStart : chunkStart+2])
			channels = binary.LittleEndian.Uint16(data[chunkStart+2 : chunkStart+4])
			sampleRate = binary.LittleEndian.Uint32(data[chunkStart+4 : chunkStart+8])
			bitsPerSample = binary.LittleEndian.Uint16(data[chunkStart+14 : chunkStart+16])
			if format != 1 {
				t.Fatalf("%q format = %d, want PCM", path, format)
			}
		case "data":
			pcm = data[chunkStart:chunkEnd]
		}
		offset = chunkEnd
		if offset%2 == 1 {
			offset++
		}
	}
	if channels != 1 || bitsPerSample != 16 {
		t.Fatalf("%q format = %d channels/%d bits, want mono 16-bit", path, channels, bitsPerSample)
	}
	samples := make([]int16, len(pcm)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))
	}
	return samples, int(sampleRate)
}

func readFramewiseLabels(t *testing.T, path string, hopSize int) []bool {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	fields := strings.Split(strings.TrimSpace(string(data)), ",")
	if len(fields) < 4 || (len(fields)-1)%3 != 0 {
		t.Fatalf("%q has invalid scv label format", path)
	}
	frameDuration := float64(hopSize) / 16000
	var labels []bool
	for i := 1; i+2 < len(fields); i += 3 {
		start, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			t.Fatalf("ParseFloat(%q) error = %v", fields[i], err)
		}
		end, err := strconv.ParseFloat(fields[i+1], 64)
		if err != nil {
			t.Fatalf("ParseFloat(%q) error = %v", fields[i+1], err)
		}
		flag, err := strconv.Atoi(fields[i+2])
		if err != nil {
			t.Fatalf("Atoi(%q) error = %v", fields[i+2], err)
		}
		frames := int(math.Round((end - start) / frameDuration))
		for range frames {
			labels = append(labels, flag == 1)
		}
	}
	return labels
}
