package agent

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/pion/webrtc/v4"
)

func TestBackgroundFrameGainNoopReturnsNil(t *testing.T) {
	if gain := backgroundFrameGain(10, 4, nil, 0, 0, 48000, 1.0); gain != nil {
		t.Fatalf("backgroundFrameGain() = %#v, want nil for no-op", gain)
	}
}

func TestBackgroundAudioOutputCodecUsesStandardOpusChannels(t *testing.T) {
	codec := backgroundAudioOutputCodec()

	if codec.MimeType != webrtc.MimeTypeOpus {
		t.Fatalf("MimeType = %q, want %q", codec.MimeType, webrtc.MimeTypeOpus)
	}
	if codec.ClockRate != 48000 {
		t.Fatalf("ClockRate = %d, want 48000", codec.ClockRate)
	}
	if codec.Channels != 2 {
		t.Fatalf("Channels = %d, want 2 for standard Opus SDP negotiation", codec.Channels)
	}
}

func TestBackgroundFrameGainAppliesVolumeAndEqualPowerFadeIn(t *testing.T) {
	gain := backgroundFrameGain(0, 5, nil, 1.0, 0, 4, 0.5)
	if len(gain) != 5 {
		t.Fatalf("gain length = %d, want 5", len(gain))
	}
	want := []float64{
		0,
		0.5 * math.Sin(math.Pi/8),
		0.5 * math.Sin(math.Pi/4),
		0.5 * math.Sin(3*math.Pi/8),
		0.5,
	}
	for i := range want {
		if math.Abs(gain[i]-want[i]) > 1e-9 {
			t.Fatalf("gain[%d] = %v, want %v; all gain %#v", i, gain[i], want[i], gain)
		}
	}
}

func TestBackgroundFrameGainAppliesEqualPowerFadeOut(t *testing.T) {
	stopSample := 4
	gain := backgroundFrameGain(4, 5, &stopSample, 0, 1.0, 4, 1.0)
	want := []float64{
		1,
		math.Cos(math.Pi / 8),
		math.Cos(math.Pi / 4),
		math.Cos(3 * math.Pi / 8),
		0,
	}
	for i := range want {
		if math.Abs(gain[i]-want[i]) > 1e-9 {
			t.Fatalf("gain[%d] = %v, want %v; all gain %#v", i, gain[i], want[i], gain)
		}
	}
}

func TestApplyBackgroundFrameGainAppliesGainToAllChannels(t *testing.T) {
	frame := backgroundTestFrame(48000, 2, 2, []int16{1000, -1000, 2000, -2000})
	got := applyBackgroundFrameGain(frame, []float64{0.5, 0.25})

	if got == frame {
		t.Fatal("applyBackgroundFrameGain returned original frame, want copied frame when gain is applied")
	}
	gotSamples := backgroundFrameSamples(got)
	want := []int16{500, -500, 500, -500}
	for i := range want {
		if gotSamples[i] != want[i] {
			t.Fatalf("samples = %#v, want %#v", gotSamples, want)
		}
	}
	if original := backgroundFrameSamples(frame); original[0] != 1000 || original[2] != 2000 {
		t.Fatalf("original frame was mutated: %#v", original)
	}
}

func TestApplyBackgroundFrameGainClipsInt16Range(t *testing.T) {
	frame := backgroundTestFrame(48000, 1, 2, []int16{20000, -20000})
	got := applyBackgroundFrameGain(frame, []float64{2, 2})
	samples := backgroundFrameSamples(got)
	if samples[0] != 32767 || samples[1] != -32768 {
		t.Fatalf("clipped samples = %#v, want [32767 -32768]", samples)
	}
}

func TestNormalizeSoundSourcePreservesAudioConfigGainFields(t *testing.T) {
	player := NewBackgroundAudioPlayer(nil, nil)
	src, cfg := player.normalizeSoundSource(AudioConfig{
		Source:      "ambient.ogg",
		Volume:      0.25,
		Probability: 0.75,
		FadeIn:      0.1,
		FadeOut:     0.2,
	})

	if src != "ambient.ogg" || cfg.Source != "ambient.ogg" {
		t.Fatalf("source = %#v / %#v, want ambient.ogg", src, cfg.Source)
	}
	if cfg.Volume != 0.25 || cfg.Probability != 0.75 || cfg.FadeIn != 0.1 || cfg.FadeOut != 0.2 {
		t.Fatalf("cfg = %#v, want preserved volume/probability/fades", cfg)
	}
}

func TestNormalizeSoundSourceAppliesAudioConfigReferenceDefaults(t *testing.T) {
	player := NewBackgroundAudioPlayer(nil, nil)

	src, cfg := player.normalizeSoundSource(AudioConfig{Source: "ambient.ogg"})
	if src != "ambient.ogg" || cfg.Source != "ambient.ogg" {
		t.Fatalf("source = %#v / %#v, want ambient.ogg", src, cfg.Source)
	}
	if cfg.Volume != 1.0 || cfg.Probability != 1.0 {
		t.Fatalf("cfg = %#v, want default volume/probability 1.0", cfg)
	}

	src, cfg = player.normalizeSoundSource([]AudioConfig{{Source: "thinking.ogg"}})
	if src != "thinking.ogg" || cfg.Source != "thinking.ogg" {
		t.Fatalf("list source = %#v / %#v, want thinking.ogg", src, cfg.Source)
	}
	if cfg.Volume != 1.0 || cfg.Probability != 1.0 {
		t.Fatalf("list cfg = %#v, want default volume/probability 1.0", cfg)
	}
}

func TestBackgroundAudioPlayBeforeStartPanicsLikeReference(t *testing.T) {
	player := NewBackgroundAudioPlayer(nil, nil)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Play before Start did not panic, want BackgroundAudio is not started error")
		}
	}()
	player.Play("ambient.ogg", false)
}

func TestBackgroundAudioPlayLoopingChannelPanicsLikeReference(t *testing.T) {
	player := NewBackgroundAudioPlayer(nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	player.mixerTaskCtx = ctx
	player.mixerTaskCancel = cancel

	frames := make(chan *model.AudioFrame)
	defer close(frames)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Play looping channel did not panic, want unsupported looped stream error")
		}
	}()
	player.Play((<-chan *model.AudioFrame)(frames), true)
}

func backgroundTestFrame(sampleRate uint32, channels uint32, samplesPerChannel uint32, samples []int16) *model.AudioFrame {
	data := make([]byte, len(samples)*2)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
	}
	return &model.AudioFrame{
		Data:              data,
		SampleRate:        sampleRate,
		NumChannels:       channels,
		SamplesPerChannel: samplesPerChannel,
	}
}

func backgroundFrameSamples(frame *model.AudioFrame) []int16 {
	if frame == nil {
		return nil
	}
	samples := make([]int16, len(frame.Data)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(frame.Data[i*2:]))
	}
	return samples
}

func TestReadAudioFramesFromFileReadsPCMBackgroundAudio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ambient.pcm")
	data := []byte{
		0x01, 0x00,
		0xff, 0x7f,
		0x00, 0x80,
		0x00, 0x00,
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	frames := readAudioFramesFromFile(path, false, make(chan struct{}))

	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("readAudioFramesFromFile closed without a frame")
		}
		if string(frame.Data) != string(data) {
			t.Fatalf("frame data = %v, want %v", frame.Data, data)
		}
		if frame.SampleRate != 48000 {
			t.Fatalf("frame SampleRate = %d, want 48000", frame.SampleRate)
		}
		if frame.NumChannels != 1 {
			t.Fatalf("frame NumChannels = %d, want 1", frame.NumChannels)
		}
		if frame.SamplesPerChannel != 4 {
			t.Fatalf("frame SamplesPerChannel = %d, want 4", frame.SamplesPerChannel)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PCM frame")
	}
}
