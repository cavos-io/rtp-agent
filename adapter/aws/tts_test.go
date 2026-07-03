package aws

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/polly"
	"github.com/aws/aws-sdk-go-v2/service/polly/types"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestAWSTTSDefaultsMatchReference(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "")

	if provider.voice != types.VoiceIdRuth {
		t.Fatalf("voice = %q, want Ruth", provider.voice)
	}
	if provider.engine != types.EngineGenerative {
		t.Fatalf("engine = %q, want generative", provider.engine)
	}
	if provider.outputFormat != types.OutputFormatMp3 {
		t.Fatalf("output format = %q, want mp3", provider.outputFormat)
	}
	if provider.textType != types.TextTypeText {
		t.Fatalf("text type = %q, want text", provider.textType)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.Label() != "aws.TTS" {
		t.Fatalf("Label = %q, want aws.TTS", provider.Label())
	}
	if provider.Model() != "generative" {
		t.Fatalf("Model = %q, want generative", provider.Model())
	}
	if provider.Provider() != "Amazon Polly" {
		t.Fatalf("Provider = %q, want Amazon Polly", provider.Provider())
	}
	if provider.SampleRate() != 16000 {
		t.Fatalf("SampleRate = %d, want 16000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("NumChannels = %d, want 1", provider.NumChannels())
	}
	if provider.Capabilities().Streaming {
		t.Fatal("Streaming = true, want false for Polly synthesize")
	}
}

func TestNewAWSTTSUsesReferenceDefaults(t *testing.T) {
	provider, err := NewAWSTTS(context.Background(), "", "")
	if err != nil {
		t.Fatalf("NewAWSTTS error = %v, want nil with SDK default config", err)
	}
	if provider.voice != types.VoiceIdRuth {
		t.Fatalf("voice = %q, want Ruth", provider.voice)
	}
	if provider.Model() != "generative" {
		t.Fatalf("Model = %q, want generative", provider.Model())
	}
	if provider.client == nil || provider.client.Options().RetryMaxAttempts != 1 {
		t.Fatalf("RetryMaxAttempts = %v, want reference single provider attempt", provider.client.Options().RetryMaxAttempts)
	}
}

func TestAWSTTSSynthesizeInputUsesProviderOptions(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "Matthew",
		WithAWSTTSEngine(types.EngineNeural),
		WithAWSTTSTextType(types.TextTypeSsml),
		WithAWSTTSLanguage(types.LanguageCodeEnUs),
		WithAWSTTSSampleRate(24000),
	)

	input := buildAWSSynthesizeSpeechInput(provider, "<speak>Hello</speak>")

	if input.Text == nil || *input.Text != "<speak>Hello</speak>" {
		t.Fatalf("text = %v, want SSML input", input.Text)
	}
	if input.VoiceId != types.VoiceIdMatthew {
		t.Fatalf("voice = %q, want Matthew", input.VoiceId)
	}
	if input.Engine != types.EngineNeural {
		t.Fatalf("engine = %q, want neural", input.Engine)
	}
	if input.OutputFormat != types.OutputFormatMp3 {
		t.Fatalf("output format = %q, want mp3", input.OutputFormat)
	}
	if input.TextType != types.TextTypeSsml {
		t.Fatalf("text type = %q, want ssml", input.TextType)
	}
	if input.LanguageCode != types.LanguageCodeEnUs {
		t.Fatalf("language = %q, want en-US", input.LanguageCode)
	}
	if input.SampleRate == nil || *input.SampleRate != "24000" {
		t.Fatalf("sample rate = %v, want 24000", input.SampleRate)
	}
}

func TestAWSTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "")

	provider.UpdateOptions(
		WithAWSTTSVoice(types.VoiceIdJoanna),
		WithAWSTTSEngine(types.EngineStandard),
		WithAWSTTSTextType(types.TextTypeSsml),
	)

	if provider.voice != types.VoiceIdJoanna {
		t.Fatalf("voice = %q, want Joanna", provider.voice)
	}
	if provider.engine != types.EngineStandard {
		t.Fatalf("engine = %q, want standard", provider.engine)
	}
	if provider.textType != types.TextTypeSsml {
		t.Fatalf("text type = %q, want ssml", provider.textType)
	}
}

func TestAWSTTSUpdateOptionsKeepsReferenceSampleRate(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "", WithAWSTTSSampleRate(16000))

	provider.UpdateOptions(WithAWSTTSSampleRate(24000))

	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want unchanged reference sample rate 16000", provider.sampleRate)
	}
}

func TestAWSTTSSynthesizeDefersReferenceRequestUntilNext(t *testing.T) {
	var requests int
	client := polly.New(polly.Options{
		Region: "us-east-1",
		Credentials: awssdk.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			"test-access-key",
			"test-secret-key",
			"",
		)),
		HTTPClient: awsHTTPClientFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("polly should not be called before Next")
		}),
	})
	provider := newAWSTTSWithClient(client, "")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v, want lazy stream", err)
	}
	if stream == nil {
		t.Fatal("Synthesize stream = nil, want lazy stream")
	}
	if requests != 0 {
		t.Fatalf("provider requests after Synthesize = %d, want none until Next", requests)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close before Next error = %v", err)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after preflight Close error = %v, want EOF", err)
	}
	if requests != 0 {
		t.Fatalf("provider requests after Close before Next = %d, want none", requests)
	}
}

func TestAWSTTSLazySynthesizeSnapshotsReferenceOptions(t *testing.T) {
	var requestBody string
	client := polly.New(polly.Options{
		Region:           "us-east-1",
		RetryMaxAttempts: 1,
		Credentials: awssdk.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			"test-access-key",
			"test-secret-key",
			"",
		)),
		HTTPClient: awsHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(req.Body)
			requestBody = string(data)
			return nil, errors.New("stop after capture")
		}),
	})
	provider := newAWSTTSWithClient(client, "Ruth",
		WithAWSTTSEngine(types.EngineGenerative),
		WithAWSTTSTextType(types.TextTypeText),
	)

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v, want lazy stream", err)
	}
	provider.UpdateOptions(
		WithAWSTTSVoice(types.VoiceIdJoanna),
		WithAWSTTSEngine(types.EngineStandard),
		WithAWSTTSTextType(types.TextTypeSsml),
	)

	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want captured APIConnectionError", err, err)
	}
	if !strings.Contains(requestBody, "Ruth") || !strings.Contains(requestBody, "generative") || !strings.Contains(requestBody, "text") {
		t.Fatalf("request body = %s, want original voice/engine/text type", requestBody)
	}
	if strings.Contains(requestBody, "Joanna") || strings.Contains(requestBody, "standard") || strings.Contains(requestBody, "ssml") {
		t.Fatalf("request body = %s, want no updated options", requestBody)
	}
}

func TestAWSTTSChunkedStreamUsesReferenceSnapshotSampleRate(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	provider := newAWSTTSWithClient(nil, "", WithAWSTTSSampleRate(16000))
	stream := &awsTTSChunkedStream{
		stream:   io.NopCloser(bytes.NewReader(mp3Data)),
		provider: provider,
		options:  provider.snapshotOptions(),
	}
	defer stream.Close()

	provider.UpdateOptions(WithAWSTTSSampleRate(24000))

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want stream snapshot sample rate 16000", audio.Frame.SampleRate)
	}
}

func TestAWSTTSChunkedStreamDecodesReferenceMP3Audio(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	stream := &awsTTSChunkedStream{
		stream:   io.NopCloser(bytes.NewReader(mp3Data)),
		provider: newAWSTTSWithClient(nil, ""),
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want reference provider rate 16000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 1 {
		t.Fatalf("channels = %d, want reference mono output", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	if bytes.Equal(audio.Frame.Data, mp3Data[:len(audio.Frame.Data)]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}
}

func TestAWSTTSChunkedStreamCarriesReferenceRequestID(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	stream := &awsTTSChunkedStream{
		stream:    io.NopCloser(bytes.NewReader(mp3Data)),
		requestID: "polly-request-1",
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.RequestID != "polly-request-1" {
		t.Fatalf("audio RequestID = %q, want Polly request id", audio.RequestID)
	}
	for range 5000 {
		audio, err = stream.Next()
		if err != nil {
			t.Fatalf("Next returned error before final marker: %v", err)
		}
		if audio.IsFinal {
			if audio.RequestID != "polly-request-1" {
				t.Fatalf("final RequestID = %q, want Polly request id", audio.RequestID)
			}
			return
		}
	}
	t.Fatal("stream did not emit final marker")
}

func TestAWSTTSChunkedStreamYieldsAudioBeforeResponseEOF(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	body := newBlockingAWSReadCloser(mp3Data)
	stream := &awsTTSChunkedStream{
		stream:   body,
		provider: newAWSTTSWithClient(nil, ""),
	}
	defer stream.Close()

	resultCh := make(chan struct {
		audio *tts.SynthesizedAudio
		err   error
	}, 1)
	go func() {
		audio, err := stream.Next()
		resultCh <- struct {
			audio *tts.SynthesizedAudio
			err   error
		}{audio: audio, err: err}
	}()

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("Next error = %v, want first audio before response EOF", got.err)
		}
		if got.audio == nil || got.audio.Frame == nil || len(got.audio.Frame.Data) == 0 {
			t.Fatalf("Next audio = %+v, want decoded audio frame before response EOF", got.audio)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first audio before response EOF")
	}
}

func TestAWSTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}

	stream := &awsTTSChunkedStream{
		stream: io.NopCloser(bytes.NewReader(mp3Data)),
	}
	defer stream.Close()

	frames := 0
	for range 5000 {
		audio, err := stream.Next()
		if err != nil {
			t.Fatalf("Next returned error before final marker after %d frames: %v", frames, err)
		}
		if audio == nil {
			t.Fatalf("Next returned nil audio before final marker after %d frames", frames)
		}
		if audio.IsFinal {
			if frames == 0 {
				t.Fatal("final marker arrived before decoded audio")
			}
			if _, err := stream.Next(); err != io.EOF {
				t.Fatalf("Next after final marker err = %v, want EOF", err)
			}
			return
		}
		if len(audio.Frame.Data) == 0 {
			t.Fatalf("frame %d is empty", frames)
		}
		frames++
	}
	t.Fatalf("stream did not emit final marker after %d frames", frames)
}

func TestAWSTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &awsTTSChunkedStream{
		stream:    io.NopCloser(bytes.NewReader(nil)),
		requestID: "polly-request-empty",
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next = %+v, want final marker", audio)
	}
	if audio.RequestID != "polly-request-empty" {
		t.Fatalf("final RequestID = %q, want Polly request id", audio.RequestID)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestAWSTTSChunkedStreamReadFailureReturnsAPIConnectionError(t *testing.T) {
	stream := &awsTTSChunkedStream{
		stream: erroringAWSReadCloser{err: errors.New("polly read failed")},
	}
	defer stream.Close()

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil on read failure", audio)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestAWSTTSChunkedStreamReadDeadlineReturnsAPITimeoutError(t *testing.T) {
	stream := &awsTTSChunkedStream{
		stream: erroringAWSReadCloser{err: context.DeadlineExceeded},
	}
	defer stream.Close()

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil on read timeout", audio)
	}
	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next error = %T %v, want APITimeoutError", err, err)
	}
}

func TestAWSTTSChunkedStreamReadFailureAfterAudioIsReferenceNonRetryable(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	stream := &awsTTSChunkedStream{
		stream: &dataThenErrorAWSReadCloser{
			data: mp3Data,
			err:  errors.New("polly read failed"),
		},
	}
	defer stream.Close()

	var frames int
	for range 1000 {
		audio, err := stream.Next()
		if err != nil {
			var connectionErr *llm.APIConnectionError
			if !errors.As(err, &connectionErr) {
				t.Fatalf("Next error after %d frames = %T %v, want APIConnectionError", frames, err, err)
			}
			if frames == 0 {
				t.Fatal("read failure arrived before audio, want partial-audio failure")
			}
			if connectionErr.Retryable {
				t.Fatal("Retryable = true, want false after partial reference audio")
			}
			return
		}
		if audio == nil {
			t.Fatalf("Next returned nil audio after %d frames", frames)
		}
		if audio.Frame != nil {
			frames++
		}
	}
	t.Fatal("stream did not surface read failure")
}

func TestAWSTTSChunkedStreamDecodeFailureReturnsAPIConnectionError(t *testing.T) {
	stream := &awsTTSChunkedStream{
		stream: io.NopCloser(bytes.NewReader([]byte("not mp3 audio"))),
	}
	defer stream.Close()

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil on decode failure", audio)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestAWSTTSChunkedStreamReadFailureClosesReferenceStream(t *testing.T) {
	body := &countingErrorAWSReadCloser{err: errors.New("polly read failed")}
	provider := newAWSTTSWithClient(nil, "")
	stream := &awsTTSChunkedStream{
		stream:   body,
		provider: provider,
	}
	provider.registerStream(stream)

	audio, err := stream.Next()

	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil on read failure", audio)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1 after read failure", body.closed)
	}
	if len(provider.streams) != 0 {
		t.Fatalf("registered streams = %d, want stream unregistered after read failure", len(provider.streams))
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after read failure err = %v, want EOF", err)
	}
}

func TestAWSTTSSynthesizeRequiresConfiguredClient(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "")

	stream, err := provider.Synthesize(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Synthesize error = %v, want lazy stream", err)
	}
	if stream == nil {
		t.Fatal("Synthesize stream = nil, want lazy stream")
	}
	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil without client", audio)
	}
	if err == nil || !strings.Contains(err.Error(), "client is not configured") {
		t.Fatalf("Next error = %v, want configured-client error", err)
	}
}

func TestAWSTTSChunkedStreamNextReturnsAPIConnectionError(t *testing.T) {
	client := polly.New(polly.Options{
		Region: "us-east-1",
		Credentials: awssdk.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			"test-access-key",
			"test-secret-key",
			"",
		)),
		HTTPClient: awsHTTPClientFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("polly dial failed")
		}),
	})
	provider := newAWSTTSWithClient(client, "")

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v, want lazy stream", err)
	}
	if stream == nil {
		t.Fatal("Synthesize stream = nil, want lazy stream")
	}

	audio, err := stream.Next()
	if audio != nil {
		t.Fatalf("Next audio = %+v, want nil on provider failure", audio)
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestAWSTTSStreamReportsUnsupported(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "")

	_, err := provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Stream error = %v, want unsupported error", err)
	}
}

func TestAWSTTSChunkedStreamEOFAndClose(t *testing.T) {
	stream := &awsTTSChunkedStream{
		stream: io.NopCloser(bytes.NewReader(nil)),
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next err = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next = %+v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next err = %v, want EOF", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close err = %v, want nil", err)
	}

	empty := &awsTTSChunkedStream{}
	if err := empty.Close(); err != nil {
		t.Fatalf("empty Close err = %v, want nil", err)
	}
}

func TestAWSTTSProviderCloseClosesActiveStreams(t *testing.T) {
	body := &countingAWSReadCloser{}
	provider := newAWSTTSWithClient(nil, "")
	stream := &awsTTSChunkedStream{
		stream:   body,
		provider: provider,
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close err = %v", err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls = %d, want 1", body.closed)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after provider Close err = %v, want EOF", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("second Close err = %v", err)
	}
	if body.closed != 1 {
		t.Fatalf("body Close calls after second Close = %d, want 1", body.closed)
	}
}

func TestAWSTTSChunkedStreamCloseSuppressesBodyCloseError(t *testing.T) {
	stream := &awsTTSChunkedStream{
		stream: closeErrorAWSReadCloser{err: errors.New("body close failed")},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil for caller-owned cancellation cleanup", err)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestAWSTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close err = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want %v", err, io.ErrClosedPipe)
	}
	if stream != nil {
		t.Fatalf("Synthesize after Close stream = %#v, want nil", stream)
	}
}

type countingAWSReadCloser struct {
	closed int
}

func (c *countingAWSReadCloser) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (c *countingAWSReadCloser) Close() error {
	c.closed++
	if c.closed > 1 {
		return io.ErrClosedPipe
	}
	return nil
}

type erroringAWSReadCloser struct {
	err error
}

func (c erroringAWSReadCloser) Read([]byte) (int, error) {
	return 0, c.err
}

func (c erroringAWSReadCloser) Close() error {
	return nil
}

type dataThenErrorAWSReadCloser struct {
	data []byte
	err  error
	sent bool
}

func (c *dataThenErrorAWSReadCloser) Read(p []byte) (int, error) {
	if c.sent {
		return 0, c.err
	}
	c.sent = true
	return copy(p, c.data), nil
}

func (c *dataThenErrorAWSReadCloser) Close() error {
	return nil
}

type closeErrorAWSReadCloser struct {
	err error
}

func (c closeErrorAWSReadCloser) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (c closeErrorAWSReadCloser) Close() error {
	return c.err
}

type blockingAWSReadCloser struct {
	mu      sync.Mutex
	data    []byte
	sent    bool
	closed  bool
	closeCh chan struct{}
}

func newBlockingAWSReadCloser(data []byte) *blockingAWSReadCloser {
	return &blockingAWSReadCloser{data: append([]byte(nil), data...), closeCh: make(chan struct{})}
}

func (b *blockingAWSReadCloser) Read(p []byte) (int, error) {
	b.mu.Lock()
	if !b.sent {
		b.sent = true
		n := copy(p, b.data)
		b.mu.Unlock()
		return n, nil
	}
	b.mu.Unlock()
	<-b.closeCh
	return 0, io.EOF
}

func (b *blockingAWSReadCloser) Close() error {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		close(b.closeCh)
	}
	b.mu.Unlock()
	return nil
}

type countingErrorAWSReadCloser struct {
	err    error
	closed int
}

func (c *countingErrorAWSReadCloser) Read([]byte) (int, error) {
	return 0, c.err
}

func (c *countingErrorAWSReadCloser) Close() error {
	c.closed++
	return nil
}

type awsHTTPClientFunc func(*http.Request) (*http.Response, error)

func (f awsHTTPClientFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}
