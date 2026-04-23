package audio

import (
	"reflect"
	"testing"
)

func TestAudioByteStream(t *testing.T) {
	s := NewAudioByteStream(16000, 1, 160) // 10ms frame
	
	data := make([]byte, 320) // One frame
	frames := s.Push(data)
	if len(frames) != 1 {
		t.Errorf("Expected 1 frame, got %d", len(frames))
	}
	
	data2 := make([]byte, 480) // 1.5 frames
	frames2 := s.Push(data2)
	if len(frames2) != 1 {
		t.Errorf("Expected 1 frame from push, got %d", len(frames2))
	}
	
	frames3 := s.Flush()
	if len(frames3) != 1 {
		t.Errorf("Expected 1 frame from flush, got %d", len(frames3))
	}
	if len(frames3[0].Data) != 160 { // Remaining 160 bytes
		t.Errorf("Expected 160 bytes, got %d", len(frames3[0].Data))
	}
}

func TestResampleLinear(t *testing.T) {
	in := []int16{0, 100, 200, 300}
	out := ResampleLinear(in, 16000, 8000)
	if len(out) != 2 {
		t.Errorf("Expected len 2, got %d", len(out))
	}
	if out[0] != 0 || out[1] != 200 {
		t.Errorf("Unexpected resample results: %v", out)
	}
	
	// Upsample
	out2 := ResampleLinear(in, 8000, 16000)
	if len(out2) != 8 {
		t.Errorf("Expected len 8, got %d", len(out2))
	}
}

func TestConvertInt16Bytes(t *testing.T) {
	in := []int16{1, 256, -1}
	bytes := Int16ToBytes(in)
	out := BytesToInt16(bytes)
	
	if !reflect.DeepEqual(in, out) {
		t.Errorf("Conversion failed: %v -> %v", in, out)
	}
}

func TestSumToMono(t *testing.T) {
	stereo := []int16{100, 200, 300, 400} // L, R, L, R
	mono := SumToMono(stereo, 2)
	
	expected := []int16{150, 350}
	if !reflect.DeepEqual(mono, expected) {
		t.Errorf("Expected %v, got %v", expected, mono)
	}
}
