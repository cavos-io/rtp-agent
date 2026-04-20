package google

import (
	"bytes"
	"context"
	"io"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
	"google.golang.org/api/option"
)

type GoogleSTT struct {
	client *speech.Client
}

// NewGoogleSTT creates a new STT client using Application Default Credentials,
// or by providing a path to a credentials JSON file.
func NewGoogleSTT(credentialsFile string) (*GoogleSTT, error) {
	ctx := context.Background()
	var opts []option.ClientOption
	if credentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsFile))
	}

	client, err := speech.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &GoogleSTT{client: client}, nil
}

func (s *GoogleSTT) Label() string { return "google.STT" }
func (s *GoogleSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: true}
}

func (s *GoogleSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language == "" {
		language = "en-US"
	}

	stream, err := s.client.StreamingRecognize(ctx)
	if err != nil {
		return nil, err
	}

	err = stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				Config: &speechpb.RecognitionConfig{
					Encoding:        speechpb.RecognitionConfig_LINEAR16,
					SampleRateHertz: 16000,
					LanguageCode:    language,
				},
				InterimResults: true,
			},
		},
	})

	if err != nil {
		return nil, err
	}

	gs := &googleSTTStream{
		stream: stream,
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
	}
	go gs.readLoop()

	return gs, nil
}

func (s *GoogleSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if language == "" {
		language = "en-US"
	}

	var buf bytes.Buffer
	for _, f := range frames {
		buf.Write(f.Data)
	}

	resp, err := s.client.Recognize(ctx, &speechpb.RecognizeRequest{
		Config: &speechpb.RecognitionConfig{
			Encoding:        speechpb.RecognitionConfig_LINEAR16,
			SampleRateHertz: 16000,
			LanguageCode:    language,
		},
		Audio: &speechpb.RecognitionAudio{
			AudioSource: &speechpb.RecognitionAudio_Content{
				Content: buf.Bytes(),
			},
		},
	})

	if err != nil {
		return nil, err
	}

	var transcript string
	if len(resp.Results) > 0 && len(resp.Results[0].Alternatives) > 0 {
		transcript = resp.Results[0].Alternatives[0].Transcript
	}

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{Text: transcript},
		},
	}, nil
}

type googleSTTStream struct {
	stream speechpb.Speech_StreamingRecognizeClient
	events chan *stt.SpeechEvent
	errCh  chan error
	closed bool
}

func (s *googleSTTStream) readLoop() {
	defer close(s.events)
	for {
		resp, err := s.stream.Recv()
		if err != nil {
			if err != io.EOF {
				s.errCh <- err
			}
			return
		}

		for _, result := range resp.Results {
			if len(result.Alternatives) == 0 {
				continue
			}

			alt := result.Alternatives[0]
			eventType := stt.SpeechEventInterimTranscript
			if result.IsFinal {
				eventType = stt.SpeechEventFinalTranscript
			}

			s.events <- &stt.SpeechEvent{
				Type: eventType,
				Alternatives: []stt.SpeechData{
					{
						Text:       alt.Transcript,
						Confidence: float64(alt.Confidence),
					},
				},
			}
		}
	}
}

func (s *googleSTTStream) PushFrame(frame *model.AudioFrame) error {
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
			AudioContent: frame.Data,
		},
	})
}

func (s *googleSTTStream) Flush() error {
	return nil
}

func (s *googleSTTStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.stream.CloseSend()
}

func (s *googleSTTStream) Next() (*stt.SpeechEvent, error) {
	select {
	case event, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return event, nil
	case err := <-s.errCh:
		return nil, err
	}
}

