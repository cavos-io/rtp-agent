package clova

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type clovaCloseCountBody struct {
	closed bool
}

func (b *clovaCloseCountBody) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (b *clovaCloseCountBody) Close() error {
	if b.closed {
		return errors.New("already closed")
	}
	b.closed = true
	return nil
}

func TestClovaTTSChunkedStreamDecodesMP3Response(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	stream := &clovaTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader(mp3Data))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("frame data is empty")
	}
	if bytes.HasPrefix(audio.Frame.Data, []byte("ID3")) || bytes.HasPrefix(audio.Frame.Data, []byte{0xff, 0xfb}) {
		t.Fatalf("frame data starts with MP3 container bytes, want decoded PCM")
	}
	if audio.Frame.SampleRate != 48000 || audio.Frame.NumChannels != 2 || audio.Frame.SamplesPerChannel == 0 {
		t.Fatalf("frame shape = rate %d channels %d samples %d, want decoded PCM frame", audio.Frame.SampleRate, audio.Frame.NumChannels, audio.Frame.SamplesPerChannel)
	}
}

func TestClovaTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	stream := &clovaTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader(mp3Data))},
	}
	defer stream.Close()

	sawAudio := false
	for {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next error before final marker = %v", err)
		}
		if audio == nil {
			t.Fatal("Next returned nil audio without error")
		}
		if audio.IsFinal {
			if !sawAudio {
				t.Fatal("final marker arrived before decoded audio")
			}
			if audio.Frame != nil {
				t.Fatalf("final marker frame = %+v, want nil", audio.Frame)
			}
			break
		}
		if audio.Frame != nil && len(audio.Frame.Data) > 0 {
			sawAudio = true
		}
	}

	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestClovaTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	body := &clovaCloseCountBody{}
	stream := &clovaTTSChunkedStream{
		resp: &http.Response{Body: body},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
}

func TestClovaTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &clovaTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader(nil))},
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error before final marker: %v", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("audio = %#v, want boundary-only final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}
