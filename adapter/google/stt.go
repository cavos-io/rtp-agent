package google

import (
	"bytes"
	"context"
	"io"
	"strconv"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/cavos-io/conversation-worker/core/stt"
	"github.com/cavos-io/conversation-worker/model"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type GoogleSTT struct {
	client               *speech.Client
	model                string
	punctuate            bool
	spokenPunctuation    bool
	profanityFilter      bool
	sampleRate           int32
	enableWordTimeOffset bool
	enableWordConfidence bool
}

type GoogleSTTOption func(*GoogleSTT)

func WithGoogleSTTModel(model string) GoogleSTTOption {
	return func(s *GoogleSTT) {
		if model != "" {
			s.model = model
		}
	}
}

func WithGoogleSTTPunctuate(punctuate bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.punctuate = punctuate
	}
}

func WithGoogleSTTSpokenPunctuation(spokenPunctuation bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.spokenPunctuation = spokenPunctuation
	}
}

func WithGoogleSTTProfanityFilter(profanityFilter bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.profanityFilter = profanityFilter
	}
}

func WithGoogleSTTSampleRate(sampleRate int32) GoogleSTTOption {
	return func(s *GoogleSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

// NewGoogleSTT creates a new STT client using Application Default Credentials,
// or by providing a path to a credentials JSON file.
func NewGoogleSTT(credentialsFile string, providerOpts ...GoogleSTTOption) (*GoogleSTT, error) {
	ctx := context.Background()
	var clientOpts []option.ClientOption
	if credentialsFile != "" {
		clientOpts = append(clientOpts, option.WithCredentialsFile(credentialsFile))
	}

	client, err := speech.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}

	return newGoogleSTTWithClient(client, providerOpts...), nil
}

func newGoogleSTTWithClient(client *speech.Client, opts ...GoogleSTTOption) *GoogleSTT {
	provider := &GoogleSTT{
		client:               client,
		model:                "latest_long",
		punctuate:            true,
		sampleRate:           16000,
		enableWordTimeOffset: true,
		enableWordConfidence: true,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *GoogleSTT) Label() string { return "google.STT" }
func (s *GoogleSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: "word", OfflineRecognize: true}
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
				Config:         googleRecognitionConfig(s, language),
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
		Config: googleRecognitionConfig(s, language),
		Audio: &speechpb.RecognitionAudio{
			AudioSource: &speechpb.RecognitionAudio_Content{
				Content: buf.Bytes(),
			},
		},
	})

	if err != nil {
		return nil, err
	}

	data := stt.SpeechData{}
	if len(resp.Results) > 0 && len(resp.Results[0].Alternatives) > 0 {
		data = googleSpeechDataFromAlternative(resp.Results[0].Alternatives[0])
	}

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			data,
		},
	}, nil
}

func googleRecognitionConfig(s *GoogleSTT, language string) *speechpb.RecognitionConfig {
	return &speechpb.RecognitionConfig{
		Encoding:                   speechpb.RecognitionConfig_LINEAR16,
		SampleRateHertz:            s.sampleRate,
		LanguageCode:               language,
		EnableWordTimeOffsets:      s.enableWordTimeOffset,
		EnableWordConfidence:       s.enableWordConfidence,
		EnableAutomaticPunctuation: s.punctuate,
		EnableSpokenPunctuation:    wrapperspb.Bool(s.spokenPunctuation),
		ProfanityFilter:            s.profanityFilter,
		Model:                      s.model,
	}
}

func googleSpeechDataFromAlternative(alt *speechpb.SpeechRecognitionAlternative) stt.SpeechData {
	if alt == nil {
		return stt.SpeechData{}
	}

	return stt.SpeechData{
		Text:       alt.GetTranscript(),
		Confidence: float64(alt.GetConfidence()),
		Words:      googleTimedStrings(alt.GetWords()),
	}
}

func googleTimedStrings(words []*speechpb.WordInfo) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}

	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:       word.GetWord(),
			StartTime:  word.GetStartTime().AsDuration().Seconds(),
			EndTime:    word.GetEndTime().AsDuration().Seconds(),
			Confidence: float64(word.GetConfidence()),
			SpeakerID:  googleSpeakerID(word),
		})
	}
	return timed
}

func googleSpeakerID(word *speechpb.WordInfo) string {
	if label := word.GetSpeakerLabel(); label != "" {
		return label
	}
	if tag := word.GetSpeakerTag(); tag != 0 {
		return strconv.Itoa(int(tag))
	}
	return ""
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
					googleSpeechDataFromAlternative(alt),
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
