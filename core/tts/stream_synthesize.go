package tts

import "context"

func SynthesizeWithStream(ctx context.Context, provider TTS, text string) (ChunkedStream, error) {
	stream, err := provider.Stream(ctx)
	if err != nil {
		return nil, err
	}
	if err := stream.PushText(text); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := stream.Flush(); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &chunkedStreamFromSynthesizeStream{stream: stream}, nil
}

type chunkedStreamFromSynthesizeStream struct {
	stream SynthesizeStream
}

func (s *chunkedStreamFromSynthesizeStream) Next() (*SynthesizedAudio, error) {
	return s.stream.Next()
}

func (s *chunkedStreamFromSynthesizeStream) Close() error {
	return s.stream.Close()
}
