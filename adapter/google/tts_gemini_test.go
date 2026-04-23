package google

import (
	"bytes"
	"io"
	"testing"

	"google.golang.org/genai"
)

func TestExtractGeminiAudio(t *testing.T) {
	audio := []byte{1, 2, 3, 4}
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{InlineData: &genai.Blob{MIMEType: "audio/wav", Data: audio}},
					},
				},
			},
		},
	}

	got, mimeType, err := extractGeminiAudio(resp)
	if err != nil {
		t.Fatalf("extractGeminiAudio() error = %v", err)
	}
	if mimeType != "audio/wav" {
		t.Fatalf("mimeType = %q, want audio/wav", mimeType)
	}
	if !bytes.Equal(got, audio) {
		t.Fatalf("audio = %v, want %v", got, audio)
	}
}

func TestExtractGeminiAudio_NoAudio(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}}}},
	}

	_, _, err := extractGeminiAudio(resp)
	if err == nil {
		t.Fatal("expected error when response has no audio part")
	}
}

func TestGeminiTTSChunkedStream_StripsWAVHeader(t *testing.T) {
	wav := append([]byte("RIFF"), make([]byte, 4)...)
	wav = append(wav, []byte("WAVE")...)
	wav = append(wav, make([]byte, 32)...)
	payload := []byte{10, 11, 12, 13}
	wav = append(wav, payload...)

	stream := &geminiTTSChunkedStream{data: wav, mimeType: "audio/wav"}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if !bytes.Equal(chunk.Frame.Data, payload) {
		t.Fatalf("chunk data = %v, want %v", chunk.Frame.Data, payload)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second Next() error = %v, want EOF", err)
	}
}
