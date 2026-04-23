package audio

import (
	"bytes"
	"testing"
)

func TestResample(t *testing.T) {
	// Create 10 samples of 16-bit PCM at 16000Hz
	input := make([]byte, 20)
	for i := 0; i < 10; i++ {
		setSample(input, i, int16(i*1000))
	}

	// Downsample to 8000Hz (should result in 5 samples)
	output := Resample(input, 16000, 8000)
	if len(output) != 10 {
		t.Errorf("Expected 10 bytes (5 samples), got %d", len(output))
	}

	// Upsample to 32000Hz (should result in 20 samples)
	output = Resample(input, 16000, 32000)
	if len(output) != 40 {
		t.Errorf("Expected 40 bytes (20 samples), got %d", len(output))
	}

	// Identical rates should return original
	output = Resample(input, 16000, 16000)
	if !bytes.Equal(input, output) {
		t.Errorf("Expected identical output for identical rates")
	}
}
