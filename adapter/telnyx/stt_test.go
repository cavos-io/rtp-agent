package telnyx

import (
	"context"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
)

func TestTelnyxSTT_Recognize(t *testing.T) {
	mockResponse := `{"text": "Telnyx transcript"}`
	server := testutils.NewJSONMockServer(mockResponse, http.StatusOK)
	defer server.Close()

	s := NewTelnyxSTT("fake-key", 
		WithSTTBaseURL(server.URL),
		WithSTTHTTPClient(server.Client()),
	)

	frames := []*model.AudioFrame{
		{Data: []byte{0x01, 0x02}},
	}

	event, err := s.Recognize(context.Background(), frames, "en")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Errorf("Expected final transcript event, got %v", event.Type)
	}

	if len(event.Alternatives) == 0 || event.Alternatives[0].Text != "Telnyx transcript" {
		t.Errorf("Unexpected transcript: %v", event.Alternatives)
	}
}
