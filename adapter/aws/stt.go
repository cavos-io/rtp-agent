package aws

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
)

type AWSSTT struct {
	client *transcribestreaming.Client
}

func NewAWSSTT(ctx context.Context, region string) (*AWSSTT, error) {
	opts := []func(*config.LoadOptions) error{}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &AWSSTT{
		client: transcribestreaming.NewFromConfig(cfg),
	}, nil
}

func (s *AWSSTT) Label() string { return "aws.STT" }
func (s *AWSSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, OfflineRecognize: false}
}

func (s *AWSSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if language == "" {
		language = "en-US"
	}
	langCode := types.LanguageCode(language)

	out, err := s.client.StartStreamTranscription(ctx, &transcribestreaming.StartStreamTranscriptionInput{
		LanguageCode:         langCode,
		MediaEncoding:        types.MediaEncodingPcm,
		MediaSampleRateHertz: aws.Int32(16000),
	})
	if err != nil {
		return nil, err
	}

	gs := &awsSTTStream{
		stream: out.GetStream(),
		events: make(chan *stt.SpeechEvent, 10),
		errCh:  make(chan error, 1),
	}
	go gs.readLoop()

	return gs, nil
}

func (s *AWSSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	// AWS Transcribe (non-streaming) uses jobs on S3. Since we don't have S3 upload configured,
	// offline recognize is unsupported natively via simple buffer upload.
	return nil, fmt.Errorf("offline recognize is not natively supported by AWSSTT via simple upload (S3 required). Use Stream instead")
}

type awsSTTStream struct {
	stream *transcribestreaming.StartStreamTranscriptionEventStream
	events chan *stt.SpeechEvent
	errCh  chan error
	closed bool
}

func (s *awsSTTStream) readLoop() {
	defer close(s.events)
	for {
		event := <-s.stream.Events()
		if event == nil {
			if err := s.stream.Err(); err != nil {
				if err != io.EOF {
					s.errCh <- err
				}
			}
			return
		}

		switch v := event.(type) {
		case *types.TranscriptResultStreamMemberTranscriptEvent:
			for _, result := range v.Value.Transcript.Results {
				if len(result.Alternatives) == 0 {
					continue
				}

				alt := result.Alternatives[0]
				eventType := stt.SpeechEventInterimTranscript
				if !result.IsPartial {
					eventType = stt.SpeechEventFinalTranscript
				}

				s.events <- &stt.SpeechEvent{
					Type: eventType,
					Alternatives: []stt.SpeechData{
						{
							Text:       aws.ToString(alt.Transcript),
							Confidence: 1.0, // Confidence not uniformly provided at top level
						},
					},
				}
			}
		}
	}
}

func (s *awsSTTStream) PushFrame(frame *model.AudioFrame) error {
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.stream.Send(context.Background(), &types.AudioStreamMemberAudioEvent{
		Value: types.AudioEvent{
			AudioChunk: frame.Data,
		},
	})
}

func (s *awsSTTStream) Flush() error {
	return nil
}

func (s *awsSTTStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.stream.Close()
}

func (s *awsSTTStream) Next() (*stt.SpeechEvent, error) {
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
