package google

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	texttospeech "cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestNewGoogleTTSRejectsMissingCredentialsFile(t *testing.T) {
	_, err := NewGoogleTTS("/definitely/missing/google-credentials.json")
	if err == nil {
		t.Fatal("NewGoogleTTS returned nil error, want missing credentials error")
	}
}

func TestNewGoogleTTSRejectsSSMLStreamingLikeReference(t *testing.T) {
	_, err := NewGoogleTTS("", WithGoogleTTSSSML(true))
	if err == nil || !strings.Contains(err.Error(), "SSML support is not available for streaming synthesis") {
		t.Fatalf("NewGoogleTTS error = %v, want reference SSML streaming error", err)
	}
}

func TestGoogleTTSMetadata(t *testing.T) {
	provider := newGoogleTTSWithClient(nil)

	if got := provider.Label(); got != "google.TTS" {
		t.Fatalf("Label = %q, want google.TTS", got)
	}
	if got := provider.SampleRate(); got != 24000 {
		t.Fatalf("SampleRate = %d, want 24000", got)
	}
	if got := provider.NumChannels(); got != 1 {
		t.Fatalf("NumChannels = %d, want 1", got)
	}
	if got := provider.Model(); got != "gemini-2.5-flash-tts" {
		t.Fatalf("Model = %q, want gemini-2.5-flash-tts", got)
	}
	if got := provider.Provider(); got != "Google Cloud Platform" {
		t.Fatalf("Provider = %q, want Google Cloud Platform", got)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities = %#v, want reference streaming without aligned transcript", caps)
	}
}

func TestGoogleTTSStreamSendsReferenceConfigAndInput(t *testing.T) {
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{
				AudioContent: []byte{1, 2, 3, 4},
			}},
		},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSLanguage("id-ID"),
		WithGoogleTTSVoice("id-ID-Standard-A"),
		WithGoogleTTSModel("gemini-custom"),
	)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if !provider.Capabilities().Streaming {
		t.Fatal("Capabilities().Streaming = false, want true like reference")
	}
	if err := stream.PushText("halo"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	if len(client.stream.sent) != 2 {
		t.Fatalf("sent requests = %d, want config and input", len(client.stream.sent))
	}
	config := client.stream.sent[0].GetStreamingConfig()
	if config == nil {
		t.Fatal("first request has nil streaming_config")
	}
	if config.GetVoice().GetLanguageCode() != "id-ID" || config.GetVoice().GetName() != "id-ID-Standard-A" || config.GetVoice().GetModelName() != "gemini-custom" {
		t.Fatalf("streaming voice = %+v, want configured voice", config.GetVoice())
	}
	if config.GetStreamingAudioConfig().GetSampleRateHertz() != 24000 || config.GetStreamingAudioConfig().GetAudioEncoding() != texttospeech.AudioEncoding_PCM {
		t.Fatalf("audio config = %+v, want PCM 24 kHz", config.GetStreamingAudioConfig())
	}
	if got := client.stream.sent[1].GetInput().GetText(); got != "halo" {
		t.Fatalf("input text = %q, want halo", got)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %v, want response bytes", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame = %+v, want 24 kHz mono samples", audio.Frame)
	}
}

func TestGoogleTTSStreamClonesReferenceAudioFrames(t *testing.T) {
	providerAudio := []byte{1, 2, 3, 4}
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{
				AudioContent: providerAudio,
			}},
		},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	providerAudio[0] = 9
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("audio data after provider mutation = %v, want cloned frame data", got)
	}
}

func TestGoogleTTSStreamPreservesReferencePCMSampleBoundaries(t *testing.T) {
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{
				{AudioContent: []byte{1}},
				{AudioContent: []byte{2, 3, 4}},
			},
		},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("audio data = %v, want odd byte buffered into next PCM sample", got)
	}
	if got := audio.Frame.SamplesPerChannel; got != 2 {
		t.Fatalf("samples per channel = %d, want 2 complete PCM16 samples", got)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next returned error: %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final Next = %+v, want final marker", final)
	}
}

func TestGoogleTTSStreamBuffersReferenceProgressivePCMFrame(t *testing.T) {
	first := bytes.Repeat([]byte{1, 2}, 200)
	second := bytes.Repeat([]byte{3, 4}, 280)
	want := append(append([]byte(nil), first...), second...)
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{
				{AudioContent: first},
				{AudioContent: second},
			},
		},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, want) {
		t.Fatalf("audio data length = %d, want buffered 20ms frame length %d", len(got), len(want))
	}
	if got := audio.Frame.SamplesPerChannel; got != 480 {
		t.Fatalf("samples per channel = %d, want reference 20ms frame", got)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next returned error: %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final Next = %+v, want final marker", final)
	}
}

func TestGoogleTTSStreamMarkupInputMatchesReference(t *testing.T) {
	client := &fakeGoogleTTSClient{stream: &fakeGoogleTTSStream{}}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSMarkup(true))

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("<speak-as interpret-as=\"characters\">ABC</speak-as>"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	if len(client.stream.sent) != 2 {
		t.Fatalf("sent requests = %d, want config and markup input", len(client.stream.sent))
	}
	input := client.stream.sent[1].GetInput()
	if got := input.GetMarkup(); got != "<speak-as interpret-as=\"characters\">ABC</speak-as>" {
		t.Fatalf("markup input = %q, want raw markup text", got)
	}
	if got := input.GetText(); got != "" {
		t.Fatalf("text input = %q, want empty when markup is enabled", got)
	}
}

func TestGoogleTTSStreamRejectsSSMLLikeReference(t *testing.T) {
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{}, WithGoogleTTSSSML(true))

	stream, err := provider.Stream(context.Background())

	if stream != nil {
		t.Fatalf("Stream = %#v, want nil", stream)
	}
	if err == nil || !strings.Contains(err.Error(), "SSML support is not available for streaming synthesis") {
		t.Fatalf("Stream error = %v, want reference SSML streaming error", err)
	}
}

func TestGoogleTTSSSMLCapabilitiesDisableStreaming(t *testing.T) {
	provider := newGoogleTTSWithClient(nil, WithGoogleTTSSSML(true))

	if provider.Capabilities().Streaming {
		t.Fatal("Capabilities().Streaming = true, want false when SSML disables streaming")
	}
}

func TestGoogleTTSStreamingCapabilityMatchesReferenceOption(t *testing.T) {
	provider := newGoogleTTSWithClient(nil, WithGoogleTTSStreaming(false))

	if provider.Capabilities().Streaming {
		t.Fatal("Capabilities().Streaming = true, want false from reference use_streaming option")
	}
}

func TestGoogleTTSStreamSendsCompletedSentenceBeforeFlushLikeReference(t *testing.T) {
	client := &fakeGoogleTTSClient{stream: &fakeGoogleTTSStream{}}
	provider := newGoogleTTSWithClient(client)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("This first sentence is definitely long enough. Tail"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if len(client.stream.sent) != 2 {
		t.Fatalf("sent after PushText = %d, want config plus completed sentence input", len(client.stream.sent))
	}
	if got := client.stream.sent[1].GetInput().GetText(); got != "This first sentence is definitely long enough." {
		t.Fatalf("first input text = %q, want completed sentence", got)
	}
	if client.stream.closed {
		t.Fatal("stream closed before Flush, want tail to remain in same input stream")
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if len(client.stream.sent) != 3 {
		t.Fatalf("sent after Flush = %d, want flushed tail input", len(client.stream.sent))
	}
	if got := client.stream.sent[2].GetInput().GetText(); got != "Tail" {
		t.Fatalf("tail input text = %q, want flushed tail", got)
	}
	if !client.stream.closed {
		t.Fatal("stream not closed after Flush")
	}
}

func TestGoogleTTSStreamNextWaitsForFlushedAudio(t *testing.T) {
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{
				AudioContent: []byte{9, 8, 7, 6},
			}},
		},
	}
	provider := newGoogleTTSWithClient(client)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	audioCh := make(chan *texttospeech.StreamingSynthesizeResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if err != nil {
			errCh <- err
			return
		}
		audioCh <- &texttospeech.StreamingSynthesizeResponse{AudioContent: audio.Frame.Data}
	}()

	select {
	case audio := <-audioCh:
		t.Fatalf("Next returned audio before Flush: %v", audio.GetAudioContent())
	case err := <-errCh:
		t.Fatalf("Next returned error before Flush: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	select {
	case audio := <-audioCh:
		if !bytes.Equal(audio.GetAudioContent(), []byte{9, 8, 7, 6}) {
			t.Fatalf("audio = %v, want flushed response bytes", audio.GetAudioContent())
		}
	case err := <-errCh:
		t.Fatalf("Next returned error after Flush: %v", err)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next did not receive flushed audio")
	}
}

func TestGoogleTTSStreamEmitsReferenceFinalMarkerAfterAudio(t *testing.T) {
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{
				AudioContent: []byte{1, 2, 3, 4},
			}},
		},
	}
	provider := newGoogleTTSWithClient(client)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	googleStream, ok := stream.(*googleTTSSynthesizeStream)
	if !ok {
		t.Fatalf("stream type = %T, want *googleTTSSynthesizeStream", stream)
	}
	if err := googleStream.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next audio error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first Next = %+v, want audio frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final error = %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next = %+v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestGoogleTTSStreamEndInputIgnoresLaterInputLikeReference(t *testing.T) {
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{
				AudioContent: []byte{1, 2, 3, 4},
			}},
		},
	}
	provider := newGoogleTTSWithClient(client)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	googleStream := stream.(*googleTTSSynthesizeStream)
	if err := googleStream.EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}
	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText after EndInput error = %v, want nil like reference", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after EndInput error = %v, want nil like reference", err)
	}
	if err := googleStream.EndInput(); err != nil {
		t.Fatalf("second EndInput error = %v, want nil like reference", err)
	}
}

func TestGoogleTTSStreamSkipsEmptyAudioResponses(t *testing.T) {
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{
				{},
				{AudioContent: []byte{1, 2, 3, 4}},
			},
		},
	}
	provider := newGoogleTTSWithClient(client)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.(*googleTTSSynthesizeStream).EndInput(); err != nil {
		t.Fatalf("EndInput returned error: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio == nil || audio.Frame == nil || !bytes.Equal(audio.Frame.Data, []byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want first non-empty provider audio", audio)
	}
}

func TestGoogleTTSStreamErrorsWhenReferenceTextProducesNoAudio(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil no-audio error", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIError", err, err)
	}
	if !apiErr.Retryable {
		t.Fatal("APIError retryable = false, want true")
	}
	if !strings.Contains(apiErr.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("APIError = %q, want reference no-audio message", apiErr.Error())
	}
	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after no-audio = (%+v, %v), want nil EOF", audio, err)
	}
}

func TestGoogleTTSStreamSynthesizesSecondSegmentLikeReference(t *testing.T) {
	firstStream := &fakeGoogleTTSStream{
		responses: []*texttospeech.StreamingSynthesizeResponse{{
			AudioContent: []byte{1, 2, 3, 4},
		}},
	}
	secondStream := &fakeGoogleTTSStream{
		responses: []*texttospeech.StreamingSynthesizeResponse{{
			AudioContent: []byte{5, 6, 7, 8},
		}},
	}
	client := &fakeGoogleTTSClient{streams: []*fakeGoogleTTSStream{firstStream, secondStream}}
	provider := newGoogleTTSWithClient(client)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.PushText("first"); err != nil {
		t.Fatalf("PushText first returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush first returned error: %v", err)
	}
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next audio error = %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("first Next audio = %+v, want audio", audio)
	}
	if final, err := stream.Next(); err != nil || final == nil || !final.IsFinal {
		t.Fatalf("first final = (%+v, %v), want final marker", final, err)
	}
	if !firstStream.closed {
		t.Fatal("first stream closed = false after first Flush")
	}
	if client.streamCalls != 1 || len(firstStream.sent) != 2 {
		t.Fatalf("first segment calls = %d sent=%d, want one stream with config and input", client.streamCalls, len(firstStream.sent))
	}

	if err := stream.PushText("second"); err != nil {
		t.Fatalf("PushText second returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush second returned error: %v", err)
	}
	if client.streamCalls != 2 {
		t.Fatalf("stream calls after second segment = %d, want second provider segment", client.streamCalls)
	}
	if len(secondStream.sent) != 2 {
		t.Fatalf("second stream sent requests = %d, want config and input", len(secondStream.sent))
	}
	if !secondStream.closed {
		t.Fatal("second stream closed = false after second Flush")
	}
	secondAudio, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next audio error = %v", err)
	}
	if secondAudio == nil || secondAudio.Frame == nil || !bytes.Equal(secondAudio.Frame.Data, []byte{5, 6, 7, 8}) {
		t.Fatalf("second Next audio = %+v, want second segment audio", secondAudio)
	}
	if final, err := stream.Next(); err != nil || final == nil || !final.IsFinal {
		t.Fatalf("second final = (%+v, %v), want final marker", final, err)
	}
	googleStream := stream.(*googleTTSSynthesizeStream)
	googleStream.mu.Lock()
	buffered := googleStream.buffer.String()
	flushed := googleStream.flushed
	googleStream.mu.Unlock()
	if buffered != "" {
		t.Fatalf("buffer after second segment = %q, want empty", buffered)
	}
	if flushed != 2 {
		t.Fatalf("flush count after second segment = %d, want two provider segments", flushed)
	}
	if err := googleStream.EndInput(); err != nil {
		t.Fatalf("EndInput after second segment returned error: %v", err)
	}
	if client.streamCalls != 2 {
		t.Fatalf("stream calls after EndInput = %d, want still two", client.streamCalls)
	}
}

func TestGoogleTTSSynthesizeRequestUsesReferenceDefaults(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if got := req.GetVoice().GetLanguageCode(); got != "en-US" {
		t.Fatalf("voice language = %q, want en-US", got)
	}
	if got := req.GetVoice().GetName(); got != "Charon" {
		t.Fatalf("voice name = %q, want Charon", got)
	}
	if got := req.GetVoice().GetModelName(); got != "gemini-2.5-flash-tts" {
		t.Fatalf("voice model = %q, want gemini-2.5-flash-tts", got)
	}
	if got := req.GetAudioConfig().GetAudioEncoding(); got != texttospeech.AudioEncoding_PCM {
		t.Fatalf("audio encoding = %v, want PCM", got)
	}
	if got := req.GetAudioConfig().GetSampleRateHertz(); got != 24000 {
		t.Fatalf("sample rate = %d, want 24000", got)
	}
	if got := req.GetAudioConfig().GetSpeakingRate(); got != 1.0 {
		t.Fatalf("speaking rate = %v, want 1.0", got)
	}
}

func TestGoogleTTSUsesConfiguredSampleRate(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: []byte{5, 6, 7, 8}}},
		},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSSampleRate(16000))

	if got := provider.SampleRate(); got != 16000 {
		t.Fatalf("SampleRate = %d, want 16000", got)
	}
	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	if got := req.GetAudioConfig().GetSampleRateHertz(); got != 16000 {
		t.Fatalf("synthesize sample rate = %d, want 16000", got)
	}
	audio, err := chunked.Next()
	if err != nil {
		t.Fatalf("chunked Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("chunked frame sample rate = %d, want 16000", audio.Frame.SampleRate)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	config := client.stream.sent[0].GetStreamingConfig().GetStreamingAudioConfig()
	if got := config.GetSampleRateHertz(); got != 16000 {
		t.Fatalf("stream config sample rate = %d, want 16000", got)
	}
	streamAudio, err := stream.Next()
	if err != nil {
		t.Fatalf("stream Next returned error: %v", err)
	}
	if streamAudio.Frame.SampleRate != 16000 {
		t.Fatalf("stream frame sample rate = %d, want 16000", streamAudio.Frame.SampleRate)
	}
}

func TestGoogleTTSPreservesReferenceExplicitZeroSampleRate(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: []byte{5, 6, 7, 8}}},
		},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSSampleRate(0))

	if got := provider.SampleRate(); got != 0 {
		t.Fatalf("SampleRate = %d, want explicit zero", got)
	}
	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	if got := req.GetAudioConfig().GetSampleRateHertz(); got != 0 {
		t.Fatalf("synthesize sample rate = %d, want explicit zero", got)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	config := client.stream.sent[0].GetStreamingConfig().GetStreamingAudioConfig()
	if got := config.GetSampleRateHertz(); got != 0 {
		t.Fatalf("stream config sample rate = %d, want explicit zero", got)
	}
}

func TestGoogleTTSUsesReferenceAudioEncoding(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: mp3Data},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: []byte{5, 6, 7, 8}}},
		},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSAudioEncoding(texttospeech.AudioEncoding_MP3))

	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	if got := req.GetAudioConfig().GetAudioEncoding(); got != texttospeech.AudioEncoding_MP3 {
		t.Fatalf("synthesize audio encoding = %v, want MP3", got)
	}
	audio, err := chunked.Next()
	if err != nil {
		t.Fatalf("chunked Next returned error: %v", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatalf("chunked Next = %+v, want decoded MP3 audio frame", audio)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded MP3 frame is empty")
	}
	if len(audio.Frame.Data) <= len(mp3Data) && bytes.Equal(audio.Frame.Data, mp3Data[:len(audio.Frame.Data)]) {
		t.Fatal("frame data still contains compressed MP3 bytes")
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	config := client.stream.sent[0].GetStreamingConfig().GetStreamingAudioConfig()
	if got := config.GetAudioEncoding(); got != texttospeech.AudioEncoding_PCM {
		t.Fatalf("stream audio encoding = %v, want reference PCM fallback for MP3", got)
	}
}

func TestGoogleTTSUpdateOptionsKeepsReferenceAudioFormat(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: []byte{5, 6, 7, 8}}},
		},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSSampleRate(16000),
		WithGoogleTTSAudioEncoding(texttospeech.AudioEncoding_PCM),
	)

	provider.UpdateOptions(
		WithGoogleTTSSampleRate(48000),
		WithGoogleTTSAudioEncoding(texttospeech.AudioEncoding_MP3),
	)

	if got := provider.SampleRate(); got != 16000 {
		t.Fatalf("SampleRate after update = %d, want reference constructor-time 16000", got)
	}
	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	audio := req.GetAudioConfig()
	if got := audio.GetSampleRateHertz(); got != 16000 {
		t.Fatalf("synthesize sample rate after update = %d, want 16000", got)
	}
	if got := audio.GetAudioEncoding(); got != texttospeech.AudioEncoding_PCM {
		t.Fatalf("synthesize encoding after update = %v, want PCM", got)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	streamAudio := client.stream.sent[0].GetStreamingConfig().GetStreamingAudioConfig()
	if got := streamAudio.GetSampleRateHertz(); got != 16000 {
		t.Fatalf("stream sample rate after update = %d, want 16000", got)
	}
	if got := streamAudio.GetAudioEncoding(); got != texttospeech.AudioEncoding_PCM {
		t.Fatalf("stream encoding after update = %v, want PCM", got)
	}
}

func TestGoogleTTSDecodesReferenceOggOpusEncoding(t *testing.T) {
	opusData, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "change-sophie.opus"))
	if err != nil {
		t.Fatalf("read opus fixture: %v", err)
	}

	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: opusData},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: opusData}},
		},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSAudioEncoding(texttospeech.AudioEncoding_OGG_OPUS),
		WithGoogleTTSSampleRate(48000),
	)

	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	if got := req.GetAudioConfig().GetAudioEncoding(); got != texttospeech.AudioEncoding_OGG_OPUS {
		t.Fatalf("synthesize audio encoding = %v, want OGG_OPUS", got)
	}
	batchAudio, err := chunked.Next()
	if err != nil {
		t.Fatalf("chunked Next returned error: %v", err)
	}
	assertGoogleDecodedOpusFrame(t, batchAudio, opusData)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	config := client.stream.sent[0].GetStreamingConfig().GetStreamingAudioConfig()
	if got := config.GetAudioEncoding(); got != texttospeech.AudioEncoding_OGG_OPUS {
		t.Fatalf("stream audio encoding = %v, want OGG_OPUS", got)
	}
	streamAudio, err := stream.Next()
	if err != nil {
		t.Fatalf("stream Next returned error: %v", err)
	}
	assertGoogleDecodedOpusFrame(t, streamAudio, opusData)
}

func TestGoogleTTSStreamDecodesSplitReferenceOggOpusAudio(t *testing.T) {
	opusData, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "change-sophie.opus"))
	if err != nil {
		t.Fatalf("read opus fixture: %v", err)
	}
	splitAt := 8
	client := &fakeGoogleTTSClient{
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{
				{AudioContent: opusData[:splitAt]},
				{AudioContent: opusData[splitAt:]},
			},
		},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSAudioEncoding(texttospeech.AudioEncoding_OGG_OPUS),
		WithGoogleTTSSampleRate(48000),
	)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	assertGoogleDecodedOpusFrame(t, audio, opusData)
}

func TestGoogleTTSSynthesizeReturnsAPITimeoutError(t *testing.T) {
	client := &fakeGoogleTTSClient{err: context.DeadlineExceeded}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestGoogleTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	client := &fakeGoogleTTSClient{err: status.Error(codes.PermissionDenied, "permission denied")}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.PermissionDenied) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.PermissionDenied)
	}
	if statusErr.Retryable {
		t.Fatal("status retryable = true, want false for permission denied")
	}
}

func TestGoogleTTSSynthesizeTreatsReference499AsEOF(t *testing.T) {
	client := &fakeGoogleTTSClient{err: llm.NewAPIStatusError("client closed", 499, "req_499", nil)}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF for reference client-closed 499", err)
	}
}

func TestGoogleTTSSynthesizeTreatsGRPCCanceledAsReferenceEOF(t *testing.T) {
	client := &fakeGoogleTTSClient{err: status.Error(codes.Canceled, "context canceled")}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF for reference client-closed gRPC cancel", err)
	}
}

func TestGoogleTTSOptionsOverrideReferenceVoiceFields(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSLanguage("id-ID"),
		WithGoogleTTSVoice("id-ID-Standard-A"),
		WithGoogleTTSModel("gemini-custom"),
	)

	stream, err := provider.Synthesize(context.Background(), "halo")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	voice := req.GetVoice()
	if voice.GetLanguageCode() != "id-ID" || voice.GetName() != "id-ID-Standard-A" || voice.GetModelName() != "gemini-custom" {
		t.Fatalf("voice = %+v, want configured language, voice, and model", voice)
	}
}

func TestGoogleTTSGenderOptionMatchesReference(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSGender("female"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if got := req.GetVoice().GetSsmlGender(); got != texttospeech.SsmlVoiceGender_FEMALE {
		t.Fatalf("voice gender = %v, want FEMALE", got)
	}
}

func TestGoogleTTSLocationOptionMatchesReferenceEndpoint(t *testing.T) {
	cfg := googleTTSConfigFromOptions(WithGoogleTTSLocation("europe-west1"))

	if got := googleTTSEndpoint(cfg); got != "europe-west1-texttospeech.googleapis.com" {
		t.Fatalf("endpoint = %q, want europe-west1-texttospeech.googleapis.com", got)
	}
}

func TestGoogleTTSEmptyLocationOptionMatchesReferenceEndpoint(t *testing.T) {
	cfg := googleTTSConfigFromOptions(WithGoogleTTSLocation(""))

	if got := googleTTSEndpoint(cfg); got != "-texttospeech.googleapis.com" {
		t.Fatalf("endpoint = %q, want reference explicit empty location endpoint", got)
	}
}

func TestGoogleTTSVoiceCloneKeyMatchesReference(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSVoiceCloneKey("clone-key"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	voice := req.GetVoice()
	if voice.GetVoiceClone().GetVoiceCloningKey() != "clone-key" {
		t.Fatalf("voice clone = %+v, want configured clone key", voice.GetVoiceClone())
	}
	if voice.GetName() != "" {
		t.Fatalf("voice name = %q, want empty when voice clone is configured", voice.GetName())
	}
}

func TestGoogleTTSEmptyVoiceCloneKeyMatchesReference(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSVoiceCloneKey(""))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	voice := req.GetVoice()
	if voice.GetVoiceClone() == nil {
		t.Fatalf("voice clone = nil, want explicit empty clone key")
	}
	if voice.GetVoiceClone().GetVoiceCloningKey() != "" {
		t.Fatalf("voice clone key = %q, want empty", voice.GetVoiceClone().GetVoiceCloningKey())
	}
	if voice.GetName() != "" {
		t.Fatalf("voice name = %q, want empty when voice clone is configured", voice.GetName())
	}
	if voice.GetModelName() != "" {
		t.Fatalf("voice model = %q, want empty for Chirp 3 clone voice", voice.GetModelName())
	}
}

func TestGoogleTTSChirpVoiceSelectsReferenceModel(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSVoice("en-US-Chirp3-HD-Charon"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	voice := req.GetVoice()
	if voice.GetName() != "en-US-Chirp3-HD-Charon" {
		t.Fatalf("voice name = %q, want Chirp voice", voice.GetName())
	}
	if voice.GetModelName() != "" {
		t.Fatalf("voice model = %q, want empty for Chirp 3 voice", voice.GetModelName())
	}
	if got := provider.Model(); got != "chirp_3" {
		t.Fatalf("Model() = %q, want chirp_3", got)
	}
}

func TestGoogleTTSChirpModelUsesReferenceDefaultVoice(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSModel("chirp_3"))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	voice := req.GetVoice()
	if voice.GetName() != "en-US-Chirp3-HD-Charon" {
		t.Fatalf("voice name = %q, want reference Chirp 3 default voice", voice.GetName())
	}
	if voice.GetModelName() != "" {
		t.Fatalf("voice model = %q, want empty for Chirp 3", voice.GetModelName())
	}
}

func TestGoogleTTSSSMLInputMatchesReference(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSSSML(true))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if got := req.GetInput().GetSsml(); got != "<speak>hello</speak>" {
		t.Fatalf("ssml input = %q, want reference speak wrapper", got)
	}
	if got := req.GetInput().GetText(); got != "" {
		t.Fatalf("text input = %q, want empty when SSML is enabled", got)
	}
}

func TestGoogleTTSMarkupInputMatchesReference(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSMarkup(true))

	stream, err := provider.Synthesize(context.Background(), "<speak-as interpret-as=\"characters\">ABC</speak-as>")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if got := req.GetInput().GetMarkup(); got != "<speak-as interpret-as=\"characters\">ABC</speak-as>" {
		t.Fatalf("markup input = %q, want raw markup text", got)
	}
	if got := req.GetInput().GetText(); got != "" {
		t.Fatalf("text input = %q, want empty when markup is enabled", got)
	}
}

func TestGoogleTTSSynthesizeRejectsSSMLWithMarkupLikeReference(t *testing.T) {
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	},
		WithGoogleTTSSSML(true),
		WithGoogleTTSMarkup(true),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")

	if stream != nil {
		t.Fatalf("Synthesize stream = %#v, want nil", stream)
	}
	if err == nil || !strings.Contains(err.Error(), "SSML support is not available for markup input") {
		t.Fatalf("Synthesize error = %v, want reference SSML markup error", err)
	}
}

func TestGoogleTTSUpdateOptionsMatchesReference(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client)

	provider.UpdateOptions(
		WithGoogleTTSLanguage("id-ID"),
		WithGoogleTTSVoice("id-ID-Standard-A"),
		WithGoogleTTSModel("gemini-custom"),
	)

	stream, err := provider.Synthesize(context.Background(), "halo")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	voice := req.GetVoice()
	if voice.GetLanguageCode() != "id-ID" || voice.GetName() != "id-ID-Standard-A" || voice.GetModelName() != "gemini-custom" {
		t.Fatalf("voice = %+v, want updated language, voice, and model", voice)
	}
}

func TestGoogleTTSUpdateOptionsReplacesVoiceParamsLikeReference(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		stream:   &fakeGoogleTTSStream{},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSLanguage("id-ID"),
		WithGoogleTTSModel("gemini-custom"),
	)

	provider.UpdateOptions(WithGoogleTTSVoice("id-ID-Standard-B"))

	if provider.voice.GetLanguageCode() != "" || provider.voice.GetName() != "id-ID-Standard-B" || provider.voice.GetModelName() != "" {
		t.Fatalf("voice = %+v, want reference replacement with only new voice name", provider.voice)
	}
	if got := provider.Model(); got != "gemini-custom" {
		t.Fatalf("Model() = %q, want existing reference model metadata", got)
	}
	stream, err := provider.Synthesize(context.Background(), "halo")
	if err != nil {
		t.Fatalf("Synthesize after voice update error = %v", err)
	}
	defer stream.Close()
	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if voice := req.GetVoice(); voice.GetLanguageCode() != "" || voice.GetName() != "id-ID-Standard-B" || voice.GetModelName() != "" {
		t.Fatalf("request voice = %+v, want reference replacement with only new voice name", voice)
	}

	streaming, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream after voice update error = %v", err)
	}
	defer streaming.Close()
	if err := streaming.PushText("halo stream"); err != nil {
		t.Fatalf("PushText after voice update error = %v", err)
	}
	if err := streaming.Flush(); err != nil {
		t.Fatalf("Flush after voice update error = %v", err)
	}
	if voice := client.stream.sent[0].GetStreamingConfig().GetVoice(); voice.GetLanguageCode() != "" || voice.GetName() != "id-ID-Standard-B" || voice.GetModelName() != "" {
		t.Fatalf("stream voice = %+v, want reference replacement with only new voice name", voice)
	}
}

func TestGoogleTTSUpdateOptionsIgnoresReferenceVoiceCloneKey(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSVoiceCloneKey("initial-clone"))

	provider.UpdateOptions(WithGoogleTTSVoiceCloneKey("updated-clone"))

	if got := provider.voice.GetVoiceClone().GetVoiceCloningKey(); got != "initial-clone" {
		t.Fatalf("provider clone key after update = %q, want constructor-time initial-clone", got)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize after clone update error = %v", err)
	}
	defer stream.Close()
	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if got := req.GetVoice().GetVoiceClone().GetVoiceCloningKey(); got != "initial-clone" {
		t.Fatalf("request clone key after update = %q, want constructor-time initial-clone", got)
	}
}

func TestGoogleTTSUpdateOptionsPreservesExplicitEmptyVoice(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSVoice("en-US-Standard-A"))

	provider.UpdateOptions(WithGoogleTTSVoice(""))

	if provider.voice.GetName() != "" || provider.voice.GetLanguageCode() != "" || provider.voice.GetModelName() != "" {
		t.Fatalf("voice = %+v, want reference replacement with explicit empty voice name", provider.voice)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize after empty voice update error = %v", err)
	}
	defer stream.Close()
	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if voice := req.GetVoice(); voice.GetName() != "" || voice.GetLanguageCode() != "" || voice.GetModelName() != "" {
		t.Fatalf("request voice = %+v, want explicit empty voice name", voice)
	}
}

func TestGoogleTTSUpdateOptionsPreservesExplicitEmptyModel(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSModel("gemini-custom"))

	provider.UpdateOptions(WithGoogleTTSModel(""))

	if got := provider.Model(); got != "Chirp3" {
		t.Fatalf("Model() = %q, want reference fallback after explicit empty model", got)
	}
	if provider.voice.GetName() != "" || provider.voice.GetLanguageCode() != "" || provider.voice.GetModelName() != "" {
		t.Fatalf("voice = %+v, want reference replacement with explicit empty model", provider.voice)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize after empty model update error = %v", err)
	}
	defer stream.Close()
	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if voice := req.GetVoice(); voice.GetName() != "" || voice.GetLanguageCode() != "" || voice.GetModelName() != "" {
		t.Fatalf("request voice = %+v, want explicit empty model", voice)
	}
}

func TestGoogleTTSUpdateOptionsPreservesExplicitEmptyLanguage(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSLanguage("id-ID"))

	provider.UpdateOptions(WithGoogleTTSLanguage(""))

	if provider.voice.GetName() != "" || provider.voice.GetLanguageCode() != "" || provider.voice.GetModelName() != "" {
		t.Fatalf("voice = %+v, want reference replacement with explicit empty language", provider.voice)
	}
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize after empty language update error = %v", err)
	}
	defer stream.Close()
	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if voice := req.GetVoice(); voice.GetName() != "" || voice.GetLanguageCode() != "" || voice.GetModelName() != "" {
		t.Fatalf("request voice = %+v, want explicit empty language", voice)
	}
}

func TestGoogleTTSPromptMatchesReferenceRequests(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: []byte{5, 6}}},
		},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSPrompt("speak warmly"))

	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	if got := req.GetInput().GetPrompt(); got != "speak warmly" {
		t.Fatalf("synthesize prompt = %q, want speak warmly", got)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if got := client.stream.sent[1].GetInput().GetPrompt(); got != "speak warmly" {
		t.Fatalf("stream prompt = %q, want speak warmly on first input", got)
	}
}

func TestGoogleTTSCustomPronunciationsMatchReferenceRequests(t *testing.T) {
	phrase := "Cavos"
	pronunciation := "keIvAs"
	encoding := texttospeech.CustomPronunciationParams_PHONETIC_ENCODING_X_SAMPA
	custom := &texttospeech.CustomPronunciations{
		Pronunciations: []*texttospeech.CustomPronunciationParams{{
			Phrase:           &phrase,
			PhoneticEncoding: &encoding,
			Pronunciation:    &pronunciation,
		}},
	}
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: []byte{5, 6}}},
		},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSCustomPronunciations(custom))

	chunked, err := provider.Synthesize(context.Background(), "Say Cavos")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	if got := req.GetInput().GetCustomPronunciations(); got != custom {
		t.Fatalf("synthesize custom pronunciations = %#v, want configured value", got)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("Say Cavos"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if got := client.stream.sent[0].GetStreamingConfig().GetCustomPronunciations(); got != custom {
		t.Fatalf("stream custom pronunciations = %#v, want configured value", got)
	}
}

func TestGoogleTTSStreamClosesAfterInputSendFailure(t *testing.T) {
	wantErr := errors.New("send failed")
	streamClient := &fakeGoogleTTSStream{sendErrAfterConfig: wantErr}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}

	err = stream.Flush()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Flush error = %v, want %v", err, wantErr)
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after send failure")
	}
	err = stream.PushText("again")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after failed Flush error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after send failure error = %v", err)
	}
}

func TestGoogleTTSStreamFlushReturnsAPIStatusErrorForInputSendFailure(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{sendErrAfterConfig: status.Error(codes.Unavailable, "unavailable")}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}

	err = stream.Flush()

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Flush error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.Unavailable) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.Unavailable)
	}
	if !statusErr.Retryable {
		t.Fatal("status retryable = false, want true for unavailable")
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after send failure")
	}
}

func TestGoogleTTSStreamFlushReturnsAPIStatusErrorForCloseSendFailure(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{closeErr: status.Error(codes.Unavailable, "unavailable")}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}

	err = stream.Flush()

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Flush error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.Unavailable) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.Unavailable)
	}
	if !statusErr.Retryable {
		t.Fatal("status retryable = false, want true for unavailable")
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after CloseSend failure")
	}
}

func TestGoogleTTSStreamNextReturnsAPITimeoutError(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{recvErr: context.DeadlineExceeded}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestGoogleTTSStreamNextReturnsAPIStatusError(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{recvErr: status.Error(codes.Unavailable, "unavailable")}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.Unavailable) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.Unavailable)
	}
	if !statusErr.Retryable {
		t.Fatal("status retryable = false, want true for unavailable")
	}
}

func TestGoogleTTSStreamNextTreatsReference499AsEOF(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{recvErr: llm.NewAPIStatusError("client closed", 499, "req_499", nil)}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF for reference client-closed 499", err)
	}
}

func TestGoogleTTSStreamNextTreatsGRPCCanceledAsReferenceEOF(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{recvErr: status.Error(codes.Canceled, "context canceled")}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want EOF for reference client-closed gRPC cancel", err)
	}
}

func TestGoogleTTSStreamTreatsReference409AsRetryable(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{recvErr: status.Error(codes.AlreadyExists, "stream conflict")}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != int(codes.AlreadyExists) {
		t.Fatalf("status code = %d, want %d", statusErr.StatusCode, codes.AlreadyExists)
	}
	if !statusErr.Retryable {
		t.Fatal("status retryable = false, want true for reference 409 path")
	}
}

func TestGoogleTTSStreamNextErrorTerminatesStream(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{recvErr: status.Error(codes.Unavailable, "unavailable")}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %#v, want nil", audio)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %T %v, want APIStatusError", err, err)
	}

	audio, err = stream.Next()

	if audio != nil {
		t.Fatalf("second Next audio = %#v, want nil", audio)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after failed stream = %v, want ErrClosedPipe", err)
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after receive failure")
	}
}

func TestGoogleTTSProviderCloseClosesActiveStreams(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after provider Close")
	}
	if err := stream.PushText("again"); err != nil {
		t.Fatalf("PushText after provider Close error = %v, want nil like reference", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after provider Close error = %v, want nil like reference", err)
	}
}

func TestGoogleTTSStreamCloseSuppressesProviderCloseError(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{closeErr: errors.New("close failed")}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{})

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	googleStream := stream.(*googleTTSSynthesizeStream)
	googleStream.mu.Lock()
	googleStream.streams = []texttospeech.TextToSpeech_StreamingSynthesizeClient{streamClient}
	googleStream.active = streamClient
	googleStream.mu.Unlock()

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v, want nil cleanup error", err)
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after Close")
	}
}

func TestGoogleTTSStreamCloseIsIdempotent(t *testing.T) {
	cancelCalls := 0
	stream := &googleTTSSynthesizeStream{
		cancel: func() {
			cancelCalls++
		},
		streams: []texttospeech.TextToSpeech_StreamingSynthesizeClient{&fakeGoogleTTSStream{}},
	}
	stream.cond = sync.NewCond(&stream.mu)

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
	if cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1", cancelCalls)
	}
}

func TestGoogleTTSStreamCloseUnblocksPendingNext(t *testing.T) {
	recvBlock := make(chan struct{})
	streamClient := &fakeGoogleTTSStream{recvBlock: recvBlock}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if audio != nil {
			errCh <- errors.New("Next returned audio after Close")
			return
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		t.Fatalf("Next returned before Close: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after Close error = %v, want EOF", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next did not unblock after Close")
	}
}

func TestGoogleTTSStreamCloseCancelsBlockedReferenceStartup(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	client := &fakeGoogleTTSClient{
		stream:                     &fakeGoogleTTSStream{},
		blockStreamingSynthesize:   started,
		unblockStreamingSynthesize: release,
		streamingErrCh:             make(chan error, 1),
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}

	flushErrCh := make(chan error, 1)
	go func() {
		flushErrCh <- stream.Flush()
	}()

	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("StreamingSynthesize did not start")
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- stream.Close()
	}()

	select {
	case err := <-closeErrCh:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(release)
		<-flushErrCh
		<-closeErrCh
		t.Fatal("Close did not cancel blocked StreamingSynthesize startup")
	}

	select {
	case err := <-client.streamingErrCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("provider streaming error = %v, want context.Canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider streaming startup did not observe context cancellation")
	}

	if err := <-flushErrCh; !errors.Is(err, io.EOF) {
		t.Fatalf("Flush error = %v, want EOF after local close", err)
	}
}

func TestGoogleTTSStreamCloseUnblocksBlockedReferenceSend(t *testing.T) {
	sendStarted := make(chan struct{})
	sendRelease := make(chan struct{})
	streamClient := &fakeGoogleTTSStream{
		sendBlock:   sendStarted,
		sendRelease: sendRelease,
	}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}

	flushErrCh := make(chan error, 1)
	go func() {
		flushErrCh <- stream.Flush()
	}()

	select {
	case <-sendStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider Send did not start")
	}

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- stream.Close()
	}()

	select {
	case err := <-closeErrCh:
		if err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(sendRelease)
		<-flushErrCh
		<-closeErrCh
		t.Fatal("Close did not unblock blocked provider Send")
	}

	close(sendRelease)
	if err := <-flushErrCh; err == nil {
		t.Fatal("Flush error = nil, want closed send error after Close")
	}
	if !streamClient.closed {
		t.Fatal("provider stream closed = false")
	}
}

func TestGoogleTTSStreamCloseDropsLateReceiveAudioLikeReference(t *testing.T) {
	recvBlock := make(chan struct{})
	streamClient := &fakeGoogleTTSStream{
		recvBlock: recvBlock,
		responses: []*texttospeech.StreamingSynthesizeResponse{{
			AudioContent: bytes.Repeat([]byte{1, 2}, 480),
		}},
	}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{stream: streamClient})

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if audio != nil {
			errCh <- errors.New("Next returned late audio after Close")
			return
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		t.Fatalf("Next returned before Close: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	close(recvBlock)

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after Close error = %v, want EOF", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next did not unblock after Close")
	}
}

func TestGoogleTTSStreamIgnoresInputAfterCloseLikeReference(t *testing.T) {
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{})

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if err := stream.PushText("late"); err != nil {
		t.Fatalf("PushText after Close error = %v, want nil like reference", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush after Close error = %v, want nil like reference", err)
	}
}

func TestGoogleTTSRegisterStreamAfterCloseClosesStream(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{}
	provider := newGoogleTTSWithClient(&fakeGoogleTTSClient{})
	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	stream := &googleTTSSynthesizeStream{
		owner:   provider,
		ctx:     context.Background(),
		client:  provider.client,
		voice:   provider.voice,
		audio:   provider.audio,
		streams: []texttospeech.TextToSpeech_StreamingSynthesizeClient{streamClient},
	}
	stream.cond = sync.NewCond(&stream.mu)

	if provider.registerStream(stream) {
		t.Fatal("registerStream after provider Close = true, want false")
	}
	if !streamClient.closed {
		t.Fatal("stream client closed = false after rejected registration")
	}
	if err := stream.PushText("again"); err != nil {
		t.Fatalf("PushText after rejected registration error = %v, want nil like reference", err)
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams = %d, want 0", len(provider.streams))
	}
}

func TestGoogleTTSClosedStreamNextIgnoresQueuedProviderStream(t *testing.T) {
	streamClient := &fakeGoogleTTSStream{}
	stream := &googleTTSSynthesizeStream{
		ctx:     context.Background(),
		streams: []texttospeech.TextToSpeech_StreamingSynthesizeClient{streamClient},
		closed:  true,
	}
	stream.cond = sync.NewCond(&stream.mu)

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestGoogleTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	client := &fakeGoogleTTSClient{response: &texttospeech.SynthesizeSpeechResponse{}}
	provider := newGoogleTTSWithClient(client)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize after Close stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if client.synthesizeCalls != 0 {
		t.Fatalf("Synthesize after Close client calls = %d, want 0", client.synthesizeCalls)
	}
}

func TestGoogleTTSSynthesizeCloseBeforeNextSkipsReferenceRequest(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	if client.synthesizeCalls != 0 {
		t.Fatalf("Synthesize calls before Next = %d, want 0 like reference cancellable ChunkedStream", client.synthesizeCalls)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after close = (%+v, %v), want EOF", audio, err)
	}
	if client.synthesizeCalls != 0 {
		t.Fatalf("Synthesize calls after Close = %d, want 0", client.synthesizeCalls)
	}
}

func TestGoogleTTSChunkedStreamCloseIsIdempotent(t *testing.T) {
	decoder := &fakeGoogleTTSAudioStreamDecoder{secondCloseErr: errors.New("decoder closed twice")}
	cancelCalls := 0
	stream := &googleTTSChunkedStream{
		cancel: func() {
			cancelCalls++
		},
		decoder: decoder,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close returned error: %v, want nil like reference cleanup", err)
	}
	if decoder.closeCalls != 1 {
		t.Fatalf("decoder Close calls = %d, want 1", decoder.closeCalls)
	}
	if cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1", cancelCalls)
	}
}

func TestGoogleTTSChunkedStreamCloseSuppressesDecoderCloseError(t *testing.T) {
	stream := &googleTTSChunkedStream{
		decoder: &fakeGoogleTTSAudioStreamDecoder{closeErr: errors.New("decoder cleanup failed")},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v, want nil for caller-owned cleanup", err)
	}
	if audio, err := stream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after Close = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestGoogleTTSChunkedStreamCloseDuringDecoderReadReturnsEOF(t *testing.T) {
	decoder := &fakeGoogleTTSAudioStreamDecoder{
		nextStarted: make(chan struct{}),
		nextRelease: make(chan struct{}),
		nextErr:     errors.New("decoder closed"),
	}
	stream := &googleTTSChunkedStream{
		decoder:        decoder,
		decoderStarted: true,
		encoding:       texttospeech.AudioEncoding_MP3,
		emittedAudio:   true,
	}

	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if audio != nil {
			errCh <- fmt.Errorf("Next returned audio after Close: %#v", audio)
			return
		}
		errCh <- err
	}()

	select {
	case <-decoder.nextStarted:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("decoder Next did not start")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	close(decoder.nextRelease)

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after Close error = %v, want EOF", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next did not unblock after Close")
	}
}

func TestGoogleTTSSynthesizeCloseCancelsInFlightReferenceRequest(t *testing.T) {
	entered := make(chan struct{})
	client := &fakeGoogleTTSClient{
		blockSynthesize:  entered,
		response:         &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		synthesizeErrCh:  make(chan error, 1),
		synthesizeDoneCh: make(chan struct{}),
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		audio, err := stream.Next()
		if audio != nil {
			errCh <- fmt.Errorf("Next audio = %#v, want nil after close", audio)
			return
		}
		errCh <- err
	}()

	select {
	case <-entered:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider synthesize request did not start")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case err := <-client.synthesizeErrCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("provider synthesize error = %v, want context.Canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("provider synthesize did not observe context cancellation")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next after Close error = %v, want EOF", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Next did not unblock after Close")
	}
}

func TestGoogleTTSStreamAfterCloseIsRejected(t *testing.T) {
	client := &fakeGoogleTTSClient{stream: &fakeGoogleTTSStream{}}
	provider := newGoogleTTSWithClient(client)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream after Close stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if client.streamCalls != 0 {
		t.Fatalf("Stream after Close client calls = %d, want 0", client.streamCalls)
	}
}

func TestGoogleTTSSpeakingRateMatchesReferenceRequests(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
		stream: &fakeGoogleTTSStream{
			responses: []*texttospeech.StreamingSynthesizeResponse{{AudioContent: []byte{5, 6}}},
		},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSSpeakingRate(1.25))

	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	if got := req.GetAudioConfig().GetSpeakingRate(); got != 1.25 {
		t.Fatalf("synthesize speaking rate = %v, want 1.25", got)
	}

	provider.UpdateOptions(WithGoogleTTSSpeakingRate(0.8))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if got := client.stream.sent[0].GetStreamingConfig().GetStreamingAudioConfig().GetSpeakingRate(); got != 0.8 {
		t.Fatalf("stream speaking rate = %v, want 0.8", got)
	}
}

func TestGoogleTTSStreamKeepsCreationAudioOptions(t *testing.T) {
	client := &fakeGoogleTTSClient{stream: &fakeGoogleTTSStream{}}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSSpeakingRate(1.25))

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	provider.UpdateOptions(WithGoogleTTSSpeakingRate(0.8))

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	if got := client.stream.sent[0].GetStreamingConfig().GetStreamingAudioConfig().GetSpeakingRate(); got != 1.25 {
		t.Fatalf("stream speaking rate = %v, want creation-time 1.25", got)
	}
}

func TestGoogleTTSAudioConfigOptionsMatchReferenceRequests(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSPitch(2.5),
		WithGoogleTTSEffectsProfileID("telephony-class-application"),
		WithGoogleTTSVolumeGainDB(-2.0),
	)

	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	audio := req.GetAudioConfig()
	if got := audio.GetPitch(); got != 2.5 {
		t.Fatalf("pitch = %v, want 2.5", got)
	}
	if got := audio.GetEffectsProfileId(); len(got) != 1 || got[0] != "telephony-class-application" {
		t.Fatalf("effects profile = %v, want telephony-class-application", got)
	}
	if got := audio.GetVolumeGainDb(); got != -2.0 {
		t.Fatalf("volume gain = %v, want -2.0", got)
	}

	provider.UpdateOptions(WithGoogleTTSVolumeGainDB(3.5))
	chunked, err = provider.Synthesize(context.Background(), "hello again")
	if err != nil {
		t.Fatalf("Synthesize after update returned error: %v", err)
	}
	defer chunked.Close()
	req = requireGoogleTTSSynthesizeRequest(t, chunked, client)
	if got := req.GetAudioConfig().GetVolumeGainDb(); got != 3.5 {
		t.Fatalf("updated volume gain = %v, want 3.5", got)
	}
}

func TestGoogleTTSSynthesizeSnapshotsReferenceAudioConfig(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSSpeakingRate(1.25),
		WithGoogleTTSVolumeGainDB(-2.0),
	)

	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer chunked.Close()

	provider.UpdateOptions(
		WithGoogleTTSSpeakingRate(0.8),
		WithGoogleTTSVolumeGainDB(3.5),
	)

	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	audio := req.GetAudioConfig()
	if got := audio.GetSpeakingRate(); got != 1.25 {
		t.Fatalf("request speaking rate after provider update = %v, want snapshot 1.25", got)
	}
	if got := audio.GetVolumeGainDb(); got != -2.0 {
		t.Fatalf("request volume gain after provider update = %v, want snapshot -2.0", got)
	}
}

func TestGoogleTTSUpdateOptionsKeepsReferencePronunciationControls(t *testing.T) {
	initialEncoding := texttospeech.CustomPronunciationParams_PHONETIC_ENCODING_X_SAMPA
	initialPhrase := "Cavos"
	initialPronunciation := "keIvAs"
	initialCustom := &texttospeech.CustomPronunciations{
		Pronunciations: []*texttospeech.CustomPronunciationParams{{
			Phrase:           &initialPhrase,
			PhoneticEncoding: &initialEncoding,
			Pronunciation:    &initialPronunciation,
		}},
	}
	updatedPhrase := "LiveKit"
	updatedPronunciation := "laIvkit"
	updatedCustom := &texttospeech.CustomPronunciations{
		Pronunciations: []*texttospeech.CustomPronunciationParams{{
			Phrase:           &updatedPhrase,
			PhoneticEncoding: &initialEncoding,
			Pronunciation:    &updatedPronunciation,
		}},
	}
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3, 4}},
	}
	provider := newGoogleTTSWithClient(client,
		WithGoogleTTSPitch(2.5),
		WithGoogleTTSEffectsProfileID("telephony-class-application"),
		WithGoogleTTSCustomPronunciations(initialCustom),
	)

	provider.UpdateOptions(
		WithGoogleTTSPitch(-3.0),
		WithGoogleTTSEffectsProfileID("headphone-class-device"),
		WithGoogleTTSCustomPronunciations(updatedCustom),
	)

	chunked, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize after update returned error: %v", err)
	}
	defer chunked.Close()
	req := requireGoogleTTSSynthesizeRequest(t, chunked, client)
	audio := req.GetAudioConfig()
	if got := audio.GetPitch(); got != 2.5 {
		t.Fatalf("pitch after update = %v, want constructor-time 2.5", got)
	}
	if got := audio.GetEffectsProfileId(); len(got) != 1 || got[0] != "telephony-class-application" {
		t.Fatalf("effects profile after update = %v, want constructor-time telephony-class-application", got)
	}
	if got := req.GetInput().GetCustomPronunciations(); got != initialCustom {
		t.Fatalf("custom pronunciations after update = %#v, want constructor-time value", got)
	}
}

func TestGoogleTTSChirp3OmitsModelName(t *testing.T) {
	provider := newGoogleTTSWithClient(nil, WithGoogleTTSModel("chirp_3"))

	if got := provider.voice.GetModelName(); got != "" {
		t.Fatalf("model name = %q, want omitted for chirp_3", got)
	}
}

func TestGoogleTTSSynthesizeStripsWAVHeaderAndChunksAudio(t *testing.T) {
	payload := append(make([]byte, 44), []byte{1, 2, 3, 4}...)
	copy(payload[0:4], "RIFF")
	copy(payload[8:12], "WAVE")
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: payload},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	req := requireGoogleTTSSynthesizeRequest(t, stream, client)
	if req.GetInput().GetText() != "hello" {
		t.Fatalf("request = %#v, want hello text input", req)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Frame.Data; !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("chunk data = %v, want stripped payload", got)
	}
	if chunk.Frame.SampleRate != 24000 || chunk.Frame.NumChannels != 1 || chunk.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame = %#v, want 24k mono 2 samples", chunk.Frame)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second Next = %+v, want final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want io.EOF", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestGoogleTTSSynthesizeClonesReferenceAudioFrames(t *testing.T) {
	providerAudio := []byte{1, 2, 3, 4}
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: providerAudio},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	providerAudio[0] = 9
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("audio data after provider mutation = %v, want cloned frame data", got)
	}
}

func TestGoogleTTSSynthesizeDropsReferenceTrailingPartialPCMSample(t *testing.T) {
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: []byte{1, 2, 3}},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := audio.Frame.Data; !bytes.Equal(got, []byte{1, 2}) {
		t.Fatalf("audio data = %v, want only complete PCM16 samples", got)
	}
	if got := audio.Frame.SamplesPerChannel; got != 1 {
		t.Fatalf("samples per channel = %d, want 1 complete PCM16 sample", got)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next returned error: %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final Next = %+v, want final marker", final)
	}
}

func TestGoogleTTSSynthesizeBuffersReferenceProgressivePCMFrames(t *testing.T) {
	firstFrame := bytes.Repeat([]byte{1, 2}, 480)
	secondFrame := bytes.Repeat([]byte{3, 4}, 960)
	response := append(append([]byte(nil), firstFrame...), secondFrame...)
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: response},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if got := first.Frame.Data; !bytes.Equal(got, firstFrame) {
		t.Fatalf("first frame length = %d, want reference 20ms frame length %d", len(got), len(firstFrame))
	}
	if got := first.Frame.SamplesPerChannel; got != 480 {
		t.Fatalf("first samples per channel = %d, want reference 20ms frame", got)
	}

	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if got := second.Frame.Data; !bytes.Equal(got, secondFrame) {
		t.Fatalf("second frame length = %d, want reference 40ms frame length %d", len(got), len(secondFrame))
	}
	if got := second.Frame.SamplesPerChannel; got != 960 {
		t.Fatalf("second samples per channel = %d, want reference 40ms frame", got)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("final Next returned error: %v", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final Next = %+v, want final marker", final)
	}
}

func TestGoogleTTSSynthesizeStripsExtendedWAVHeaderLikeReference(t *testing.T) {
	var payload bytes.Buffer
	payload.WriteString("RIFF")
	_ = binary.Write(&payload, binary.LittleEndian, uint32(0))
	payload.WriteString("WAVE")
	payload.WriteString("fmt ")
	_ = binary.Write(&payload, binary.LittleEndian, uint32(16))
	_ = binary.Write(&payload, binary.LittleEndian, uint16(1))
	_ = binary.Write(&payload, binary.LittleEndian, uint16(1))
	_ = binary.Write(&payload, binary.LittleEndian, uint32(24000))
	_ = binary.Write(&payload, binary.LittleEndian, uint32(48000))
	_ = binary.Write(&payload, binary.LittleEndian, uint16(2))
	_ = binary.Write(&payload, binary.LittleEndian, uint16(16))
	payload.WriteString("JUNK")
	_ = binary.Write(&payload, binary.LittleEndian, uint32(4))
	payload.Write([]byte{0xaa, 0xbb, 0xcc, 0xdd})
	payload.WriteString("data")
	_ = binary.Write(&payload, binary.LittleEndian, uint32(4))
	payload.Write([]byte{1, 2, 3, 4})
	wav := payload.Bytes()
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))

	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: wav},
	}
	provider := newGoogleTTSWithClient(client, WithGoogleTTSAudioEncoding(texttospeech.AudioEncoding_LINEAR16))

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if got := chunk.Frame.Data; !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("chunk data = %v, want WAV data chunk without extended header bytes", got)
	}
}

func TestGoogleTTSSynthesizeStripsHeaderOnlyWAV(t *testing.T) {
	payload := make([]byte, 44)
	copy(payload[0:4], "RIFF")
	copy(payload[8:12], "WAVE")
	client := &fakeGoogleTTSClient{
		response: &texttospeech.SynthesizeSpeechResponse{AudioContent: payload},
	}
	provider := newGoogleTTSWithClient(client)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil no-audio error", audio)
	}
	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIError", err, err)
	}
	if !apiErr.Retryable {
		t.Fatal("APIError retryable = false, want true")
	}
	if !strings.Contains(apiErr.Error(), "no audio frames were pushed for text: hello") {
		t.Fatalf("APIError = %q, want reference no-audio message", apiErr.Error())
	}
}

func TestGoogleTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &googleTTSChunkedStream{
		data: []byte{1, 2},
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next audio error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first Next = %+v, want audio frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final error = %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next = %+v, want final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func assertGoogleDecodedOpusFrame(t *testing.T, audio *tts.SynthesizedAudio, opusData []byte) {
	t.Helper()
	if audio == nil || audio.Frame == nil {
		t.Fatalf("audio = %+v, want decoded opus audio frame", audio)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want decoded opus sample rate 48000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want decoded opus mono", audio.Frame.NumChannels)
	}
	if audio.Frame.SamplesPerChannel == 0 {
		t.Fatal("decoded opus frame has no samples")
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded opus frame is empty")
	}
	prefixLen := min(len(audio.Frame.Data), len(opusData))
	if bytes.Equal(audio.Frame.Data[:prefixLen], opusData[:prefixLen]) {
		t.Fatal("frame data still contains compressed opus bytes")
	}
}

func requireGoogleTTSSynthesizeRequest(t *testing.T, stream tts.ChunkedStream, client *fakeGoogleTTSClient) *texttospeech.SynthesizeSpeechRequest {
	t.Helper()
	googleStream, ok := stream.(*googleTTSChunkedStream)
	if !ok {
		t.Fatalf("stream type = %T, want *googleTTSChunkedStream", stream)
	}
	if err := googleStream.ensureResponse(); err != nil {
		t.Fatalf("ensure synthesize response returned error: %v", err)
	}
	if client.request == nil {
		t.Fatal("SynthesizeSpeech request = nil")
	}
	return client.request
}

type fakeGoogleTTSClient struct {
	request                    *texttospeech.SynthesizeSpeechRequest
	synthesizeCalls            int
	response                   *texttospeech.SynthesizeSpeechResponse
	blockSynthesize            chan struct{}
	synthesizeErrCh            chan error
	synthesizeDoneCh           chan struct{}
	stream                     *fakeGoogleTTSStream
	streams                    []*fakeGoogleTTSStream
	streamCalls                int
	blockStreamingSynthesize   chan struct{}
	unblockStreamingSynthesize chan struct{}
	streamingErrCh             chan error
	err                        error
}

func (c *fakeGoogleTTSClient) SynthesizeSpeech(ctx context.Context, req *texttospeech.SynthesizeSpeechRequest, opts ...gax.CallOption) (*texttospeech.SynthesizeSpeechResponse, error) {
	c.synthesizeCalls++
	c.request = req
	if c.blockSynthesize != nil {
		close(c.blockSynthesize)
		err := ctx.Err()
		if err == nil {
			<-ctx.Done()
			err = ctx.Err()
		}
		if c.synthesizeErrCh != nil {
			c.synthesizeErrCh <- err
		}
		if c.synthesizeDoneCh != nil {
			close(c.synthesizeDoneCh)
		}
		return nil, err
	}
	return c.response, c.err
}

func (c *fakeGoogleTTSClient) StreamingSynthesize(ctx context.Context, opts ...gax.CallOption) (texttospeech.TextToSpeech_StreamingSynthesizeClient, error) {
	c.streamCalls++
	if c.blockStreamingSynthesize != nil {
		close(c.blockStreamingSynthesize)
		select {
		case <-ctx.Done():
			if c.streamingErrCh != nil {
				c.streamingErrCh <- ctx.Err()
			}
			return nil, ctx.Err()
		case <-c.unblockStreamingSynthesize:
		}
	}
	if c.err != nil {
		return nil, c.err
	}
	if len(c.streams) > 0 {
		stream := c.streams[0]
		c.streams = c.streams[1:]
		c.stream = stream
		stream.ctx = ctx
		return stream, nil
	}
	if c.stream != nil {
		c.stream.ctx = ctx
	}
	return c.stream, nil
}

type fakeGoogleTTSStream struct {
	grpc.ClientStream
	ctx                context.Context
	sent               []*texttospeech.StreamingSynthesizeRequest
	responses          []*texttospeech.StreamingSynthesizeResponse
	recvBlock          chan struct{}
	recvErr            error
	closed             bool
	closeErr           error
	sendErrAfterConfig error
	sendBlock          chan struct{}
	sendRelease        chan struct{}
}

func (s *fakeGoogleTTSStream) Send(req *texttospeech.StreamingSynthesizeRequest) error {
	s.sent = append(s.sent, req)
	if req.GetInput() != nil && s.sendErrAfterConfig != nil {
		return s.sendErrAfterConfig
	}
	if req.GetInput() != nil && s.sendBlock != nil {
		close(s.sendBlock)
		<-s.sendRelease
		if s.closed {
			return io.ErrClosedPipe
		}
	}
	return nil
}

func (s *fakeGoogleTTSStream) Recv() (*texttospeech.StreamingSynthesizeResponse, error) {
	if s.recvBlock != nil {
		select {
		case <-s.recvBlock:
		case <-s.ctx.Done():
		}
		s.recvBlock = nil
	}
	if len(s.responses) == 0 {
		if s.recvErr != nil {
			return nil, s.recvErr
		}
		return nil, io.EOF
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	return resp, nil
}

func (s *fakeGoogleTTSStream) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeGoogleTTSStream) Trailer() metadata.MD         { return nil }
func (s *fakeGoogleTTSStream) CloseSend() error {
	s.closed = true
	return s.closeErr
}
func (s *fakeGoogleTTSStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}
func (s *fakeGoogleTTSStream) SendMsg(m any) error { return nil }
func (s *fakeGoogleTTSStream) RecvMsg(m any) error { return nil }

type fakeGoogleTTSAudioStreamDecoder struct {
	closeCalls     int
	closeErr       error
	nextStarted    chan struct{}
	nextRelease    chan struct{}
	nextErr        error
	secondCloseErr error
}

func (d *fakeGoogleTTSAudioStreamDecoder) Push([]byte) {}
func (d *fakeGoogleTTSAudioStreamDecoder) EndInput()   {}
func (d *fakeGoogleTTSAudioStreamDecoder) Next() (*model.AudioFrame, error) {
	if d.nextStarted != nil {
		close(d.nextStarted)
		d.nextStarted = nil
	}
	if d.nextRelease != nil {
		<-d.nextRelease
	}
	if d.nextErr != nil {
		return nil, d.nextErr
	}
	return nil, io.EOF
}
func (d *fakeGoogleTTSAudioStreamDecoder) Close() error {
	d.closeCalls++
	if d.closeCalls > 1 {
		return d.secondCloseErr
	}
	return d.closeErr
}
