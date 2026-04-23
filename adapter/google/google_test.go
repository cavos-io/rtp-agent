package google

import (
	"context"
	"io"
	"testing"

	"cloud.google.com/go/speech/apiv1/speechpb"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc/metadata"
)

type mockSpeechClient struct {
	recognizeResp *speechpb.RecognizeResponse
	recognizeErr  error
	stream        speechpb.Speech_StreamingRecognizeClient
	streamErr     error
}

func (m *mockSpeechClient) Recognize(ctx context.Context, req *speechpb.RecognizeRequest, opts ...gax.CallOption) (*speechpb.RecognizeResponse, error) {
	return m.recognizeResp, m.recognizeErr
}

func (m *mockSpeechClient) StreamingRecognize(ctx context.Context, opts ...gax.CallOption) (speechpb.Speech_StreamingRecognizeClient, error) {
	return m.stream, m.streamErr
}

func (m *mockSpeechClient) Close() error { return nil }

type mockStreamingRecognizeClient struct {
	recvResps []*speechpb.StreamingRecognizeResponse
	recvErrs  []error
	recvIdx   int
	sendErr   error
}

func (m *mockStreamingRecognizeClient) Send(req *speechpb.StreamingRecognizeRequest) error {
	return m.sendErr
}

func (m *mockStreamingRecognizeClient) Recv() (*speechpb.StreamingRecognizeResponse, error) {
	if m.recvIdx >= len(m.recvResps) && m.recvIdx >= len(m.recvErrs) {
		return nil, io.EOF
	}
	var resp *speechpb.StreamingRecognizeResponse
	var err error
	if m.recvIdx < len(m.recvResps) {
		resp = m.recvResps[m.recvIdx]
	}
	if m.recvIdx < len(m.recvErrs) {
		err = m.recvErrs[m.recvIdx]
	}
	m.recvIdx++
	return resp, err
}

func (m *mockStreamingRecognizeClient) CloseSend() error             { return nil }
func (m *mockStreamingRecognizeClient) Context() context.Context     { return context.Background() }
func (m *mockStreamingRecognizeClient) Header() (metadata.MD, error) { return nil, nil }
func (m *mockStreamingRecognizeClient) Trailer() metadata.MD         { return nil }
func (m *mockStreamingRecognizeClient) SendMsg(m_ any) error         { return nil }
func (m *mockStreamingRecognizeClient) RecvMsg(m_ any) error         { return nil }

type mockTtsClient struct {
	synthesizeResp *texttospeechpb.SynthesizeSpeechResponse
	synthesizeErr  error
}

func (m *mockTtsClient) SynthesizeSpeech(ctx context.Context, req *texttospeechpb.SynthesizeSpeechRequest, opts ...gax.CallOption) (*texttospeechpb.SynthesizeSpeechResponse, error) {
	return m.synthesizeResp, m.synthesizeErr
}

func (m *mockTtsClient) Close() error { return nil }

func TestGoogleSTT_Recognize(t *testing.T) {
	mock := &mockSpeechClient{
		recognizeResp: &speechpb.RecognizeResponse{
			Results: []*speechpb.SpeechRecognitionResult{
				{
					Alternatives: []*speechpb.SpeechRecognitionAlternative{
						{Transcript: "hello google"},
					},
				},
			},
		},
	}

	s := &GoogleSTT{client: mock}
	res, err := s.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("raw")}}, "en-US")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}
	if res.Alternatives[0].Text != "hello google" {
		t.Errorf("Expected 'hello google', got %q", res.Alternatives[0].Text)
	}
}

func TestGoogleSTT_Stream(t *testing.T) {
	streamMock := &mockStreamingRecognizeClient{
		recvResps: []*speechpb.StreamingRecognizeResponse{
			{
				Results: []*speechpb.StreamingRecognitionResult{
					{
						Alternatives: []*speechpb.SpeechRecognitionAlternative{
							{Transcript: "streaming hello"},
						},
						IsFinal: true,
					},
				},
			},
		},
		recvErrs: []error{nil, io.EOF},
	}
	mock := &mockSpeechClient{
		stream: streamMock,
	}

	s := &GoogleSTT{client: mock}
	stream, err := s.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	_ = stream.PushFrame(&model.AudioFrame{Data: []byte("frame")})
	
	ev, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if ev.Type != stt.SpeechEventFinalTranscript {
		t.Errorf("Expected final transcript, got %v", ev.Type)
	}
	if ev.Alternatives[0].Text != "streaming hello" {
		t.Errorf("Expected 'streaming hello', got %q", ev.Alternatives[0].Text)
	}
}

func TestGoogleTTS_Synthesize(t *testing.T) {
	mock := &mockTtsClient{
		synthesizeResp: &texttospeechpb.SynthesizeSpeechResponse{
			AudioContent: []byte("synthetic google audio"),
		},
	}

	ttsAdapter := &GoogleTTS{
		client: mock,
		voice:  &texttospeechpb.VoiceSelectionParams{LanguageCode: "en-US"},
		audio:  &texttospeechpb.AudioConfig{SampleRateHertz: 24000},
	}
	
	stream, err := ttsAdapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if string(chunk.Frame.Data) != "synthetic google audio" {
		t.Errorf("Expected 'synthetic google audio', got %q", string(chunk.Frame.Data))
	}
}
