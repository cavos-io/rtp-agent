package agent

import (
	"context"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/model"
)

func TestAudioRecognitionPushAudioFansOutToStreams(t *testing.T) {
	vadStream := &testMockVADStream{}
	sttStream := &testMockRecognizeStream{}
	recog := &AudioRecognition{
		vadStream: vadStream,
		sttStream: sttStream,
	}

	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}
	if err := recog.PushAudio(frame); err != nil {
		t.Fatalf("push audio failed: %v", err)
	}

	if vadStream.pushed != 1 {
		t.Fatalf("expected vad stream push count 1, got %d", vadStream.pushed)
	}
	if sttStream.pushed != 1 {
		t.Fatalf("expected stt stream push count 1, got %d", sttStream.pushed)
	}
}

type noopHooks struct{}

func (noopHooks) OnStartOfSpeech(ev *vad.VADEvent)      {}
func (noopHooks) OnEndOfSpeech(ev *vad.VADEvent)        {}
func (noopHooks) OnInterimTranscript(ev *stt.SpeechEvent) {}
func (noopHooks) OnFinalTranscript(ev *stt.SpeechEvent) {}

type failingVAD struct{}

func (f failingVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	return nil, io.EOF
}

type failingSTT struct{}

func (f failingSTT) Label() string                     { return "failing" }
func (f failingSTT) Capabilities() stt.STTCapabilities { return stt.STTCapabilities{Streaming: true} }
func (f failingSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return nil, io.EOF
}
func (f failingSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return nil, io.EOF
}

func TestAudioRecognitionStartErrorPropagation(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	recog := NewAudioRecognition(session, noopHooks{}, failingSTT{}, failingVAD{})
	if err := recog.Start(context.Background()); err == nil {
		t.Fatalf("expected start to propagate stream initialization error")
	}
}

