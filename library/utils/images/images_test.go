package images

import (
	"testing"
)

func TestEncode_RGBA(t *testing.T) {
	width, height := 10, 10
	data := make([]byte, width*height*4)
	frame := &VideoFrame{
		Data:   data,
		Width:  width,
		Height: height,
		Format: "rgba",
	}
	
	opts := NewEncodeOptions()
	opts.Format = "png"
	
	res, err := Encode(frame, opts)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(res) == 0 {
		t.Error("Empty result")
	}
}

func TestEncode_RGB24(t *testing.T) {
	width, height := 10, 10
	data := make([]byte, width*height*3)
	frame := &VideoFrame{
		Data:   data,
		Width:  width,
		Height: height,
		Format: "rgb24",
	}
	
	opts := NewEncodeOptions()
	opts.Format = "jpeg"
	
	res, err := Encode(frame, opts)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(res) == 0 {
		t.Error("Empty result")
	}
}

func TestEncode_YUV420P(t *testing.T) {
	width, height := 10, 10
	ySize := width * height
	cSize := (width / 2) * (height / 2)
	data := make([]byte, ySize+2*cSize)
	frame := &VideoFrame{
		Data:   data,
		Width:  width,
		Height: height,
		Format: "yuv420p",
	}
	
	opts := NewEncodeOptions()
	res, err := Encode(frame, opts)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if len(res) == 0 {
		t.Error("Empty result")
	}
}

func TestEncode_Resizing(t *testing.T) {
	width, height := 100, 100
	data := make([]byte, width*height*4)
	frame := &VideoFrame{
		Data:   data,
		Width:  width,
		Height: height,
		Format: "rgba",
	}
	
	strategies := []string{"skew", "center_aspect_fit", "center_aspect_cover", "scale_aspect_cover", "scale_aspect_fit"}
	
	for _, s := range strategies {
		t.Run(s, func(t *testing.T) {
			opts := NewEncodeOptions()
			opts.Width = 50
			opts.Height = 50
			opts.Strategy = s
			
			res, err := Encode(frame, opts)
			if err != nil {
				t.Fatalf("Encode with strategy %s failed: %v", s, err)
			}
			if len(res) == 0 {
				t.Error("Empty result")
			}
		})
	}
}

func TestEncode_UnsupportedFormat(t *testing.T) {
	frame := &VideoFrame{
		Data:   []byte{0, 0, 0, 0},
		Width:  1,
		Height: 1,
		Format: "unknown",
	}
	_, err := Encode(frame, NewEncodeOptions())
	if err == nil {
		t.Error("Expected error for unknown format")
	}
}
