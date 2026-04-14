package model

import "time"

type VideoFrame struct {
	Data      []byte
	Width     int
	Height    int
	Timestamp time.Duration
}

type AudioSegmentEnd struct{}