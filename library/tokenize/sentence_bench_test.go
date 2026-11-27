//go:build darwin || linux

package tokenize

import (
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Run with: go test ./library/tokenize -run '^$' -bench BenchmarkSplitSentencesFiveStreams -benchtime=3x -count=1
// Each operation processes 1 second of streamed reply text for five streams:
// one word every 50 ms, with the splitter re-run on the growing buffer as
// BufferedTokenStream does for every pushed chunk.
func BenchmarkSplitSentencesFiveStreams(b *testing.B) {
	words := strings.Fields("Baik Pak, tagihan Bapak bulan ini sebesar dua ratus ribu rupiah dan jatuh tempo tanggal lima. Apakah ada yang bisa saya bantu lagi?")

	var buffers [5]string
	position := 0
	latencies := make([]time.Duration, 0, b.N*20*len(buffers))
	var before, after syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		next := time.Now()
		for range 20 {
			if position == len(words) {
				position = 0
				buffers = [5]string{}
			}
			for i := range buffers {
				buffers[i] += words[position] + " "
				started := time.Now()
				SplitSentences(buffers[i], 20, true)
				latencies = append(latencies, time.Since(started))
			}
			position++
			next = next.Add(50 * time.Millisecond)
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
	b.ReportMetric(float64(latencies[(len(latencies)-1)*50/100])/float64(time.Microsecond), "split-p50-us")
	b.ReportMetric(float64(latencies[(len(latencies)-1)*95/100])/float64(time.Microsecond), "split-p95-us")
	b.ReportMetric(float64(latencies[(len(latencies)-1)*99/100])/float64(time.Microsecond), "split-p99-us")
}
