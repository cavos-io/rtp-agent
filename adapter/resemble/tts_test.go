package resemble

import (
	"context"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestResembleTTS_Synthesize(t *testing.T) {
	mockAudio := []byte{0x01, 0x02, 0x03, 0x04}
	server := testutils.NewJSONMockServer(string(mockAudio), http.StatusOK)
	defer server.Close()

	tts := NewResembleTTS("fake-key", "voice-1",
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)

	stream, err := tts.Synthesize(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if len(chunk.Frame.Data) != 4 {
		t.Errorf("Expected 4 bytes, got %d", len(chunk.Frame.Data))
	}
}
