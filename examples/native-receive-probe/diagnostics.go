package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Observation only: this timer never drives PCM or RTP delivery.
func startHeartbeat(dir, name string) func() error {
	stop, done := make(chan struct{}), make(chan struct{})
	var ticks [][2]int64
	go func() {
		defer close(done)
		for {
			due := nowNS() + 10_000_000
			timer := time.NewTimer(10 * time.Millisecond)
			select {
			case <-stop:
				timer.Stop()
				return
			case <-timer.C:
				ticks = append(ticks, [2]int64{due, nowNS()})
			}
		}
	}()
	return func() error {
		close(stop)
		<-done
		data, err := json.Marshal(ticks)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, name+"-heartbeat.json"), data, 0600)
	}
}
