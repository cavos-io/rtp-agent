package openai

import (
	"context"
	"testing"

	oa "github.com/sashabaranov/go-openai"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestOpenAITTS_Synthesize(t *testing.T) {
	// 1. Setup mock REST server returning raw PCM bytes
	pcmData := []byte{0x00, 0x01, 0x02, 0x03}
	server := testutils.NewJSONMockServer(string(pcmData), 200)
	defer server.Close()

	// 2. Initialize adapter
	adapter := NewOpenAITTS("test-key", oa.TTSModel1, oa.VoiceAlloy, WithBaseURL(server.URL))

	// 3. Run Synthesize
	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	// 4. Verify
	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if len(chunk.Frame.Data) != 4 {
		t.Errorf("expected 4 bytes of audio, got %d", len(chunk.Frame.Data))
	}
}
