package azure

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
)

func TestAzureSTT_Recognize(t *testing.T) {
	// 1. Setup mock REST server
	resp := `{"DisplayText": "azure stt test", "RecognitionStatus": "Success"}`
	server := testutils.NewJSONMockServer(resp, 200)
	defer server.Close()

	// 2. Initialize adapter
	adapter := NewAzureSTT("test-key", "eastus", WithSTTBaseURL(server.URL))

	// 3. Run Recognize
	frames := []*model.AudioFrame{{Data: []byte{0, 0, 0}}}
	event, err := adapter.Recognize(context.Background(), frames, "en-US")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	// 4. Verify
	if event.Alternatives[0].Text != "azure stt test" {
		t.Errorf("expected 'azure stt test', got '%s'", event.Alternatives[0].Text)
	}
}

func TestAzureTTS_Synthesize(t *testing.T) {
	// 1. Setup mock REST server
	pcmData := []byte{0x01, 0x02, 0x03, 0x04}
	server := testutils.NewJSONMockServer(string(pcmData), 200)
	defer server.Close()

	// 2. Initialize adapter
	adapter := NewAzureTTS("test-key", "eastus", "voice-1", WithTTSBaseURL(server.URL))

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
		t.Errorf("expected 4 bytes, got %d", len(chunk.Frame.Data))
	}
}

func TestAzureLLM_Constructor(t *testing.T) {
	l := NewAzureLLM("test-key", "https://test.openai.azure.com", "gpt-4")
	if l == nil {
		t.Fatal("expected non-nil LLM")
	}
}
