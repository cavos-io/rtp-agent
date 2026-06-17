package elevenlabs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	coreaudio "github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/codecs"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	defaultElevenLabsBaseURL           = "https://api.elevenlabs.io/v1"
	defaultElevenLabsInactivityTimeout = 180
)

type ElevenLabsTTS struct {
	apiKey              string
	baseURL             string
	voiceID             string
	modelID             string
	encoding            string
	sampleRate          int
	language            string
	enableSSMLParsing   bool
	chunkLengthSchedule []int
}

type ElevenLabsTTSOption func(*ElevenLabsTTS)

func WithElevenLabsVoiceID(voiceID string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		if voiceID != "" {
			t.voiceID = voiceID
		}
	}
}

func WithElevenLabsModel(modelID string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		if modelID != "" {
			t.modelID = modelID
		}
	}
}

func WithElevenLabsBaseURL(baseURL string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		if baseURL != "" {
			t.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithElevenLabsLanguage(language string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		t.language = language
	}
}

func WithElevenLabsEnableSSMLParsing(enable bool) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		t.enableSSMLParsing = enable
	}
}

func WithElevenLabsEncoding(encoding string) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		if encoding != "" {
			t.encoding = encoding
			t.sampleRate = elevenLabsSampleRate(encoding)
		}
	}
}

func WithElevenLabsChunkLengthSchedule(schedule []int) ElevenLabsTTSOption {
	return func(t *ElevenLabsTTS) {
		t.chunkLengthSchedule = append([]int(nil), schedule...)
	}
}

func NewElevenLabsTTS(apiKey string, voiceID string, modelID string, opts ...ElevenLabsTTSOption) (*ElevenLabsTTS, error) {
	if voiceID == "" {
		voiceID = "hpp4J3VqNfWAUOO0d1Us"
	}
	if modelID == "" {
		modelID = "eleven_turbo_v2_5"
	}
	provider := &ElevenLabsTTS{
		apiKey:     resolveElevenLabsAPIKey(apiKey),
		baseURL:    defaultElevenLabsBaseURL,
		voiceID:    voiceID,
		modelID:    modelID,
		encoding:   "mp3_22050_32",
		sampleRate: 22050,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider, nil
}

func (t *ElevenLabsTTS) Label() string { return "elevenlabs.TTS" }
func (t *ElevenLabsTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: true}
}
func (t *ElevenLabsTTS) SampleRate() int  { return t.sampleRate }
func (t *ElevenLabsTTS) NumChannels() int { return 1 }
func (t *ElevenLabsTTS) Model() string    { return t.modelID }
func (t *ElevenLabsTTS) Provider() string { return "ElevenLabs" }

func (t *ElevenLabsTTS) UpdateOptions(opts ...ElevenLabsTTSOption) {
	for _, opt := range opts {
		opt(t)
	}
}

// Synthesize performs a full HTTP POST for non-streaming scenarios.
func (t *ElevenLabsTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := validateElevenLabsAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	apiURL, jsonBody := buildElevenLabsSynthesizeRequest(t, text)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", t.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("elevenlabs error: %s", string(respBody))
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "audio/") {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("elevenlabs returned non-audio data: %s", string(respBody))
	}

	return &elevenLabsChunkedStream{
		resp:       resp,
		encoding:   t.encoding,
		sampleRate: t.sampleRate,
	}, nil
}

func buildElevenLabsSynthesizeRequest(t *ElevenLabsTTS, text string) (string, []byte) {
	apiURL := fmt.Sprintf("%s/text-to-speech/%s/stream?model_id=%s&output_format=%s", strings.TrimRight(t.baseURL, "/"), t.voiceID, url.QueryEscape(t.modelID), url.QueryEscape(t.encoding))
	body := map[string]interface{}{
		"text":     text,
		"model_id": t.modelID,
	}
	if t.language != "" && elevenLabsSupportsLanguageCode(t.modelID) {
		body["language_code"] = t.language
	}
	if t.enableSSMLParsing {
		body["enable_ssml_parsing"] = true
	}
	jsonBody, _ := json.Marshal(body)
	return apiURL, jsonBody
}

type elevenLabsChunkedStream struct {
	resp       *http.Response
	encoding   string
	sampleRate int
	decoder    codecs.AudioStreamDecoder
	started    bool
	emitted    bool
}

func (s *elevenLabsChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if strings.HasPrefix(s.encoding, "mp3") {
		return s.nextDecodedMP3()
	}

	// Read PCM audio in chunks from the HTTP response
	buf := make([]byte, 8192)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		if err == io.EOF && n > 0 {
			s.emitted = true
			// Return final chunk
			return &tts.SynthesizedAudio{
				Frame: &model.AudioFrame{
					Data:              buf[:n],
					SampleRate:        uint32(s.sampleRate),
					NumChannels:       1,
					SamplesPerChannel: uint32(n / 2),
				},
				IsFinal: true,
			}, nil
		}
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("elevenlabs TTS chunked pcm response read %s: %w", s.audioByteState(), err)
	}

	s.emitted = true
	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              buf[:n],
			SampleRate:        uint32(s.sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(n / 2),
		},
	}, nil
}

func (s *elevenLabsChunkedStream) nextDecodedMP3() (*tts.SynthesizedAudio, error) {
	if !s.started {
		s.started = true
		s.decoder = codecs.NewMP3AudioStreamDecoder()
		data, err := io.ReadAll(s.resp.Body)
		if err != nil {
			return nil, fmt.Errorf("elevenlabs TTS chunked mp3 response read %s: %w", s.audioByteState(), err)
		}
		if len(data) == 0 {
			return nil, io.EOF
		}
		go func() {
			s.decoder.Push(data)
			s.decoder.EndInput()
		}()
	}

	frame, err := s.decoder.Next()
	if err != nil {
		if strings.Contains(err.Error(), "decoder closed") {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("elevenlabs TTS chunked mp3 decode %s: %w", s.audioByteState(), err)
	}
	frame, err = normalizeElevenLabsMP3Frame(frame, s.sampleRate)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs TTS chunked mp3 resample %s: %w", s.audioByteState(), err)
	}
	s.emitted = true
	return &tts.SynthesizedAudio{Frame: frame}, nil
}

func (s *elevenLabsChunkedStream) audioByteState() string {
	if s.emitted {
		return "after audio bytes"
	}
	return "before audio bytes"
}

func (s *elevenLabsChunkedStream) Close() error {
	if s.decoder != nil {
		_ = s.decoder.Close()
	}
	return s.resp.Body.Close()
}

// Stream establishes a high-performance WebSocket connection to ElevenLabs for low-latency streaming TTS.
func (t *ElevenLabsTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := validateElevenLabsAPIKey(t.apiKey); err != nil {
		return nil, err
	}

	header := make(http.Header)
	header.Set("xi-api-key", t.apiKey)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildElevenLabsStreamURL(t), header)
	if err != nil {
		return nil, fmt.Errorf("failed to dial elevenlabs websocket: %w", err)
	}

	contextID := "ctx_" + uuid.NewString()[:12]
	ctx, cancel := context.WithCancel(ctx)
	stream := &elevenLabsStream{
		conn:                conn,
		audio:               make(chan *tts.SynthesizedAudio, 100),
		errCh:               make(chan error, 1),
		ctx:                 ctx,
		cancel:              cancel,
		encoding:            t.encoding,
		sampleRate:          t.sampleRate,
		contextID:           contextID,
		chunkLengthSchedule: append([]int(nil), t.chunkLengthSchedule...),
	}

	go stream.readLoop()
	go stream.pingLoop()

	return stream, nil
}

func buildElevenLabsStreamURL(t *ElevenLabsTTS) string {
	streamBaseURL := strings.TrimRight(t.baseURL, "/")
	if strings.HasPrefix(streamBaseURL, "http://") || strings.HasPrefix(streamBaseURL, "https://") {
		streamBaseURL = strings.Replace(streamBaseURL, "http", "ws", 1)
	}
	parsed, err := url.Parse(streamBaseURL + fmt.Sprintf("/text-to-speech/%s/multi-stream-input", t.voiceID))
	if err != nil {
		u := url.URL{Scheme: "wss", Host: "api.elevenlabs.io", Path: fmt.Sprintf("/v1/text-to-speech/%s/multi-stream-input", t.voiceID)}
		parsed = &u
	}
	q := parsed.Query()
	q.Set("model_id", t.modelID)
	q.Set("output_format", t.encoding)
	if t.language != "" && elevenLabsSupportsLanguageCode(t.modelID) {
		q.Set("language_code", t.language)
	}
	q.Set("enable_ssml_parsing", strconv.FormatBool(t.enableSSMLParsing))
	q.Set("enable_logging", "true")
	q.Set("inactivity_timeout", strconv.Itoa(defaultElevenLabsInactivityTimeout))
	q.Set("apply_text_normalization", "auto")
	q.Set("sync_alignment", "true")
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func elevenLabsSupportsLanguageCode(modelID string) bool {
	switch modelID {
	case "eleven_turbo_v2_5", "eleven_turbo_v2", "eleven_flash_v2_5", "eleven_flash_v2":
		return true
	default:
		return false
	}
}

func elevenLabsSampleRate(encoding string) int {
	parts := strings.Split(encoding, "_")
	if len(parts) >= 2 {
		var sampleRate int
		if _, err := fmt.Sscanf(parts[1], "%d", &sampleRate); err == nil && sampleRate > 0 {
			return sampleRate
		}
	}
	return 22050
}

type elevenLabsStream struct {
	conn   *websocket.Conn
	audio  chan *tts.SynthesizedAudio
	errCh  chan error
	mu     sync.Mutex
	closed bool

	ctx    context.Context
	cancel context.CancelFunc

	encoding            string
	sampleRate          int
	contextID           string
	initSent            bool
	chunkLengthSchedule []int

	alignRunes    []rune
	alignStartsMs []int
	alignDurMs    []int
}

type elevenLabsAlignment struct {
	Chars             []string `json:"chars"`
	CharStartTimesMs  []int    `json:"charStartTimesMs"`
	CharsStartTimesMs []int    `json:"charsStartTimesMs"`
	CharDurationsMs   []int    `json:"charDurationsMs"`
	CharsDurationsMs  []int    `json:"charsDurationsMs"`
}

func (a *elevenLabsAlignment) starts() []int {
	if a == nil {
		return nil
	}
	if len(a.CharStartTimesMs) > 0 {
		return a.CharStartTimesMs
	}
	return a.CharsStartTimesMs
}

func (a *elevenLabsAlignment) durations() []int {
	if a == nil {
		return nil
	}
	if len(a.CharDurationsMs) > 0 {
		return a.CharDurationsMs
	}
	return a.CharsDurationsMs
}

type elWSResponse struct {
	Audio               string               `json:"audio"`
	IsFinal             bool                 `json:"isFinal"`
	NormalizedAlignment *elevenLabsAlignment `json:"normalizedAlignment"`
	Alignment           *elevenLabsAlignment `json:"alignment"`
	Error               string               `json:"error,omitempty"`
}

func (s *elevenLabsStream) readLoop() {
	defer s.Close()
	defer close(s.audio)

	for {
		_, message, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				logger.Logger.Errorw("ElevenLabs WebSocket read error", err)
				s.sendError(fmt.Errorf("elevenlabs TTS websocket read: %w", err))
			}
			return
		}

		var resp elWSResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			logger.Logger.Warnw("Failed to unmarshal ElevenLabs response", err, "payload", string(message))
			continue
		}

		if resp.Error != "" {
			logger.Logger.Errorw("ElevenLabs WebSocket returned error", nil, "error", resp.Error)
			s.sendError(fmt.Errorf("elevenlabs error: %s", resp.Error))
			return
		}

		deltaText := elevenLabsDeltaText(resp)
		timedTranscript := s.timedTranscriptFromAlignment(resp)

		if resp.Audio != "" {
			audio, err := elevenLabsSynthesizedAudio(resp, s.sampleRate, s.encoding)
			if err != nil {
				logger.Logger.Errorw("Failed to decode ElevenLabs audio", err)
				s.sendError(fmt.Errorf("elevenlabs TTS websocket audio decode: %w", err))
				return
			}
			audio.TimedTranscript = timedTranscript
			select {
			case <-s.ctx.Done():
				return
			case s.audio <- audio:
			}
		} else if resp.IsFinal || deltaText != "" {
			// Even if there's no audio, pass alignment or final flags
			select {
			case <-s.ctx.Done():
				return
			case s.audio <- &tts.SynthesizedAudio{
				IsFinal:         resp.IsFinal,
				DeltaText:       deltaText,
				TimedTranscript: timedTranscript,
			}:
			}
		}

		if resp.IsFinal {
			return
		}
	}
}

func elevenLabsSynthesizedAudio(resp elWSResponse, sampleRate int, encoding string) (*tts.SynthesizedAudio, error) {
	data, err := base64.StdEncoding.DecodeString(resp.Audio)
	if err != nil {
		return nil, err
	}
	deltaText := elevenLabsDeltaText(resp)
	timedTranscript := elevenLabsTimedTranscript(resp, resp.IsFinal)
	if len(timedTranscript) == 0 {
		timedTranscript = nil
	}
	if strings.HasPrefix(encoding, "mp3") {
		frame, err := decodeElevenLabsMP3Audio(data, sampleRate)
		if err != nil {
			return nil, err
		}
		return &tts.SynthesizedAudio{
			Frame:           frame,
			IsFinal:         resp.IsFinal,
			DeltaText:       deltaText,
			TimedTranscript: timedTranscript,
		}, nil
	}

	return &tts.SynthesizedAudio{
		Frame: &model.AudioFrame{
			Data:              data,
			SampleRate:        uint32(sampleRate),
			NumChannels:       1,
			SamplesPerChannel: uint32(len(data) / 2),
		},
		IsFinal:         resp.IsFinal,
		DeltaText:       deltaText,
		TimedTranscript: timedTranscript,
	}, nil
}

func elevenLabsDeltaText(resp elWSResponse) string {
	var deltaText strings.Builder
	if resp.NormalizedAlignment != nil {
		for _, char := range resp.NormalizedAlignment.Chars {
			deltaText.WriteString(char)
		}
	} else if resp.Alignment != nil {
		for _, char := range resp.Alignment.Chars {
			deltaText.WriteString(char)
		}
	}
	return deltaText.String()
}

func (s *elevenLabsStream) timedTranscriptFromAlignment(resp elWSResponse) []tts.TimedString {
	alignment := preferredElevenLabsAlignment(resp)
	if alignment == nil {
		return nil
	}
	appendElevenLabsAlignment(&s.alignRunes, &s.alignStartsMs, &s.alignDurMs, alignment)
	timed, remainingRunes, remainingStarts, remainingDurations := elevenLabsTimedWords(s.alignRunes, s.alignStartsMs, s.alignDurMs, resp.IsFinal)
	s.alignRunes = remainingRunes
	s.alignStartsMs = remainingStarts
	s.alignDurMs = remainingDurations
	return timed
}

func elevenLabsTimedTranscript(resp elWSResponse, flush bool) []tts.TimedString {
	alignment := preferredElevenLabsAlignment(resp)
	if alignment == nil {
		return nil
	}
	var runes []rune
	var starts []int
	var durations []int
	appendElevenLabsAlignment(&runes, &starts, &durations, alignment)
	timed, _, _, _ := elevenLabsTimedWords(runes, starts, durations, flush)
	return timed
}

func preferredElevenLabsAlignment(resp elWSResponse) *elevenLabsAlignment {
	if resp.NormalizedAlignment != nil {
		return resp.NormalizedAlignment
	}
	return resp.Alignment
}

func appendElevenLabsAlignment(runes *[]rune, starts *[]int, durations *[]int, alignment *elevenLabsAlignment) {
	if alignment == nil {
		return
	}
	startTimes := alignment.starts()
	durationTimes := alignment.durations()
	if len(alignment.Chars) != len(startTimes) || len(alignment.Chars) != len(durationTimes) {
		return
	}
	for i, char := range alignment.Chars {
		charRunes := []rune(char)
		if len(charRunes) == 0 {
			continue
		}
		for j, r := range charRunes {
			*runes = append(*runes, r)
			*starts = append(*starts, startTimes[i])
			if j == len(charRunes)-1 {
				*durations = append(*durations, durationTimes[i])
			} else {
				*durations = append(*durations, 0)
			}
		}
	}
}

func elevenLabsTimedWords(runes []rune, starts []int, durations []int, flush bool) ([]tts.TimedString, []rune, []int, []int) {
	if len(runes) == 0 || len(runes) != len(starts) || len(runes) != len(durations) {
		return nil, runes, starts, durations
	}
	wordStarts := elevenLabsWordStartIndices(runes)
	if len(wordStarts) == 0 {
		return nil, runes, starts, durations
	}

	timestamps := append(append([]int(nil), starts...), starts[len(starts)-1]+durations[len(durations)-1])
	timed := make([]tts.TimedString, 0, len(wordStarts))
	end := 0
	for i := 0; i+1 < len(wordStarts); i++ {
		start := wordStarts[i]
		end = wordStarts[i+1]
		timed = append(timed, tts.TimedString{
			Text:      string(runes[start:end]),
			StartTime: float64(timestamps[start]) / 1000,
			EndTime:   float64(timestamps[end]) / 1000,
		})
	}
	if flush {
		start := end
		timed = append(timed, tts.TimedString{
			Text:      string(runes[start:]),
			StartTime: float64(timestamps[start]) / 1000,
			EndTime:   float64(timestamps[len(runes)]) / 1000,
		})
		end = len(runes)
	}
	return timed, append([]rune(nil), runes[end:]...), append([]int(nil), starts[end:]...), append([]int(nil), durations[end:]...)
}

func elevenLabsWordStartIndices(runes []rune) []int {
	starts := []int{}
	inWord := false
	for i, r := range runes {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			inWord = false
			continue
		}
		if !inWord {
			starts = append(starts, i)
			inWord = true
		}
	}
	return starts
}

func decodeElevenLabsMP3Audio(data []byte, sampleRate int) (*model.AudioFrame, error) {
	decoder := codecs.NewMP3AudioStreamDecoder()
	defer decoder.Close()
	go func() {
		decoder.Push(data)
		decoder.EndInput()
	}()
	frame, err := decoder.Next()
	if err != nil {
		return nil, err
	}
	return normalizeElevenLabsMP3Frame(frame, sampleRate)
}

func normalizeElevenLabsMP3Frame(frame *model.AudioFrame, sampleRate int) (*model.AudioFrame, error) {
	frame = elevenLabsDownmixToMono(frame)
	if sampleRate <= 0 {
		return frame, nil
	}
	return coreaudio.ResampleAudioFrame(frame, uint32(sampleRate))
}

func elevenLabsDownmixToMono(frame *model.AudioFrame) *model.AudioFrame {
	if frame == nil || frame.NumChannels <= 1 {
		return frame
	}
	channels := int(frame.NumChannels)
	samples := int(frame.SamplesPerChannel)
	if samples == 0 {
		samples = len(frame.Data) / (channels * 2)
	}
	out := make([]byte, samples*2)
	for sample := 0; sample < samples; sample++ {
		sum := int32(0)
		for channel := 0; channel < channels; channel++ {
			offset := (sample*channels + channel) * 2
			if offset+2 > len(frame.Data) {
				break
			}
			sum += int32(int16(binary.LittleEndian.Uint16(frame.Data[offset : offset+2])))
		}
		binary.LittleEndian.PutUint16(out[sample*2:sample*2+2], uint16(int16(sum/int32(channels))))
	}
	mono := *frame
	mono.Data = out
	mono.NumChannels = 1
	mono.SamplesPerChannel = uint32(samples)
	return &mono
}

func (s *elevenLabsStream) pingLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if !s.closed {
				_ = s.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
			}
			s.mu.Unlock()
		}
	}
}

func (s *elevenLabsStream) sendError(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *elevenLabsStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if err := s.sendInitLocked(); err != nil {
		return err
	}
	if err := s.conn.WriteJSON(elevenLabsTextPayload(s.contextID, text)); err != nil {
		s.closeAfterWriteFailureLocked()
		return fmt.Errorf("failed to write text to elevenlabs: %w", err)
	}
	return nil
}

func (s *elevenLabsStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if err := s.sendInitLocked(); err != nil {
		return err
	}
	if err := s.conn.WriteJSON(elevenLabsFlushPayload(s.contextID)); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func elevenLabsInitPayload(contextID string, chunkLengthSchedule []int) map[string]interface{} {
	payload := map[string]interface{}{
		"text":           " ",
		"voice_settings": map[string]interface{}{},
		"context_id":     contextID,
	}
	if len(chunkLengthSchedule) > 0 {
		payload["generation_config"] = map[string]interface{}{
			"chunk_length_schedule": append([]int(nil), chunkLengthSchedule...),
		}
	}
	return payload
}

func elevenLabsTextPayload(contextID string, text string) map[string]interface{} {
	return map[string]interface{}{
		"text":       text,
		"context_id": contextID,
	}
}

func elevenLabsFlushPayload(contextID string) map[string]interface{} {
	return map[string]interface{}{
		"text":       "",
		"context_id": contextID,
		"flush":      true,
	}
}

func elevenLabsCloseContextPayload(contextID string) map[string]interface{} {
	return map[string]interface{}{
		"context_id":    contextID,
		"close_context": true,
	}
}

func (s *elevenLabsStream) sendInitLocked() error {
	if s.initSent {
		return nil
	}
	if err := s.conn.WriteJSON(elevenLabsInitPayload(s.contextID, s.chunkLengthSchedule)); err != nil {
		s.closeAfterWriteFailureLocked()
		return fmt.Errorf("failed to write initial config to elevenlabs: %w", err)
	}
	s.initSent = true
	return nil
}

func (s *elevenLabsStream) closeAfterWriteFailureLocked() {
	s.closed = true
	s.cancel()
	_ = s.conn.Close()
}

func (s *elevenLabsStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if s.initSent {
		_ = s.conn.WriteJSON(elevenLabsCloseContextPayload(s.contextID))
	}
	return s.conn.Close()
}

func (s *elevenLabsStream) Next() (*tts.SynthesizedAudio, error) {
	select {
	case <-s.ctx.Done():
		return nil, io.EOF
	case err := <-s.errCh:
		return nil, err
	case audio, ok := <-s.audio:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return audio, nil
	}
}
