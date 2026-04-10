package openai

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/sashabaranov/go-openai"
)

type OpenAISTT struct {
	client *openai.Client
	model  string
}

func NewOpenAISTT(apiKey string, model string) *OpenAISTT {
	if model == "" {
		model = openai.Whisper1
	}
	return &OpenAISTT{
		client: openai.NewClient(apiKey),
		model:  model,
	}
}

func (s *OpenAISTT) Label() string { return "openai.STT" }
func (s *OpenAISTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: false, InterimResults: false, Diarization: false, OfflineRecognize: true}
}

func (s *OpenAISTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	// OpenAI does not have a native streaming API for STT (Whisper) via standard REST.
	// We'd have to implement chunking ourselves or use another provider.
	return nil, fmt.Errorf("streaming stt not yet implemented for openai")
}

func (s *OpenAISTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("no audio frames")
	}

	// Concatenate frames into a single PCM buffer
	var pcmBuf bytes.Buffer
	for _, f := range frames {
		pcmBuf.Write(f.Data)
	}

	// Get audio params from first frame
	sampleRate := frames[0].SampleRate
	numChannels := uint32(frames[0].NumChannels)
	if numChannels == 0 {
		numChannels = 1
	}
	bitsPerSample := uint32(16)
	pcmData := pcmBuf.Bytes()
	dataSize := uint32(len(pcmData))

	// Build WAV header (44 bytes)
	var wav bytes.Buffer
	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := uint16(numChannels * bitsPerSample / 8)

	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+dataSize)) // file size - 8
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	binary.Write(&wav, binary.LittleEndian, uint32(16)) // chunk size
	binary.Write(&wav, binary.LittleEndian, uint16(1))  // PCM format
	binary.Write(&wav, binary.LittleEndian, uint16(numChannels))
	binary.Write(&wav, binary.LittleEndian, sampleRate)
	binary.Write(&wav, binary.LittleEndian, byteRate)
	binary.Write(&wav, binary.LittleEndian, blockAlign)
	binary.Write(&wav, binary.LittleEndian, uint16(bitsPerSample))
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, dataSize)
	wav.Write(pcmData)

	fmt.Printf("📤 [OpenAI-STT] Sending WAV: %d bytes (%d frames, %dHz, %dch)\n", wav.Len(), len(frames), sampleRate, numChannels)

	req := openai.AudioRequest{
		Model:    s.model,
		FilePath: "audio.wav",
		Reader:   bytes.NewReader(wav.Bytes()),
		Language: language,
	}

	resp, err := s.client.CreateTranscription(ctx, req)
	if err != nil {
		return nil, err
	}

	return &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{
			{
				Text: resp.Text,
			},
		},
	}, nil
}
