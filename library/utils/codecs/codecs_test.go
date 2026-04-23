package codecs

import (
	"testing"
)

func TestPCMAudioStreamDecoder(t *testing.T) {
	sampleRate := 16000
	numChannels := 1
	d := NewPCMAudioStreamDecoder(sampleRate, numChannels)
	
	// Frame size for 20ms: 16000 * 0.02 * 1 * 2 = 640 bytes
	frameSize := (sampleRate * 20 / 1000) * numChannels * 2
	data := make([]byte, frameSize+100)
	
	d.Push(data)
	
	frame, err := d.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if len(frame.Data) != frameSize {
		t.Errorf("Expected %d bytes, got %d", frameSize, len(frame.Data))
	}
	
	d.EndInput()
	
	// Should get the remaining 100 bytes
	frame2, err := d.Next()
	if err != nil {
		t.Fatalf("Next failed for remaining: %v", err)
	}
	if len(frame2.Data) != 100 {
		t.Errorf("Expected 100 bytes, got %d", len(frame2.Data))
	}
	
	d.Close()
}

func TestMP3AudioStreamDecoder_ErrorPath(t *testing.T) {
	d := NewMP3AudioStreamDecoder()
	
	// Push garbage and end
	d.Push([]byte("garbage"))
	d.EndInput()
	
	// It should fail initialization because of invalid MP3 data
	_, err := d.Next()
	if err == nil {
		t.Error("Expected error for invalid MP3 data")
	}
	
	d.Close()
}
