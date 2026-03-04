package google

import (
	"context"
	"fmt"
	"io"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/cavos-io/conversation-worker/core/tts"
	"github.com/cavos-io/conversation-worker/model"
	"google.golang.org/api/option"
)

type GoogleTTS struct {
	client *texttospeech.Client
	voice  *texttospeechpb.VoiceSelectionParams
	audio  *texttospeechpb.AudioConfig
}

// NewGoogleTTS creates a new TTS client using Application Default Credentials,
// or by providing a path to a credentials JSON file.
func NewGoogleTTS(credentialsFile string) (*GoogleTTS, error) {
	ctx := context.Background()
	var opts []option.ClientOption
	if credentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsFile))
	}

	client, err := texttospeech.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &GoogleTTS{
		client: client,
		voice: &texttospeechpb.VoiceSelectionParams{
			LanguageCode: "en-US",
			Name:         "en-US-Journey-F",
		},
		audio: &texttospeechpb.AudioConfig{
			AudioEncoding:   texttospeechpb.AudioEncoding_LINEAR16,
			SampleRateHertz: 24000,
		},
	}, nil
}

func (t *GoogleTTS) Label() string { return "google.TTS" }
func (t *GoogleTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *GoogleTTS) SampleRate() int { return 24000 }
func (t *GoogleTTS) NumChannels() int { return 1 }

func (t *GoogleTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{Text: text},
		},
		Voice:       t.voice,
		AudioConfig: t.audio,
	}

	resp, err := t.client.SynthesizeSpeech(ctx, req)
	if err != nil {
		return nil, err
	}

	return &googleTTSChunkedStream{
		data: resp.AudioContent,
	}, nil
}

func (t *GoogleTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	// Google Cloud TTS doesn't support a streaming input API natively in the same way as STT.
	// You provide text and it streams audio back via StreamingSynthesize, 
	// but the text is sent in one go per request.
	return nil, fmt.Errorf("streaming tts input not yet implemented for google cloud")
}

type googleTTSChunkedStream struct {
	data   []byte
	offset int
	headerStripped bool
}

func (s *googleTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if !s.headerStripped && len(s.data) > 44 {
		// Google TTS LINEAR16 usually returns a WAV with a 44-byte header.
		// Verify RIFF and WAVE tags.
		if string(s.data[0:4]) == "RIFF" && string(s.data[8:12]) == "WAVE" {
			s.data = s.data[44:]
		}
		s.headerStripped = true
	}

	if s.offset >= len(s.data) {
		return nil, io.EOF
	}

	chunkSize := 4096
	end := s.offset + chunkSize
	if end > len(s.data) {
		end = len(s.data)
	}

	chunk := s.data[s.offset:end]
	s.offset = end

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              chunk,
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: uint32(len(chunk) / 2),
		},
	}, nil
}

func (s *googleTTSChunkedStream) Close() error {
	return nil
}
