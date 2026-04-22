package openai

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
)

func TestOpenAISTT_Recognize(t *testing.T) {
	// 1. Setup mock REST server
	resp := `{"text": "this is a whisper test"}`
	server := testutils.NewJSONMockServer(resp, 200)
	defer server.Close()

	// 2. Initialize adapter
	adapter := NewOpenAISTT("test-key", "whisper-1", WithBaseURL(server.URL))

	// 3. Run Recognize
	// We need some dummy audio frames
	frames := []*model.AudioFrame{
		{
			Data:        make([]byte, 1000), // Silence
			SampleRate:  16000,
			NumChannels: 1,
		},
	}
	
	event, err := adapter.Recognize(context.Background(), frames, "en")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	// 4. Verify
	if event.Alternatives[0].Text != "this is a whisper test" {
		t.Errorf("expected 'this is a whisper test', got '%s'", event.Alternatives[0].Text)
	}
}
