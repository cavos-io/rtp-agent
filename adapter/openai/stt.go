package openai

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/sashabaranov/go-openai"
)

type OpenAISTT struct {
	client *openai.Client
	model  string
	prompt string
}

func NewOpenAISTT(apiKey string, model string) *OpenAISTT {
	return NewOpenAISTTWithBaseURL(apiKey, model, "")
}

func NewOpenAISTTWithBaseURL(apiKey string, model string, baseURL string) *OpenAISTT {
	if model == "" {
		model = openai.Whisper1
	}
	cfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	return &OpenAISTT{
		client: openai.NewClientWithConfig(cfg),
		model:  model,
	}
}

func NewOpenAISTTWithPrompt(apiKey string, model string, baseURL string, prompt string) *OpenAISTT {
	s := NewOpenAISTTWithBaseURL(apiKey, model, baseURL)
	s.prompt = prompt
	return s
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
	logger.Logger.Debugw("Recognizing speech from audio frames", "adapter", "openai-stt")
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

	logger.Logger.Debugw("Sending WAV to OpenAI", "size", wav.Len(), "frames", len(frames), "sampleRate", sampleRate, "channels", numChannels)

	req := openai.AudioRequest{
		Model:    s.model,
		FilePath: "audio.wav",
		Reader:   bytes.NewReader(wav.Bytes()),
		Language: language,
		Prompt:   s.prompt,
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

func encodePCMAsWAV(frames []*model.AudioFrame) ([]byte, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("no audio frames to encode")
	}

	sampleRate := uint32(frames[0].SampleRate)
	if sampleRate == 0 {
		sampleRate = 24000
	}

	numChannels := uint16(frames[0].NumChannels)
	if numChannels == 0 {
		numChannels = 1
	}

	var pcm bytes.Buffer
	for _, f := range frames {
		if len(f.Data) == 0 {
			continue
		}
		if _, err := pcm.Write(f.Data); err != nil {
			return nil, err
		}
	}

	pcmData := pcm.Bytes()
	dataLen := uint32(len(pcmData))
	bitsPerSample := uint16(16)
	blockAlign := numChannels * (bitsPerSample / 8)
	byteRate := sampleRate * uint32(blockAlign)

	var out bytes.Buffer
	if _, err := out.WriteString("RIFF"); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint32(36)+dataLen); err != nil {
		return nil, err
	}
	if _, err := out.WriteString("WAVE"); err != nil {
		return nil, err
	}
	if _, err := out.WriteString("fmt "); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint32(16)); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, uint16(1)); err != nil { // PCM
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, numChannels); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, sampleRate); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, byteRate); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, blockAlign); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, bitsPerSample); err != nil {
		return nil, err
	}
	if _, err := out.WriteString("data"); err != nil {
		return nil, err
	}
	if err := binary.Write(&out, binary.LittleEndian, dataLen); err != nil {
		return nil, err
	}
	if _, err := out.Write(pcmData); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}
