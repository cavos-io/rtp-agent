package speechmatics

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/tts"
)

type SpeechmaticsTTS struct {
	apiKey string
}

func NewSpeechmaticsTTS(apiKey string) *SpeechmaticsTTS {
	return &SpeechmaticsTTS{
		apiKey: apiKey,
	}
}

func (t *SpeechmaticsTTS) Label() string { return "speechmatics.TTS" }
func (t *SpeechmaticsTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: false, AlignedTranscript: false}
}
func (t *SpeechmaticsTTS) SampleRate() int { return 16000 }
func (t *SpeechmaticsTTS) NumChannels() int { return 1 }

func (t *SpeechmaticsTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	// Currently, Speechmatics primarily focuses on ASR (STT). 
	// They do not have a public TTS API in widespread use for this pattern,
	// so returning an unsupported error.
	return nil, fmt.Errorf("speechmatics tts is unsupported")
}

func (t *SpeechmaticsTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return nil, fmt.Errorf("speechmatics streaming tts is unsupported")
}

