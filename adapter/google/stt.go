package google

import (
	"bytes"
	"context"
	"io"
	"sync"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type GoogleSTT struct {
	mu                   sync.Mutex
	streams              map[*googleSTTStream]struct{}
	client               googleSpeechClient
	model                string
	punctuate            bool
	spokenPunctuation    bool
	profanityFilter      bool
	voiceActivityEvents  bool
	sampleRate           int32
	minConfidence        float64
	enableWordTimeOffset bool
	enableWordConfidence bool
}

type googleSpeechClient interface {
	StreamingRecognize(ctx context.Context, opts ...gax.CallOption) (speechpb.Speech_StreamingRecognizeClient, error)
	Recognize(ctx context.Context, req *speechpb.RecognizeRequest, opts ...gax.CallOption) (*speechpb.RecognizeResponse, error)
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

func WithGoogleSTTVoiceActivityEvents(enabled bool) GoogleSTTOption {
	return func(s *GoogleSTT) {
		s.voiceActivityEvents = enabled
	}
}

func WithGoogleSTTSampleRate(sampleRate int32) GoogleSTTOption {
	return func(s *GoogleSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithGoogleSTTMinConfidenceThreshold(threshold float64) GoogleSTTOption {
	return func(s *GoogleSTT) {
		if threshold >= 0 {
			s.minConfidence = threshold
		}
	}
}

// NewGoogleSTT creates a new STT client using Application Default Credentials,
// or by providing a path to a credentials JSON file.
func NewGoogleSTT(credentialsFile string, providerOpts ...GoogleSTTOption) (*GoogleSTT, error) {
	ctx := context.Background()
	clientOpts, err := googleClientOptionsFromCredentialsFile(credentialsFile)
	if err != nil {
		return nil, err
	}

	client, err := speech.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}

	return newGoogleSTTWithClient(client, providerOpts...), nil
}

func newGoogleSTTWithClient(client googleSpeechClient, opts ...GoogleSTTOption) *GoogleSTT {
	provider := &GoogleSTT{
		streams:              make(map[*googleSTTStream]struct{}),
		client:               client,
		model:                "latest_long",
		punctuate:            true,
		sampleRate:           16000,
		minConfidence:        0.65,
		enableWordTimeOffset: true,
		enableWordConfidence: true,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *GoogleSTT) Label() string           { return "google.STT" }
func (s *GoogleSTT) InputSampleRate() uint32 { return uint32(s.sampleRate) }
func (s *GoogleSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: true, Diarization: false, AlignedTranscript: "word", OfflineRecognize: true}
}

func (s *GoogleSTT) Close() error {
	s.mu.Lock()
	streams := make([]*googleSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = make(map[*googleSTTStream]struct{})
	s.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (s *GoogleSTT) registerStream(stream *googleSTTStream) {
	s.mu.Lock()
	if s.streams == nil {
		s.streams = make(map[*googleSTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
	s.mu.Unlock()
}

func (s *GoogleSTT) unregisterStream(stream *googleSTTStream) {
	s.mu.Lock()
	delete(s.streams, stream)
	s.mu.Unlock()
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
				Config:                    googleRecognitionConfig(s, language),
				InterimResults:            true,
				EnableVoiceActivityEvents: s.voiceActivityEvents,
			},
		},
	})

	if err != nil {
		return nil, err
	}

	gs := &googleSTTStream{
		owner:         s,
		stream:        stream,
		minConfidence: s.minConfidence,
		events:        make(chan *stt.SpeechEvent, 10),
		errCh:         make(chan error, 1),
	}
	s.registerStream(gs)
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

	return &stt.SpeechEvent{
		Type:         stt.SpeechEventFinalTranscript,
		Alternatives: googleSpeechDataFromRecognizeResults(resp.Results),
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

func googleSpeechDataFromRecognizeResults(results []*speechpb.SpeechRecognitionResult) []stt.SpeechData {
	if len(results) == 0 {
		return []stt.SpeechData{}
	}
	var text string
	var confidence float64
	var count int
	var firstAlt *speechpb.SpeechRecognitionAlternative
	for _, result := range results {
		if len(result.GetAlternatives()) == 0 {
			continue
		}
		alt := result.GetAlternatives()[0]
		if firstAlt == nil {
			firstAlt = alt
		}
		text += alt.GetTranscript()
		confidence += float64(alt.GetConfidence())
		count++
	}
	if count == 0 {
		return []stt.SpeechData{}
	}
	return []stt.SpeechData{{
		Text:       text,
		Confidence: confidence / float64(count),
		Words:      googleTimedStrings(firstAlt.GetWords()),
	}}
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
	return word.GetSpeakerLabel()
}

type googleSTTStream struct {
	mu            sync.Mutex
	owner         *GoogleSTT
	stream        speechpb.Speech_StreamingRecognizeClient
	minConfidence float64
	events        chan *stt.SpeechEvent
	errCh         chan error
	closed        bool
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

		switch resp.GetSpeechEventType() {
		case speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_BEGIN:
			s.events <- &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech}
		case speechpb.StreamingRecognizeResponse_SPEECH_ACTIVITY_END:
			s.events <- &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech}
		}

		if data, eventType, ok := googleSpeechDataFromStreamingResults(resp.Results, s.minConfidence); ok {
			s.events <- &stt.SpeechEvent{
				Type:         eventType,
				Alternatives: []stt.SpeechData{data},
			}
		}
	}
}

func googleSpeechDataFromStreamingResults(results []*speechpb.StreamingRecognitionResult, minConfidence float64) (stt.SpeechData, stt.SpeechEventType, bool) {
	if len(results) == 0 {
		return stt.SpeechData{}, "", false
	}
	var text string
	var confidence float64
	var count int
	var firstAlt *speechpb.SpeechRecognitionAlternative
	for _, result := range results {
		if len(result.GetAlternatives()) == 0 {
			continue
		}
		alt := result.GetAlternatives()[0]
		if result.GetIsFinal() {
			return googleSpeechDataFromAlternative(alt), stt.SpeechEventFinalTranscript, true
		}
		if firstAlt == nil {
			firstAlt = alt
		}
		text += alt.GetTranscript()
		confidence += float64(alt.GetConfidence())
		count++
	}
	if count == 0 || text == "" {
		return stt.SpeechData{}, "", false
	}
	confidence = confidence / float64(count)
	if confidence < minConfidence {
		return stt.SpeechData{}, "", false
	}
	return stt.SpeechData{
		Text:       text,
		Confidence: confidence,
		Words:      googleTimedStrings(firstAlt.GetWords()),
	}, stt.SpeechEventInterimTranscript, true
}

func (s *googleSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if err := s.stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
			AudioContent: frame.Data,
		},
	}); err != nil {
		s.closed = true
		_ = s.stream.CloseSend()
		s.unregister()
		return err
	}
	return nil
}

func (s *googleSTTStream) Flush() error {
	return nil
}

func (s *googleSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.stream.CloseSend()
	s.unregister()
	return err
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

func (s *googleSTTStream) unregister() {
	if s.owner != nil {
		s.owner.unregisterStream(s)
	}
}
