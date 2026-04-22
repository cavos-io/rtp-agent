package rtzr

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
)

func TestRtzrSTT_Recognize(t *testing.T) {
	server := testutils.NewJSONMockServer(`{"text": "안녕하세요"}`, 200)
	defer server.Close()

	s := NewRtzrSTT("test-key", WithSTTBaseURL(server.URL))
	ev, err := s.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("fake-audio")}}, "ko")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	if ev.Alternatives[0].Text != "안녕하세요" {
		t.Errorf("expected '안녕하세요', got '%s'", ev.Alternatives[0].Text)
	}
}
