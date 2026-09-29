//go:build darwin || linux

package silero

import (
	"math"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	ort "github.com/yalue/onnxruntime_go"
)

// Run with: go test ./adapter/silero -run '^$' -bench BenchmarkSileroFiveRooms -benchtime=3x -count=1
// Each operation processes 1.024 seconds of 32 ms audio windows for five streams.
func BenchmarkSileroFiveRooms(b *testing.B) {
	modelPath := filepath.Join("..", "..", "resources", "models", sileroModelFileName)
	options := DefaultVADOptions()
	options.ONNXFilePath = modelPath

	for _, mode := range []string{"default", "tuned", "rms"} {
		b.Run(mode, func(b *testing.B) {
			if mode != "rms" {
				if err := initializeSileroONNXRuntime(""); err != nil {
					b.Fatal(err)
				}
			}
			var factory vad.ProbabilityEstimatorFactory
			var destroy func() error
			var err error
			sessionMu := &sync.Mutex{}
			if mode == "rms" {
				factory = func() vad.ProbabilityEstimator { return benchmarkRMSEstimate }
			} else if mode == "tuned" {
				factory, destroy, err = newSileroONNXProbabilityEstimatorFactory(options, sessionMu)
			} else {
				var session *ort.DynamicAdvancedSession
				session, err = ort.NewDynamicAdvancedSession(
					modelPath,
					[]string{sileroONNXInputName, sileroONNXStateName, sileroONNXSampleRate},
					[]string{sileroONNXOutputName, sileroONNXStateOutput},
					nil,
				)
				if err == nil {
					factory = func() vad.ProbabilityEstimator {
						return newSileroONNXEstimator(session, sessionMu, options.SampleRate)
					}
					destroy = session.Destroy
				}
			}
			if err != nil {
				b.Fatal(err)
			}
			if destroy != nil {
				defer func() {
					if err := destroy(); err != nil {
						b.Error(err)
					}
				}()
			}

			var streams [5]vad.ProbabilityEstimator
			for i := range streams {
				streams[i] = factory()
			}
			frame := testAudioFrame(16000, 512, 0)
			latencies := make([]time.Duration, 0, b.N*32*len(streams))
			var before, after syscall.Rusage
			if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for range b.N {
				next := time.Now()
				for range 32 {
					for _, estimate := range streams {
						started := time.Now()
						if _, err := estimate(frame); err != nil {
							b.Fatal(err)
						}
						latencies = append(latencies, time.Since(started))
					}
					next = next.Add(32 * time.Millisecond)
					time.Sleep(time.Until(next))
				}
			}
			b.StopTimer()
			if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
				b.Fatal(err)
			}
			cpuNS := after.Utime.Nano() + after.Stime.Nano() - before.Utime.Nano() - before.Stime.Nano()
			b.ReportMetric(float64(cpuNS)/float64(b.N)/1.024e6, "cpu-ms/s")
			slices.Sort(latencies)
			b.ReportMetric(float64(latencies[(len(latencies)-1)*50/100])/float64(time.Microsecond), "infer-p50-us")
			b.ReportMetric(float64(latencies[(len(latencies)-1)*95/100])/float64(time.Microsecond), "infer-p95-us")
			b.ReportMetric(float64(latencies[(len(latencies)-1)*99/100])/float64(time.Microsecond), "infer-p99-us")
		})
	}
}

// Matches the RMS probability calculation used by SimpleVAD without ONNX.
func benchmarkRMSEstimate(frame *model.AudioFrame) (float64, error) {
	if len(frame.Data) == 0 {
		return 0, nil
	}
	var sum float64
	for i := 0; i+1 < len(frame.Data); i += 2 {
		sample := int16(uint16(frame.Data[i]) | uint16(frame.Data[i+1])<<8)
		value := float64(sample) / 32768.0
		sum += value * value
	}
	return math.Sqrt(sum / float64(len(frame.Data)/2)), nil
}
