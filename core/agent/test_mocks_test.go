package agent

import (
	"context"
	"io"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/model"
)

type mockPublisher struct{}

func (p *mockPublisher) Identity() string { return "mock" }
func (p *mockPublisher) PublishData(data []byte, topic string, destinationSIDs []string) error {
	return nil
}
func (p *mockPublisher) SetAttributes(attrs map[string]string) error { return nil }

type testMockVAD struct{}

func (v *testMockVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	return &testMockVADStream{}, nil
}

type testMockVADStream struct {
	pushed int
}

func (s *testMockVADStream) PushFrame(frame *model.AudioFrame) error {
	s.pushed++
	return nil
}
func (s *testMockVADStream) Flush() error                         { return nil }
func (s *testMockVADStream) Close() error                         { return nil }
func (s *testMockVADStream) Next() (*vad.VADEvent, error)         { return nil, io.EOF }

type testMockSTT struct{}

func (s *testMockSTT) Label() string { return "mock" }
func (s *testMockSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true}
}
func (s *testMockSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return &testMockRecognizeStream{}, nil
}
func (s *testMockSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, nil
}

type testMockRecognizeStream struct {
	pushed int
}

func (s *testMockRecognizeStream) PushFrame(frame *model.AudioFrame) error {
	s.pushed++
	return nil
}
func (s *testMockRecognizeStream) Flush() error                         { return nil }
func (s *testMockRecognizeStream) Close() error                         { return nil }
func (s *testMockRecognizeStream) Next() (*stt.SpeechEvent, error)        { return nil, io.EOF }
