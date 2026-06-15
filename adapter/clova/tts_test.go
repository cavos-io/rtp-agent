package clova

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

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
