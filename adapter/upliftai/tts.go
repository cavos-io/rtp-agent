package upliftai

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/tokenize"
)

const (
	defaultUpliftAIVoiceID    = "v_meklc281"
	defaultUpliftAISampleRate = 22050
	defaultUpliftAIFormat     = "MP3_22050_32"
	defaultUpliftAIBaseURL    = "https://api.upliftai.org/v1/tts"
)

type UpliftAITTS struct {
	apiKey                    string
	voice                     string
	outputFormat              string
	baseURL                   string
	phraseReplacementConfigID string
	mu                        sync.Mutex
	closed                    bool
	streams                   map[io.Closer]struct{}
}

type UpliftAITTSOption func(*UpliftAITTS)
type UpliftAITTSUpdateOption func(*upliftAITTSUpdateOptions)

type upliftAITTSUpdateOptions struct {
	voiceID      *string
	outputFormat *string
}

func WithUpliftAIBaseURL(baseURL string) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.baseURL = baseURL
	}
}

func WithUpliftAIOutputFormat(outputFormat string) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.outputFormat = outputFormat
	}
}

func WithUpliftAIPhraseReplacementConfigID(configID string) UpliftAITTSOption {
	return func(t *UpliftAITTS) {
		t.phraseReplacementConfigID = configID
	}
}

func WithUpliftAIUpdateVoiceID(voiceID string) UpliftAITTSUpdateOption {
	return func(opts *upliftAITTSUpdateOptions) {
		opts.voiceID = &voiceID
	}
}

func WithUpliftAIUpdateOutputFormat(outputFormat string) UpliftAITTSUpdateOption {
	return func(opts *upliftAITTSUpdateOptions) {
		opts.outputFormat = &outputFormat
	}
}

func NewUpliftAITTS(apiKey string, voice string, opts ...UpliftAITTSOption) *UpliftAITTS {
	if apiKey == "" {
		apiKey = os.Getenv("UPLIFTAI_API_KEY")
	}
	if voice == "" {
		voice = defaultUpliftAIVoiceID
	}
	baseURL := os.Getenv("UPLIFTAI_BASE_URL")
	if baseURL == "" {
		baseURL = defaultUpliftAIBaseURL
	}
	tts := &UpliftAITTS{
		apiKey:       apiKey,
		voice:        voice,
		outputFormat: defaultUpliftAIFormat,
		baseURL:      baseURL,
		streams:      make(map[io.Closer]struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(tts)
		}
	}
	return tts
}

func (t *UpliftAITTS) Label() string { return "upliftai.TTS" }
func (t *UpliftAITTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *UpliftAITTS) SampleRate() int  { return defaultUpliftAISampleRate }
func (t *UpliftAITTS) NumChannels() int { return 1 }

func (t *UpliftAITTS) UpdateOptions(opts ...UpliftAITTSUpdateOption) {
	var update upliftAITTSUpdateOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&update)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if update.voiceID != nil {
		t.voice = *update.voiceID
	}
	if update.outputFormat != nil {
		t.outputFormat = *update.outputFormat
	}
}

func (t *UpliftAITTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if t.apiKey == "" {
		return nil, fmt.Errorf("API key is required, either as argument or set UPLIFTAI_API_KEY environment variable")
	}

	stream := &upliftAITTSChunkedStream{
		owner: t,
		ctx:   ctx,
		text:  text,
	}
	if !t.registerStream(stream) {
		_ = stream.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *UpliftAITTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if t.isClosed() {
		return nil, io.ErrClosedPipe
	}
	stream := newUpliftAITTSSynthesizeStream(t, ctx)
	if !t.registerStream(stream) {
		_ = stream.Close()
		return nil, io.ErrClosedPipe
	}
	return stream, nil
}

func (t *UpliftAITTS) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	streams := make([]io.Closer, 0, len(t.streams))
	for stream := range t.streams {
		streams = append(streams, stream)
	}
	t.streams = make(map[io.Closer]struct{})
	t.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (t *UpliftAITTS) isClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *UpliftAITTS) registerStream(stream io.Closer) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	t.streams[stream] = struct{}{}
	return true
}

func (t *UpliftAITTS) unregisterStream(stream io.Closer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.streams, stream)
}

func (t *UpliftAITTS) requestOptions() (baseURL string, voiceID string, outputFormat string, phraseReplacementConfigID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.baseURL, t.voice, t.outputFormat, t.phraseReplacementConfigID
}

type upliftAITTSSynthesizeStream struct {
	owner  *UpliftAITTS
	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	buf       strings.Builder
	inputCh   chan string
	eventCh   chan upliftAITTSStreamResult
	doneCh    chan struct{}
	active    tts.ChunkedStream
	closed    bool
	inputDone bool
	once      sync.Once
}

type upliftAITTSStreamResult struct {
	audio *tts.SynthesizedAudio
	err   error
}

func newUpliftAITTSSynthesizeStream(owner *UpliftAITTS, ctx context.Context) *upliftAITTSSynthesizeStream {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	stream := &upliftAITTSSynthesizeStream{
		owner:   owner,
		ctx:     ctx,
		cancel:  cancel,
		inputCh: make(chan string, 100),
		eventCh: make(chan upliftAITTSStreamResult, 100),
		doneCh:  make(chan struct{}),
	}
	go stream.run()
	return stream
}

func (s *upliftAITTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputDone {
		return io.ErrClosedPipe
	}
	s.buf.WriteString(text)
	return nil
}

func (s *upliftAITTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.inputDone {
		return nil
	}
	text := s.buf.String()
	if text == "" {
		return nil
	}
	text = s.formatSegmentText(text)
	if text == "" {
		s.buf.Reset()
		return nil
	}
	s.buf.Reset()
	select {
	case s.inputCh <- text:
		return nil
	case <-s.doneCh:
		return io.ErrClosedPipe
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *upliftAITTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputDone {
		return nil
	}
	text := s.buf.String()
	s.buf.Reset()
	if text != "" {
		text = s.formatSegmentText(text)
		if text == "" {
			s.inputDone = true
			close(s.inputCh)
			return nil
		}
		select {
		case s.inputCh <- text:
		case <-s.doneCh:
			return io.ErrClosedPipe
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
	}
	s.inputDone = true
	close(s.inputCh)
	return nil
}

func (s *upliftAITTSSynthesizeStream) formatSegmentText(text string) string {
	wordTokens := tokenize.SplitWords(text, false, false, false)
	words := make([]string, 0, len(wordTokens))
	for _, word := range wordTokens {
		words = append(words, word.Token)
	}
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " ")
}

func (s *upliftAITTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, io.EOF
		}
		s.mu.Unlock()

		select {
		case result, ok := <-s.eventCh:
			if !ok {
				return nil, io.EOF
			}
			if result.err != nil {
				return nil, result.err
			}
			return result.audio, nil
		case <-s.ctx.Done():
			return nil, io.EOF
		}
	}
}

func (s *upliftAITTSSynthesizeStream) run() {
	defer close(s.doneCh)
	defer close(s.eventCh)
	for {
		select {
		case <-s.ctx.Done():
			return
		case text, ok := <-s.inputCh:
			if !ok {
				return
			}
			if err := s.runSegment(text); err != nil {
				s.sendResult(nil, err)
				return
			}
		}
	}
}

func (s *upliftAITTSSynthesizeStream) runSegment(text string) error {
	stream, err := s.owner.Synthesize(s.ctx, text)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = stream.Close()
		return nil
	}
	s.active = stream
	s.mu.Unlock()
	defer func() {
		_ = stream.Close()
		s.mu.Lock()
		if s.active == stream {
			s.active = nil
		}
		s.mu.Unlock()
	}()

	for {
		audio, err := stream.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if audio == nil {
			continue
		}
		if !s.sendResult(audio, nil) {
			return nil
		}
	}
}

func (s *upliftAITTSSynthesizeStream) sendResult(audio *tts.SynthesizedAudio, err error) bool {
	select {
	case s.eventCh <- upliftAITTSStreamResult{audio: audio, err: err}:
		return true
	case <-s.ctx.Done():
		return false
	}
}

func (s *upliftAITTSSynthesizeStream) Close() error {
	var closeErr error
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		if !s.inputDone {
			s.inputDone = true
			close(s.inputCh)
		}
		active := s.active
		s.active = nil
		s.mu.Unlock()

		s.cancel()
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
		if active != nil {
			closeErr = active.Close()
		}
	})
	return closeErr
}

type upliftAITTSChunkedStream struct {
	owner        *UpliftAITTS
	ctx          context.Context
	text         string
	resp         *http.Response
	once         sync.Once
	err          error
	decoder      codecs.AudioStreamDecoder
	outputFormat string
	started      bool
	hasAudio     bool
	pendingFinal bool
	finalSent    bool
	closed       bool
}

func (s *upliftAITTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.closed || s.finalSent {
		return nil, io.EOF
	}
	if err := s.ensureResponse(); err != nil {
		return nil, err
	}
	if s.resp == nil || s.resp.Body == nil {
		return nil, io.EOF
	}
	if strings.HasPrefix(s.currentOutputFormat(), "MP3") {
		return s.nextDecodedMP3()
	}
	if strings.HasPrefix(s.currentOutputFormat(), "WAV") {
		return s.nextDecodedWAV()
	}
	if s.currentOutputFormat() == "ULAW_8000_8" {
		return s.nextDecodedULaw()
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	buf := make([]byte, 4096)
	for {
		n, err := s.resp.Body.Read(buf)
		if n > 0 {
			if err == io.EOF {
				s.pendingFinal = true
			}
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              buf[:n],
					SampleRate:        defaultUpliftAISampleRate,
					NumChannels:       1,
					SamplesPerChannel: uint32(n / 2),
				},
			}, nil
		}
		if err != nil {
			if err == io.EOF {
				if !s.finalSent {
					s.finalSent = true
					return &tts.SynthesizedAudio{IsFinal: true}, nil
				}
				return nil, io.EOF
			}
			return nil, upliftAITTSReadError("UpliftAI TTS stream read failed", err)
		}
	}
}

func (s *upliftAITTSChunkedStream) ensureResponse() error {
	if s.resp != nil || s.owner == nil {
		return nil
	}
	baseURL, voiceID, outputFormat, phraseReplacementConfigID := s.owner.requestOptions()
	s.outputFormat = outputFormat
	if err := validateUpliftAIOutputFormat(outputFormat); err != nil {
		return llm.NewAPIConnectionError(err.Error())
	}
	reqBody := map[string]interface{}{
		"text":         s.text,
		"voiceId":      voiceID,
		"outputFormat": outputFormat,
	}
	if phraseReplacementConfigID != "" {
		reqBody["phraseReplacementConfigId"] = phraseReplacementConfigID
	}
	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(s.ctx, "POST", baseURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.owner.apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS request failed: %v", err))
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return llm.NewAPIStatusError("UpliftAI TTS request failed", resp.StatusCode, "", string(respBody))
	}
	s.resp = resp
	return nil
}

func (s *upliftAITTSChunkedStream) currentOutputFormat() string {
	if s.outputFormat != "" {
		return s.outputFormat
	}
	if s.owner == nil {
		return ""
	}
	_, _, outputFormat, _ := s.owner.requestOptions()
	return outputFormat
}

func validateUpliftAIOutputFormat(outputFormat string) error {
	switch {
	case outputFormat == "PCM_22050_16":
		return nil
	case outputFormat == "WAV_22050_16", outputFormat == "WAV_22050_32":
		return nil
	case strings.HasPrefix(outputFormat, "MP3"):
		return nil
	case strings.HasPrefix(outputFormat, "OGG"):
		return nil
	case outputFormat == "ULAW_8000_8":
		return nil
	default:
		return fmt.Errorf("unsupported output format: %s", outputFormat)
	}
}

func (s *upliftAITTSChunkedStream) nextDecodedMP3() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, upliftAITTSReadError("UpliftAI TTS MP3 read failed", err)
		}
		if len(data) == 0 {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		s.hasAudio = true
		decoder := codecs.NewMP3AudioStreamDecoder()
		s.decoder = decoder
		go func() {
			decoder.Push(data)
			decoder.EndInput()
		}()
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			if s.hasAudio && !s.finalSent {
				s.finalSent = true
				return &tts.SynthesizedAudio{IsFinal: true}, nil
			}
			return nil, io.EOF
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS MP3 decode failed: %v", err))
	}
	frame = upliftAIDownmixToMono(frame)
	if frame.SampleRate != defaultUpliftAISampleRate {
		resampled, err := coreaudio.ResampleAudioFrame(frame, defaultUpliftAISampleRate)
		if err != nil {
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS MP3 resample failed: %v", err))
		}
		frame = resampled
	}
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func (s *upliftAITTSChunkedStream) nextDecodedWAV() (*tts.SynthesizedAudio, error) {
	if s.finalSent {
		return nil, io.EOF
	}
	if !s.started {
		s.started = true
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, upliftAITTSReadError("UpliftAI TTS WAV read failed", err)
		}
		if len(data) == 0 {
			s.finalSent = true
			return &tts.SynthesizedAudio{IsFinal: true}, nil
		}
		frame, err := decodeUpliftAIWAVPCM16(data)
		if err != nil {
			return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS WAV decode failed: %v", err))
		}
		frame = upliftAIDownmixToMono(frame)
		if frame.SampleRate != defaultUpliftAISampleRate {
			resampled, err := coreaudio.ResampleAudioFrame(frame, defaultUpliftAISampleRate)
			if err != nil {
				return nil, llm.NewAPIConnectionError(fmt.Sprintf("UpliftAI TTS WAV resample failed: %v", err))
			}
			frame = resampled
		}
		s.hasAudio = true
		s.pendingFinal = true
		return &tts.SynthesizedAudio{Frame: frame}, nil
	}
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	return nil, io.EOF
}

func decodeUpliftAIWAVPCM16(data []byte) (*model.AudioFrame, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("invalid upliftai wav data")
	}
	offset := 12
	var sampleRate uint32
	var channels uint16
	var bitsPerSample uint16
	var pcm []byte
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if offset+chunkSize > len(data) {
			return nil, fmt.Errorf("invalid upliftai wav chunk size")
		}
		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("invalid upliftai wav fmt chunk")
			}
			audioFormat := binary.LittleEndian.Uint16(data[offset : offset+2])
			channels = binary.LittleEndian.Uint16(data[offset+2 : offset+4])
			sampleRate = binary.LittleEndian.Uint32(data[offset+4 : offset+8])
			bitsPerSample = binary.LittleEndian.Uint16(data[offset+14 : offset+16])
			if audioFormat != 1 || bitsPerSample != 16 {
				return nil, fmt.Errorf("unsupported upliftai wav format: audio_format=%d bits_per_sample=%d", audioFormat, bitsPerSample)
			}
		case "data":
			pcm = bytes.Clone(data[offset : offset+chunkSize])
		}
		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}
	if sampleRate == 0 || channels == 0 || bitsPerSample == 0 {
		return nil, fmt.Errorf("missing upliftai wav format metadata")
	}
	if pcm == nil {
		return nil, fmt.Errorf("missing upliftai wav data chunk")
	}
	return &model.AudioFrame{
		Data:              pcm,
		SampleRate:        sampleRate,
		NumChannels:       uint32(channels),
		SamplesPerChannel: uint32(len(pcm) / int(channels) / 2),
	}, nil
}

func (s *upliftAITTSChunkedStream) nextDecodedULaw() (*tts.SynthesizedAudio, error) {
	if s.pendingFinal {
		s.pendingFinal = false
		s.finalSent = true
		return &tts.SynthesizedAudio{IsFinal: true}, nil
	}
	buf := make([]byte, 4096)
	for {
		n, err := s.resp.Body.Read(buf)
		if n > 0 {
			if err == io.EOF {
				s.pendingFinal = true
			}
			data := decodeUpliftAIMuLaw(buf[:n])
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              data,
					SampleRate:        8000,
					NumChannels:       1,
					SamplesPerChannel: uint32(len(data) / 2),
				},
			}, nil
		}
		if err != nil {
			if err == io.EOF {
				if !s.finalSent {
					s.finalSent = true
					return &tts.SynthesizedAudio{IsFinal: true}, nil
				}
				return nil, io.EOF
			}
			return nil, upliftAITTSReadError("UpliftAI TTS mu-law read failed", err)
		}
	}
}

func upliftAITTSReadError(prefix string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return llm.NewAPITimeoutError(fmt.Sprintf("%s: %v", prefix, err))
	}
	return llm.NewAPIConnectionError(fmt.Sprintf("%s: %v", prefix, err))
}

func decodeUpliftAIMuLaw(data []byte) []byte {
	pcm := make([]byte, len(data)*2)
	for i, encoded := range data {
		u := ^encoded
		sign := 1
		if u&0x80 != 0 {
			sign = -1
		}
		exponent := int((u >> 4) & 0x07)
		mantissa := int(u & 0x0f)
		sample := ((mantissa << 3) + 0x84) << exponent
		value := int16(sign * (sample - 0x84))
		pcm[i*2] = byte(value)
		pcm[i*2+1] = byte(value >> 8)
	}
	return pcm
}

func upliftAIDownmixToMono(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil || frame.NumChannels <= 1 {
		return frame
	}
	channels := int(frame.NumChannels)
	sampleCount := len(frame.Data) / (2 * channels)
	data := make([]byte, sampleCount*2)
	for i := 0; i < sampleCount; i++ {
		sum := 0
		for ch := 0; ch < channels; ch++ {
			offset := (i*channels + ch) * 2
			sum += int(int16(binary.LittleEndian.Uint16(frame.Data[offset:])))
		}
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(sum/channels)))
	}
	clone := *frame
	clone.Data = data
	clone.NumChannels = 1
	clone.SamplesPerChannel = uint32(sampleCount)
	return &clone
}

func (s *upliftAITTSChunkedStream) Close() error {
	s.once.Do(func() {
		s.closed = true
		if s.owner != nil {
			s.owner.unregisterStream(s)
		}
		if s.resp != nil && s.resp.Body != nil {
			s.err = s.resp.Body.Close()
		}
		if s.decoder != nil {
			_ = s.decoder.Close()
		}
	})
	return s.err
}
