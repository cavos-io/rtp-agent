package elevenlabs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	langutil "github.com/cavos-io/rtp-agent/library/utils/language"
	"github.com/gorilla/websocket"
)

const (
	defaultElevenLabsSTTBaseURL    = "https://api.elevenlabs.io/v1"
	defaultElevenLabsSTTModel      = "scribe_v1"
	defaultElevenLabsSTTSampleRate = 16000
	elevenLabsSTTAuthHeader        = "xi-api-key"
	elevenLabsSTTUsageInterval     = 5 * time.Second
	elevenLabsSTTKeepAliveInterval = 10 * time.Second
)

type ElevenLabsVADOptions struct {
	VADSilenceThresholdSecs *float64
	VADThreshold            *float64
	MinSpeechDurationMS     *int
	MinSilenceDurationMS    *int
}

type STT struct {
	apiKey            string
	baseURL           string
	modelID           string
	languageCode      string
	tagAudioEvents    bool
	includeTimestamps bool
	sampleRate        int
	serverVAD         *ElevenLabsVADOptions
	serverVADRefresh  bool
	keyterms          []string
	mu                sync.Mutex
	streams           map[*elevenLabsSTTStream]struct{}
	closed            bool
}

type STTOption func(*STT)

func WithElevenLabsSTTBaseURL(baseURL string) STTOption {
	return func(s *STT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithElevenLabsSTTModel(modelID string) STTOption {
	return func(s *STT) {
		if modelID != "" {
			s.modelID = modelID
		}
	}
}

func WithElevenLabsSTTLanguage(languageCode string) STTOption {
	return func(s *STT) {
		s.languageCode = langutil.NormalizeLanguage(languageCode)
	}
}

func WithElevenLabsSTTTagAudioEvents(enabled bool) STTOption {
	return func(s *STT) {
		s.tagAudioEvents = enabled
	}
}

func WithElevenLabsSTTIncludeTimestamps(enabled bool) STTOption {
	return func(s *STT) {
		s.includeTimestamps = enabled
	}
}

func WithElevenLabsSTTSampleRate(sampleRate int) STTOption {
	return func(s *STT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithElevenLabsSTTServerVAD(serverVAD ElevenLabsVADOptions) STTOption {
	return func(s *STT) {
		s.serverVAD = &serverVAD
		s.serverVADRefresh = true
	}
}

func WithElevenLabsSTTServerVADDisabled() STTOption {
	return func(s *STT) {
		s.serverVAD = nil
		s.serverVADRefresh = true
	}
}

func WithElevenLabsSTTKeyterms(keyterms []string) STTOption {
	return func(s *STT) {
		s.keyterms = keyterms
	}
}

func NewSTT(apiKey string, opts ...STTOption) *STT {
	provider := &STT{
		apiKey:         resolveElevenLabsAPIKey(apiKey),
		baseURL:        defaultElevenLabsSTTBaseURL,
		modelID:        defaultElevenLabsSTTModel,
		tagAudioEvents: true,
		sampleRate:     defaultElevenLabsSTTSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	provider.serverVADRefresh = false
	return provider
}

func (s *STT) Label() string { return "elevenlabs.STT" }
func (s *STT) Model() string { return s.modelID }
func (s *STT) Provider() string {
	return "ElevenLabs"
}
func (s *STT) InputSampleRate() uint32 {
	if s == nil || s.sampleRate <= 0 {
		return defaultElevenLabsSTTSampleRate
	}
	return uint32(s.sampleRate)
}
func (s *STT) Capabilities() stt.STTCapabilities {
	realtime := elevenLabsSTTIsRealtime(s.modelID)
	aligned := ""
	if realtime && s.includeTimestamps {
		aligned = "word"
	}
	return stt.STTCapabilities{
		Streaming:         realtime,
		InterimResults:    true,
		Diarization:       false,
		AlignedTranscript: aligned,
		OfflineRecognize:  true,
	}
}

func (s *STT) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	streams := make([]*elevenLabsSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = nil
	s.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (s *STT) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *STT) UpdateOptions(opts ...STTOption) {
	s.serverVADRefresh = false
	oldServerVAD := s.serverVAD
	for _, opt := range opts {
		opt(s)
	}
	s.mu.Lock()
	streams := make([]*elevenLabsSTTStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	serverVAD := s.serverVAD != nil
	refreshServerVAD := s.serverVADRefresh
	s.serverVADRefresh = false
	s.mu.Unlock()
	for _, stream := range streams {
		if refreshServerVAD || !elevenLabsVADOptionsEqual(oldServerVAD, s.serverVAD) {
			stream.reconnect(buildElevenLabsSTTStreamURL(s, stream.language()), buildElevenLabsSTTHeaders(s), serverVAD)
			continue
		}
		stream.setServerVAD(serverVAD)
	}
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateElevenLabsAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildElevenLabsSTTStreamURL(s, language), buildElevenLabsSTTHeaders(s))
	if err != nil {
		return nil, llm.NewAPIConnectionError("Failed to connect to ElevenLabs")
	}
	if s.isClosed() {
		conn.Close()
		return nil, io.ErrClosedPipe
	}
	streamCtx, cancel := context.WithCancel(ctx)
	streamNow := time.Now()
	stream := &elevenLabsSTTStream{
		conn:               conn,
		events:             make(chan *stt.SpeechEvent, 100),
		errCh:              make(chan error, 1),
		ctx:                streamCtx,
		cancel:             cancel,
		sampleRate:         s.sampleRate,
		usageLastFlush:     streamNow,
		usageFlushInterval: elevenLabsSTTUsageInterval,
		keepAliveInterval:  elevenLabsSTTKeepAliveInterval,
		state: &elevenLabsSTTStreamState{
			language:          resolveElevenLabsSTTLanguage(s, language),
			includeTimestamps: s.includeTimestamps,
			serverVAD:         s.serverVAD != nil,
			startTime:         float64(streamNow.UnixNano()) / 1e9,
		},
	}
	if !s.registerStream(stream) {
		return nil, io.ErrClosedPipe
	}
	go stream.readLoop()
	go stream.keepAliveLoop()
	return stream, nil
}

func (s *STT) registerStream(stream *elevenLabsSTTStream) bool {
	if s == nil || stream == nil {
		return false
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = stream.Close()
		return false
	}
	if s.streams == nil {
		s.streams = make(map[*elevenLabsSTTStream]struct{})
	}
	s.streams[stream] = struct{}{}
	stream.unregister = s.unregisterStream
	s.mu.Unlock()
	return true
}

func (s *STT) unregisterStream(stream *elevenLabsSTTStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateElevenLabsAPIKey(s.apiKey); err != nil {
		return nil, err
	}
	if language != "" {
		s.mu.Lock()
		s.languageCode = langutil.NormalizeLanguage(language)
		s.mu.Unlock()
	}

	audio := elevenLabsSTTWAVBytes(frames, uint32(s.sampleRate), 1)
	req, err := buildElevenLabsSTTRecognizeRequest(ctx, s, audio, language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		_, _, err := elevenLabsSTTStatusErrorBody(respBody)
		if err != nil {
			return nil, llm.NewAPIConnectionError(err.Error())
		}
		return nil, llm.NewAPIConnectionError("")
	}
	var result elevenLabsSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	return elevenLabsSTTSpeechEvent(resolveElevenLabsSTTLanguage(s, language), result), nil
}

func elevenLabsSTTStatusErrorBody(respBody []byte) (string, any, error) {
	var payload map[string]any
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return "", nil, err
	}
	if detail, _ := payload["detail"].(string); detail != "" {
		return detail, payload, nil
	}
	return "Unknown ElevenLabs error", payload, nil
}

func elevenLabsSTTWAVBytes(frames []*model.AudioFrame, defaultSampleRate uint32, defaultNumChannels uint32) []byte {
	sampleRate := defaultSampleRate
	numChannels := defaultNumChannels
	var pcm bytes.Buffer
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate != 0 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels != 0 {
			numChannels = frame.NumChannels
		}
		pcm.Write(frame.Data)
	}
	if sampleRate == 0 {
		sampleRate = 16000
	}
	if numChannels == 0 {
		numChannels = 1
	}
	data := pcm.Bytes()
	dataSize := uint32(len(data))
	byteRate := sampleRate * numChannels * 2
	blockAlign := numChannels * 2
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36)+dataSize)
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(&wav, binary.LittleEndian, sampleRate)
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, dataSize)
	wav.Write(data)
	return wav.Bytes()
}

func buildElevenLabsSTTRecognizeRequest(ctx context.Context, s *STT, audio []byte, language string) (*http.Request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	header.Set("Content-Type", "audio/x-wav")
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	if err := writer.WriteField("model_id", s.modelID); err != nil {
		return nil, err
	}
	if err := writer.WriteField("tag_audio_events", strconv.FormatBool(s.tagAudioEvents)); err != nil {
		return nil, err
	}
	requestLanguage := s.languageCode
	if language != "" {
		requestLanguage = langutil.NormalizeLanguage(language)
	}
	if requestLanguage != "" {
		if err := writer.WriteField("language_code", requestLanguage); err != nil {
			return nil, err
		}
	}
	for _, keyterm := range s.keyterms {
		if err := writer.WriteField("keyterms", keyterm); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.baseURL, "/")+"/speech-to-text", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set(elevenLabsSTTAuthHeader, s.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, nil
}

func buildElevenLabsSTTStreamURL(s *STT, language string) string {
	baseURL := strings.TrimRight(s.baseURL, "/")
	baseURL = strings.Replace(baseURL, "https://", "wss://", 1)
	baseURL = strings.Replace(baseURL, "http://", "ws://", 1)
	u, _ := url.Parse(baseURL + "/speech-to-text/realtime")
	q := u.Query()
	q.Set("model_id", s.modelID)
	q.Set("audio_format", fmt.Sprintf("pcm_%d", s.sampleRate))
	if s.serverVAD == nil {
		q.Set("commit_strategy", "manual")
	} else {
		q.Set("commit_strategy", "vad")
	}
	requestLanguage := resolveElevenLabsSTTLanguage(s, language)
	if requestLanguage == "" {
		q.Set("include_language_detection", "true")
	} else {
		q.Set("language_code", requestLanguage)
	}
	if s.includeTimestamps {
		q.Set("include_timestamps", "true")
	}
	if s.serverVAD != nil {
		if s.serverVAD.VADSilenceThresholdSecs != nil {
			q.Set("vad_silence_threshold_secs", formatElevenLabsFloat(*s.serverVAD.VADSilenceThresholdSecs))
		}
		if s.serverVAD.VADThreshold != nil {
			q.Set("vad_threshold", formatElevenLabsFloat(*s.serverVAD.VADThreshold))
		}
		if s.serverVAD.MinSpeechDurationMS != nil {
			q.Set("min_speech_duration_ms", strconv.Itoa(*s.serverVAD.MinSpeechDurationMS))
		}
		if s.serverVAD.MinSilenceDurationMS != nil {
			q.Set("min_silence_duration_ms", strconv.Itoa(*s.serverVAD.MinSilenceDurationMS))
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func buildElevenLabsSTTHeaders(s *STT) http.Header {
	headers := make(http.Header)
	headers.Set(elevenLabsSTTAuthHeader, s.apiKey)
	return headers
}

func buildElevenLabsSTTAudioChunkMessage(audio []byte, sampleRate int, commit bool) map[string]any {
	return map[string]any{
		"message_type":  "input_audio_chunk",
		"audio_base_64": base64.StdEncoding.EncodeToString(audio),
		"commit":        commit,
		"sample_rate":   sampleRate,
	}
}

func resolveElevenLabsSTTLanguage(s *STT, language string) string {
	if language != "" {
		return elevenLabsNormalizeSTTLanguage(language)
	}
	return s.languageCode
}

func elevenLabsSTTIsRealtime(modelID string) bool {
	return modelID == "scribe_v2_realtime"
}

func formatElevenLabsFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func elevenLabsVADOptionsEqual(a, b *ElevenLabsVADOptions) bool {
	if a == nil || b == nil {
		return a == b
	}
	return elevenLabsFloatPtrEqual(a.VADSilenceThresholdSecs, b.VADSilenceThresholdSecs) &&
		elevenLabsFloatPtrEqual(a.VADThreshold, b.VADThreshold) &&
		elevenLabsIntPtrEqual(a.MinSpeechDurationMS, b.MinSpeechDurationMS) &&
		elevenLabsIntPtrEqual(a.MinSilenceDurationMS, b.MinSilenceDurationMS)
}

func elevenLabsFloatPtrEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func elevenLabsIntPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

type elevenLabsSTTStream struct {
	conn               *websocket.Conn
	connVersion        int64
	events             chan *stt.SpeechEvent
	errCh              chan error
	mu                 sync.Mutex
	closed             bool
	inputEnded         bool
	ctx                context.Context
	cancel             context.CancelFunc
	sampleRate         int
	audioBuf           *audio.AudioByteStream
	audioDur           float64
	usageLastFlush     time.Time
	usageFlushInterval time.Duration
	keepAliveInterval  time.Duration
	state              *elevenLabsSTTStreamState
	rateGuard          stt.SampleRateGuard
	writeJSON          func(map[string]any) error
	unregister         func(*elevenLabsSTTStream)
	unregOnce          sync.Once
}

func (s *elevenLabsSTTStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	if err := s.rateGuard.Check(frame); err != nil {
		return err
	}
	if s.audioBuf == nil {
		s.audioBuf = audio.NewAudioByteStream(uint32(s.sampleRate), 1, uint32(s.sampleRate/20))
	}
	for _, chunk := range s.audioBuf.Push(frame.Data) {
		if err := s.writeMessageLocked(buildElevenLabsSTTAudioChunkMessage(chunk.Data, s.sampleRate, false)); err != nil {
			return err
		}
		s.addAudioDurationLocked(audio.CalculateFrameDuration(chunk))
	}
	return nil
}

func (s *elevenLabsSTTStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audioBuf == nil {
		return nil
	}
	flushed := false
	for _, chunk := range s.audioBuf.Flush() {
		flushed = true
		if err := s.writeMessageLocked(buildElevenLabsSTTAudioChunkMessage(chunk.Data, s.sampleRate, false)); err != nil {
			return err
		}
		s.addAudioDurationLocked(audio.CalculateFrameDuration(chunk))
	}
	if flushed {
		s.emitRecognitionUsageLocked()
	}
	return nil
}

func (s *elevenLabsSTTStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	s.inputEnded = true
	if s.audioBuf != nil {
		flushed := false
		for _, chunk := range s.audioBuf.Flush() {
			flushed = true
			if err := s.writeMessageLocked(buildElevenLabsSTTAudioChunkMessage(chunk.Data, s.sampleRate, false)); err != nil {
				return err
			}
			s.addAudioDurationLocked(audio.CalculateFrameDuration(chunk))
		}
		if flushed {
			s.emitRecognitionUsageLocked()
		}
	}
	return nil
}

func (s *elevenLabsSTTStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.inputEnded = true
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	err := s.conn.Close()
	s.mu.Unlock()
	s.unregisterFromProvider()
	return err
}

func (s *elevenLabsSTTStream) setServerVAD(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != nil {
		s.state.serverVAD = enabled
	}
}

func (s *elevenLabsSTTStream) language() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return ""
	}
	return s.state.language
}

func (s *elevenLabsSTTStream) reconnect(streamURL string, headers http.Header, serverVAD bool) {
	if s.ctx == nil || s.conn == nil {
		s.setServerVAD(serverVAD)
		return
	}
	conn, _, err := websocket.DefaultDialer.DialContext(s.ctx, streamURL, headers)
	if err != nil {
		s.sendError(llm.NewAPIConnectionError("Failed to connect to ElevenLabs"))
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = conn.Close()
		return
	}
	oldConn := s.conn
	s.conn = conn
	s.connVersion++
	s.audioBuf = nil
	if s.state != nil {
		s.state.serverVAD = serverVAD
	}
	s.mu.Unlock()
	if oldConn != nil {
		_ = oldConn.Close()
	}
}

func (s *elevenLabsSTTStream) currentConn() (*websocket.Conn, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn, s.connVersion
}

func (s *elevenLabsSTTStream) isStaleConn(version int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return version != s.connVersion && !s.closed
}

func (s *elevenLabsSTTStream) sendError(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *elevenLabsSTTStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return 0
	}
	return s.state.startTimeOffset
}

func (s *elevenLabsSTTStream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != nil {
		s.state.startTimeOffset = offset
	}
}

func (s *elevenLabsSTTStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return 0
	}
	return s.state.startTime
}

func (s *elevenLabsSTTStream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != nil {
		s.state.startTime = startTime
	}
}

func (s *elevenLabsSTTStream) unregisterFromProvider() {
	if s.unregister != nil {
		s.unregOnce.Do(func() { s.unregister(s) })
	}
}

func (s *elevenLabsSTTStream) writeMessageLocked(message map[string]any) error {
	if s.writeJSON != nil {
		if err := s.writeJSON(message); err != nil {
			s.closeAfterWriteFailureLocked(err)
			return err
		}
		return nil
	}
	if err := writeElevenLabsSTTMessage(s.conn, message); err != nil {
		s.closeAfterWriteFailureLocked(err)
		return err
	}
	return nil
}

func (s *elevenLabsSTTStream) closeAfterWriteFailureLocked(err error) {
	s.closed = true
	s.sendError(llm.NewAPIConnectionError(fmt.Sprintf("failed to write to ElevenLabs: %v", err)))
	if s.cancel != nil {
		s.cancel()
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

func (s *elevenLabsSTTStream) emitRecognitionUsageLocked() {
	if s.events == nil || s.audioDur <= 0 {
		return
	}
	duration := s.audioDur
	s.audioDur = 0
	s.events <- &stt.SpeechEvent{
		Type:             stt.SpeechEventRecognitionUsage,
		RecognitionUsage: &stt.RecognitionUsage{AudioDuration: duration},
	}
	s.usageLastFlush = time.Now()
}

func (s *elevenLabsSTTStream) addAudioDurationLocked(duration float64) {
	s.audioDur += duration
	if s.usageFlushInterval <= 0 {
		s.usageFlushInterval = elevenLabsSTTUsageInterval
	}
	if s.usageLastFlush.IsZero() {
		s.usageLastFlush = time.Now()
		return
	}
	if time.Since(s.usageLastFlush) >= s.usageFlushInterval {
		s.emitRecognitionUsageLocked()
	}
}

func (s *elevenLabsSTTStream) Next() (*stt.SpeechEvent, error) {
	if s.isClosed() {
		select {
		case err := <-s.errCh:
			return nil, err
		default:
		}
		return nil, io.EOF
	}
	select {
	case event, ok := <-s.events:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return event, nil
	case err := <-s.errCh:
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		return nil, err
	case <-s.ctx.Done():
		if s.isClosed() {
			return nil, io.EOF
		}
		if errors.Is(s.ctx.Err(), context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(s.ctx.Err().Error())
		}
		return nil, s.ctx.Err()
	}
}

func (s *elevenLabsSTTStream) readLoop() {
	defer s.unregisterFromProvider()
	defer close(s.events)
	for {
		conn, version := s.currentConn()
		if conn == nil {
			return
		}
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			if s.isStaleConn(version) {
				continue
			}
			if !s.isClosedOrInputEnded() {
				s.errCh <- elevenLabsSTTUnexpectedCloseError(err)
			}
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(payload, &data); err != nil {
			continue
		}
		events, err := processElevenLabsSTTStreamEvent(s.state, data)
		if err != nil {
			continue
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

func (s *elevenLabsSTTStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *elevenLabsSTTStream) isClosedOrInputEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed || s.inputEnded
}

func elevenLabsSTTUnexpectedCloseError(err error) error {
	statusCode := -1
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) && closeErr.Code != 0 {
		statusCode = closeErr.Code
	}
	return llm.NewAPIStatusError("ElevenLabs STT connection closed unexpectedly", statusCode, "", err.Error())
}

func (s *elevenLabsSTTStream) keepAliveLoop() {
	interval := s.keepAliveInterval
	if interval <= 0 {
		interval = elevenLabsSTTKeepAliveInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			err := s.writeMessageLocked(buildElevenLabsSTTAudioChunkMessage(nil, s.sampleRate, false))
			s.mu.Unlock()
			if err != nil {
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func writeElevenLabsSTTMessage(conn *websocket.Conn, message map[string]any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

type elevenLabsSTTStreamState struct {
	language          string
	includeTimestamps bool
	serverVAD         bool
	speaking          bool
	startTimeOffset   float64
	startTime         float64
}

type elevenLabsSTTResponse struct {
	Text         string              `json:"text"`
	LanguageCode string              `json:"language_code"`
	Words        []elevenLabsSTTWord `json:"words"`
}

type elevenLabsSTTWord struct {
	Text      string  `json:"text"`
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	SpeakerID string  `json:"speaker_id"`
}

func elevenLabsSTTSpeechEvent(defaultLanguage string, resp elevenLabsSTTResponse) *stt.SpeechEvent {
	language := resp.LanguageCode
	if language == "" {
		language = defaultLanguage
	}
	language = elevenLabsNormalizeSTTLanguage(language)
	data := stt.SpeechData{
		Text:     resp.Text,
		Language: language,
		Words:    elevenLabsSTTTimedStrings(resp.Words, 0),
	}
	if len(resp.Words) > 0 {
		data.SpeakerID = resp.Words[0].SpeakerID
		data.StartTime = minElevenLabsSTTStart(resp.Words)
		data.EndTime = maxElevenLabsSTTEnd(resp.Words)
	}
	return &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{data}}
}

func processElevenLabsSTTStreamEvent(state *elevenLabsSTTStreamState, data map[string]any) ([]*stt.SpeechEvent, error) {
	messageType, _ := data["message_type"].(string)
	switch messageType {
	case "partial_transcript":
		text, _ := data["text"].(string)
		if text == "" {
			return nil, nil
		}
		events := make([]*stt.SpeechEvent, 0, 2)
		if !state.speaking {
			state.speaking = true
			events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
		}
		events = append(events, &stt.SpeechEvent{
			Type:         stt.SpeechEventInterimTranscript,
			Alternatives: []stt.SpeechData{elevenLabsSTTSpeechDataFromStream(state, data)},
		})
		return events, nil
	case "committed_transcript":
		if state.includeTimestamps {
			return nil, nil
		}
		return elevenLabsSTTCommittedEvents(state, data), nil
	case "committed_transcript_with_timestamps":
		if !state.includeTimestamps {
			return nil, nil
		}
		return elevenLabsSTTCommittedEvents(state, data), nil
	case "session_started":
		return nil, nil
	case "auth_error", "quota_exceeded", "transcriber_error", "input_error", "error":
		msg, _ := data["message"].(string)
		if msg == "" {
			msg = "Unknown error"
		}
		details, _ := data["details"].(string)
		if details != "" {
			msg += " - " + details
		}
		return nil, llm.NewAPIConnectionError(fmt.Sprintf("%s: %s", messageType, msg))
	default:
		return nil, nil
	}
}

func elevenLabsSTTCommittedEvents(state *elevenLabsSTTStreamState, data map[string]any) []*stt.SpeechEvent {
	text, _ := data["text"].(string)
	if text == "" {
		if state.speaking {
			state.speaking = false
			return []*stt.SpeechEvent{{Type: stt.SpeechEventEndOfSpeech}}
		}
		return nil
	}
	events := make([]*stt.SpeechEvent, 0, 2)
	if !state.speaking {
		state.speaking = true
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
	}
	events = append(events, &stt.SpeechEvent{
		Type:         stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{elevenLabsSTTSpeechDataFromStream(state, data)},
	})
	if state.serverVAD {
		events = append(events, &stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
		state.speaking = false
	}
	return events
}

func elevenLabsSTTSpeechDataFromStream(state *elevenLabsSTTStreamState, data map[string]any) stt.SpeechData {
	text, _ := data["text"].(string)
	language, _ := data["language_code"].(string)
	if language == "" {
		language = state.language
	}
	if language == "" {
		language = "en"
	}
	language = elevenLabsNormalizeSTTLanguage(language)
	words := elevenLabsSTTWordsFromAny(data["words"])
	speechData := stt.SpeechData{
		Text:      text,
		Language:  language,
		StartTime: state.startTimeOffset,
		EndTime:   state.startTimeOffset,
	}
	if len(words) > 0 {
		speechData.StartTime = words[0].Start + state.startTimeOffset
		speechData.EndTime = words[len(words)-1].End + state.startTimeOffset
		speechData.Words = elevenLabsSTTTimedStrings(words, state.startTimeOffset)
	}
	return speechData
}

func elevenLabsNormalizeSTTLanguage(language string) string {
	if normalized := langutil.NormalizeLanguage(language); normalized != "" {
		return normalized
	}
	return language
}

func elevenLabsSTTWordsFromAny(raw any) []elevenLabsSTTWord {
	rawWords, ok := raw.([]any)
	if !ok {
		return nil
	}
	words := make([]elevenLabsSTTWord, 0, len(rawWords))
	for _, rawWord := range rawWords {
		wordMap, ok := rawWord.(map[string]any)
		if !ok {
			continue
		}
		words = append(words, elevenLabsSTTWord{
			Text:      elevenLabsAnyString(wordMap["text"]),
			Start:     elevenLabsAnyFloat(wordMap["start"]),
			End:       elevenLabsAnyFloat(wordMap["end"]),
			SpeakerID: elevenLabsAnyString(wordMap["speaker_id"]),
		})
	}
	return words
}

func elevenLabsSTTTimedStrings(words []elevenLabsSTTWord, startTimeOffset float64) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}
	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:            word.Text,
			StartTime:       word.Start + startTimeOffset,
			EndTime:         word.End + startTimeOffset,
			StartTimeOffset: startTimeOffset,
		})
	}
	return timed
}

func minElevenLabsSTTStart(words []elevenLabsSTTWord) float64 {
	if len(words) == 0 {
		return 0
	}
	start := words[0].Start
	for _, word := range words[1:] {
		if word.Start < start {
			start = word.Start
		}
	}
	return start
}

func maxElevenLabsSTTEnd(words []elevenLabsSTTWord) float64 {
	if len(words) == 0 {
		return 0
	}
	end := words[0].End
	for _, word := range words[1:] {
		if word.End > end {
			end = word.End
		}
	}
	return end
}

func elevenLabsAnyString(value any) string {
	str, _ := value.(string)
	return str
}

func elevenLabsAnyFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return 0
	}
}

// Deprecated: use STT.
type ElevenLabsSTT = STT

// Deprecated: use STTOption.
type ElevenLabsSTTOption = STTOption

// Deprecated: use NewSTT.
func NewElevenLabsSTT(apiKey string, opts ...STTOption) *STT {
	return NewSTT(apiKey, opts...)
}
