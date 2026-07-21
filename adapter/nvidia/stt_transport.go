package nvidia

import (
	"context"
	"crypto/tls"
	"io"

	rivapb "github.com/cavos-io/rtp-agent/adapter/nvidia/internal/rivapb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type nvidiaSTTClientFactory func(context.Context, *STT) (rivapb.RivaSpeechRecognitionClient, io.Closer, error)

func nvidiaSTTTransportCredentials(useSSL bool) credentials.TransportCredentials {
	if useSSL {
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	return insecure.NewCredentials()
}

func newNvidiaSTTClient(_ context.Context, s *STT) (rivapb.RivaSpeechRecognitionClient, io.Closer, error) {
	conn, err := grpc.NewClient(s.server, grpc.WithTransportCredentials(nvidiaSTTTransportCredentials(s.useSSL)))
	if err != nil {
		return nil, nil, err
	}
	return rivapb.NewRivaSpeechRecognitionClient(conn), conn, nil
}

func nvidiaSTTStreamingConfig(s *STT, language string) *rivapb.StreamingRecognitionConfig {
	cfg := &rivapb.RecognitionConfig{
		Encoding:                   rivapb.AudioEncoding_LINEAR_PCM,
		SampleRateHertz:            int32(s.sampleRate),
		LanguageCode:               language,
		MaxAlternatives:            1,
		AudioChannelCount:          1,
		EnableWordTimeOffsets:      true,
		EnableAutomaticPunctuation: s.punctuate,
		Model:                      s.model,
	}
	if s.diarization {
		cfg.DiarizationConfig = &rivapb.SpeakerDiarizationConfig{
			EnableSpeakerDiarization: true,
			MaxSpeakerCount:          int32(s.maxSpeakerCount),
		}
	}
	return &rivapb.StreamingRecognitionConfig{
		Config:         cfg,
		InterimResults: true,
	}
}

func (s *nvidiaSTTStream) notifyTransportLocked() {
	close(s.transportNotify)
	s.transportNotify = make(chan struct{})
}

func (s *nvidiaSTTStream) enqueueTransportAudioLocked(audio []byte) {
	s.transportAudio = append(s.transportAudio, append([]byte(nil), audio...))
	s.notifyTransportLocked()
}

func (s *nvidiaSTTStream) failTransport(err error) {
	if err == nil || s.transportCtx.Err() != nil {
		return
	}
	s.mu.Lock()
	if s.streamErr == nil && !s.closed {
		s.streamErr = err
		s.transportFinished = true
		s.notifyLocked()
	}
	s.mu.Unlock()
}

func (s *nvidiaSTTStream) runTransport() {
	defer close(s.transportDone)

	client, closer, err := s.stt.clientFactory(s.transportCtx, s.stt)
	if err != nil {
		s.failTransport(err)
		return
	}
	defer closer.Close()

	pairs := []string{"function-id", s.stt.functionID}
	if s.stt.apiKey != "" {
		pairs = append(pairs, "authorization", "Bearer "+s.stt.apiKey)
	}
	ctx := metadata.NewOutgoingContext(s.transportCtx, metadata.Pairs(pairs...))
	rpc, err := client.StreamingRecognize(ctx)
	if err != nil {
		s.failTransport(err)
		return
	}
	if err := rpc.Send(&rivapb.StreamingRecognizeRequest{
		StreamingRequest: &rivapb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: nvidiaSTTStreamingConfig(s.stt, s.language),
		},
	}); err != nil {
		s.failTransport(err)
		return
	}
	receiveDone := make(chan error, 1)
	go func() {
		receiveDone <- s.receiveTransportResponses(rpc)
	}()

	for {
		s.mu.Lock()
		var audio []byte
		if len(s.transportAudio) > 0 {
			audio = s.transportAudio[0]
			s.transportAudio = s.transportAudio[1:]
		}
		eof := s.transportEOF && len(s.transportAudio) == 0
		notify := s.transportNotify
		s.mu.Unlock()

		if audio != nil {
			if err := rpc.Send(&rivapb.StreamingRecognizeRequest{
				StreamingRequest: &rivapb.StreamingRecognizeRequest_AudioContent{
					AudioContent: append([]byte(nil), audio...),
				},
			}); err != nil {
				s.failTransport(err)
				return
			}
			continue
		}
		if eof {
			if err := rpc.CloseSend(); err != nil {
				s.failTransport(err)
				return
			}
			select {
			case <-receiveDone:
			case <-s.transportCtx.Done():
			}
			return
		}
		select {
		case <-notify:
		case <-receiveDone:
			return
		case <-s.transportCtx.Done():
			return
		}
	}
}

func (s *nvidiaSTTStream) receiveTransportResponses(rpc grpc.BidiStreamingClient[
	rivapb.StreamingRecognizeRequest,
	rivapb.StreamingRecognizeResponse,
]) error {
	for {
		response, err := rpc.Recv()
		if err != nil {
			if err == io.EOF {
				s.mu.Lock()
				s.transportFinished = true
				s.notifyLocked()
				s.mu.Unlock()
				return nil
			}
			s.failTransport(err)
			return err
		}

		converted := nvidiaSTTResponse{Results: make([]nvidiaSTTResult, 0, len(response.GetResults()))}
		for _, result := range response.GetResults() {
			if len(result.GetAlternatives()) == 0 {
				continue
			}
			alternative := result.GetAlternatives()[0]
			words := make([]nvidiaSTTWord, 0, len(alternative.GetWords()))
			for _, word := range alternative.GetWords() {
				words = append(words, nvidiaSTTWord{
					Word:       word.GetWord(),
					StartTime:  float64(word.GetStartTime()),
					EndTime:    float64(word.GetEndTime()),
					SpeakerTag: int(word.GetSpeakerTag()),
				})
			}
			converted.Results = append(converted.Results, nvidiaSTTResult{
				IsFinal: result.GetIsFinal(),
				Alternative: nvidiaSTTAlternative{
					Transcript: alternative.GetTranscript(),
					Confidence: float64(alternative.GetConfidence()),
					Words:      words,
				},
			})
		}

		s.mu.Lock()
		s.events = append(s.events, s.eventsFromResponse(converted)...)
		s.notifyLocked()
		s.mu.Unlock()
	}
}
