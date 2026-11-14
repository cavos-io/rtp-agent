package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHeartbeatStopsAndWritesTrace(t *testing.T) {
	dir := t.TempDir()
	finish := startHeartbeat(dir, "test")
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "test-heartbeat.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ticks [][2]int64
	if err = json.Unmarshal(data, &ticks); err != nil {
		t.Fatal(err)
	}
	for _, tick := range ticks {
		if tick[1] < tick[0] {
			t.Fatal("heartbeat fired before deadline")
		}
	}
}
