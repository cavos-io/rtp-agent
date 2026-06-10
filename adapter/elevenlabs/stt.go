package elevenlabs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

const (
	defaultElevenLabsSTTBaseURL    = "https://api.elevenlabs.io/v1"
	defaultElevenLabsSTTModel      = "scribe_v1"
	defaultElevenLabsSTTSampleRate = 16000
	elevenLabsSTTAuthHeader        = "xi-api-key"
)

type ElevenLabsVADOptions struct {
	VADSilenceThresholdSecs *float64
	VADThreshold            *float64
	MinSpeechDurationMS     *int
	MinSilenceDurationMS    *int
}

type ElevenLabsSTT struct {
	apiKey            string
	baseURL           string
	modelID           string
	languageCode      string
	tagAudioEvents    bool
	includeTimestamps bool
	sampleRate        int
	serverVAD         *ElevenLabsVADOptions
	keyterms          []string
}

type ElevenLabsSTTOption func(*ElevenLabsSTT)

func WithElevenLabsSTTBaseURL(baseURL string) ElevenLabsSTTOption {
	return func(s *ElevenLabsSTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithElevenLabsSTTModel(modelID string) ElevenLabsSTTOption {
	return func(s *ElevenLabsSTT) {
		if modelID != "" {
			s.modelID = modelID
		}
	}
}

func WithElevenLabsSTTLanguage(languageCode string) ElevenLabsSTTOption {
	return func(s *ElevenLabsSTT) {
		s.languageCode = languageCode
	}
}

func WithElevenLabsSTTTagAudioEvents(enabled bool) ElevenLabsSTTOption {
	return func(s *ElevenLabsSTT) {
		s.tagAudioEvents = enabled
	}
}

func WithElevenLabsSTTIncludeTimestamps(enabled bool) ElevenLabsSTTOption {
	return func(s *ElevenLabsSTT) {
		s.includeTimestamps = enabled
	}
}

func WithElevenLabsSTTSampleRate(sampleRate int) ElevenLabsSTTOption {
	return func(s *ElevenLabsSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithElevenLabsSTTServerVAD(serverVAD ElevenLabsVADOptions) ElevenLabsSTTOption {
	return func(s *ElevenLabsSTT) {
		s.serverVAD = &serverVAD
	}
}

func WithElevenLabsSTTKeyterms(keyterms []string) ElevenLabsSTTOption {
	return func(s *ElevenLabsSTT) {
		s.keyterms = keyterms
	}
}

func NewElevenLabsSTT(apiKey string, opts ...ElevenLabsSTTOption) *ElevenLabsSTT {
	provider := &ElevenLabsSTT{
		apiKey:         resolveElevenLabsAPIKey(apiKey),
		baseURL:        defaultElevenLabsSTTBaseURL,
		modelID:        defaultElevenLabsSTTModel,
		tagAudioEvents: true,
		sampleRate:     defaultElevenLabsSTTSampleRate,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *ElevenLabsSTT) Label() string { return "elevenlabs.STT" }
func (s *ElevenLabsSTT) Model() string { return s.modelID }
func (s *ElevenLabsSTT) Provider() string {
	return "ElevenLabs"
}
func (s *ElevenLabsSTT) Capabilities() stt.STTCapabilities {
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
		OfflineRecognize:  !realtime,
	}
}

func (s *ElevenLabsSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	if err := validateElevenLabsAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	if !elevenLabsSTTIsRealtime(s.modelID) {
		return nil, fmt.Errorf("elevenlabs streaming stt requires scribe_v2_realtime")
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, buildElevenLabsSTTStreamURL(s, language), buildElevenLabsSTTHeaders(s))
	if err != nil {
		return nil, fmt.Errorf("failed to dial elevenlabs stt websocket: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &elevenLabsSTTStream{
		conn:       conn,
		events:     make(chan *stt.SpeechEvent, 100),
		errCh:      make(chan error, 1),
		ctx:        streamCtx,
		cancel:     cancel,
		sampleRate: s.sampleRate,
		state: &elevenLabsSTTStreamState{
			language:          resolveElevenLabsSTTLanguage(s, language),
			includeTimestamps: s.includeTimestamps,
		},
	}
	go stream.readLoop()
	go stream.keepAliveLoop()
	return stream, nil
}

func (s *ElevenLabsSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	if err := validateElevenLabsAPIKey(s.apiKey); err != nil {
		return nil, err
	}

	if elevenLabsSTTIsRealtime(s.modelID) {
		return nil, fmt.Errorf("elevenlabs realtime models do not support offline recognize")
	}
	var audio bytes.Buffer
	for _, frame := range frames {
		audio.Write(frame.Data)
	}
	req, err := buildElevenLabsSTTRecognizeRequest(ctx, s, audio.Bytes(), language)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("elevenlabs stt error: %s", string(respBody))
	}
	var result elevenLabsSTTResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return elevenLabsSTTSpeechEvent(resolveElevenLabsSTTLanguage(s, language), result), nil
}

func buildElevenLabsSTTRecognizeRequest(ctx context.Context, s *ElevenLabsSTT, audio []byte, language string) (*http.Request, error) {
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
	if requestLanguage := resolveElevenLabsSTTLanguage(s, language); requestLanguage != "" {
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

func buildElevenLabsSTTStreamURL(s *ElevenLabsSTT, language string) string {
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

func buildElevenLabsSTTHeaders(s *ElevenLabsSTT) http.Header {
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

func resolveElevenLabsSTTLanguage(s *ElevenLabsSTT, language string) string {
	if language != "" {
		return language
	}
	return s.languageCode
}

func elevenLabsSTTIsRealtime(modelID string) bool {
	return modelID == "scribe_v2_realtime"
}

func formatElevenLabsFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

type elevenLabsSTTStream struct {
	conn       *websocket.Conn
	events     chan *stt.SpeechEvent
	errCh      chan error
	mu         sync.Mutex
	closed     bool
	ctx        context.Context
	cancel     context.CancelFunc
	sampleRate int
	state      *elevenLabsSTTStreamState
}

func (s *elevenLabsSTTStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	return writeElevenLabsSTTMessage(s.conn, buildElevenLabsSTTAudioChunkMessage(frame.Data, s.sampleRate, false))
}

func (s *elevenLabsSTTStream) Flush() error {
	return writeElevenLabsSTTMessage(s.conn, buildElevenLabsSTTAudioChunkMessage(nil, s.sampleRate, true))
}

func (s *elevenLabsSTTStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return s.conn.Close()
}

func (s *elevenLabsSTTStream) Next() (*stt.SpeechEvent, error) {
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
		return nil, err
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *elevenLabsSTTStream) readLoop() {
	defer close(s.events)
	for {
		msgType, payload, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				s.errCh <- err
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
			s.errCh <- err
			return
		}
		for _, event := range events {
			s.events <- event
		}
	}
}

func (s *elevenLabsSTTStream) keepAliveLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = writeElevenLabsSTTMessage(s.conn, buildElevenLabsSTTAudioChunkMessage(nil, s.sampleRate, false))
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
	speaking          bool
	startTimeOffset   float64
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
		details, _ := data["details"].(string)
		if details != "" {
			msg += " - " + details
		}
		return nil, fmt.Errorf("%s: %s", messageType, msg)
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
