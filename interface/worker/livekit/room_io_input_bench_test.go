//go:build darwin || linux

package livekit

import (
	"slices"
	"syscall"
	"testing"
	"time"
)

// Run with: go test ./interface/worker/livekit -run '^$' -bench BenchmarkRoomIOInputFiveRooms -benchtime=3x -count=1
// Each operation processes 1 second of 20 ms Opus packets for five rooms, paced in real time.
func BenchmarkRoomIOInputFiveRooms(b *testing.B) {
	const targetRate = 24000
	packet := roomIOTestOpusPacket(b)

	for _, mode := range []struct {
		name       string
		decodeRate uint32
	}{
		{"decode48k_resample24k", 48000},
		{"decode24k", roomIOOpusDecodeRate(targetRate)},
	} {
		b.Run(mode.name, func(b *testing.B) {
			type room struct {
				decoder   *opusDecoder
				converter *roomIOInputConverter
			}
			var rooms [5]room
			for i := range rooms {
				dec, err := newOpusDecoder(int(mode.decodeRate), 1)
				if err != nil {
					b.Fatal(err)
				}
				rooms[i] = room{decoder: dec, converter: newRoomIOInputConverter(targetRate)}
			}

			latencies := make([]time.Duration, 0, b.N*50*len(rooms))
			var before, after syscall.Rusage
			if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for range b.N {
				next := time.Now()
				for range 50 {
					for _, r := range rooms {
						started := time.Now()
						pcm, err := r.decoder.Decode(packet)
						if err != nil {
							b.Fatal(err)
						}
						r.converter.Convert(roomIOInputFrameFromPCM(pcm, mode.decodeRate, 1))
						latencies = append(latencies, time.Since(started))
					}
					next = next.Add(20 * time.Millisecond)
					time.Sleep(time.Until(next))
				}
			}
			b.StopTimer()
			if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
				b.Fatal(err)
			}
			cpuNS := after.Utime.Nano() + after.Stime.Nano() - before.Utime.Nano() - before.Stime.Nano()
			b.ReportMetric(float64(cpuNS)/float64(b.N)/1e6, "cpu-ms/s")
			slices.Sort(latencies)
			b.ReportMetric(float64(latencies[(len(latencies)-1)*50/100])/float64(time.Microsecond), "decode-p50-us")
			b.ReportMetric(float64(latencies[(len(latencies)-1)*95/100])/float64(time.Microsecond), "decode-p95-us")
			b.ReportMetric(float64(latencies[(len(latencies)-1)*99/100])/float64(time.Microsecond), "decode-p99-us")
		})
	}
}
