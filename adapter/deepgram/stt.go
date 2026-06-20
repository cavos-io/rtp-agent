package deepgram

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/utils/language"
	"github.com/gorilla/websocket"
)

type DeepgramSTT struct {
	apiKey            string
	model             string
	detectLanguage    bool
	punctuate         bool
	smartFormat       bool
	noDelay           bool
	endpointingMS     int
	enableDiarization bool
	fillerWords       bool
	sampleRate        int
	numChannels       int
	interimResults    bool
	vadEvents         bool
	profanityFilter   bool
	numerals          bool
	mipOptOut         bool
	keywords          []DeepgramKeyword
	keyterms          []string
	redact            []string
	tags              []string
	baseURL           string
	mu                sync.Mutex
	streams           map[*deepgramStream]struct{}
}

type DeepgramKeyword struct {
	Keyword string
	Boost   float64
}

type DeepgramSTTOption func(*DeepgramSTT)

const deepgramSTTKeepAliveInterval = 5 * time.Second

func WithDeepgramSTTBaseURL(baseURL string) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithDeepgramSTTInterimResults(interimResults bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.interimResults = interimResults
	}
}

func WithDeepgramSTTDetectLanguage(detectLanguage bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.detectLanguage = detectLanguage
	}
}

func WithDeepgramSTTPunctuate(punctuate bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.punctuate = punctuate
	}
}

func WithDeepgramSTTSmartFormat(smartFormat bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.smartFormat = smartFormat
	}
}

func WithDeepgramSTTNoDelay(noDelay bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.noDelay = noDelay
	}
}

func WithDeepgramSTTEndpointing(endpointingMS int) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.endpointingMS = endpointingMS
	}
}

func WithDeepgramSTTDiarization(enableDiarization bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.enableDiarization = enableDiarization
	}
}

func WithDeepgramSTTFillerWords(fillerWords bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.fillerWords = fillerWords
	}
}

func WithDeepgramSTTSampleRate(sampleRate int) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		if sampleRate > 0 {
			s.sampleRate = sampleRate
		}
	}
}

func WithDeepgramSTTNumChannels(numChannels int) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		if numChannels > 0 {
			s.numChannels = numChannels
		}
	}
}

func WithDeepgramSTTVADEvents(vadEvents bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.vadEvents = vadEvents
	}
}

func WithDeepgramSTTProfanityFilter(profanityFilter bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.profanityFilter = profanityFilter
	}
}

func WithDeepgramSTTNumerals(numerals bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.numerals = numerals
	}
}

func WithDeepgramSTTMipOptOut(mipOptOut bool) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.mipOptOut = mipOptOut
	}
}

func WithDeepgramSTTKeywords(keywords []DeepgramKeyword) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.keywords = append([]DeepgramKeyword(nil), keywords...)
	}
}

func WithDeepgramSTTKeyterms(keyterms []string) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.keyterms = append([]string(nil), keyterms...)
	}
}

func WithDeepgramSTTRedact(redact []string) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.redact = append([]string(nil), redact...)
	}
}

func WithDeepgramSTTTags(tags []string) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		s.tags = append([]string(nil), tags...)
	}
}

func NewDeepgramSTT(apiKey string, model string, opts ...DeepgramSTTOption) *DeepgramSTT {
	if apiKey == "" {
		apiKey = os.Getenv("DEEPGRAM_API_KEY")
	}
	if model == "" {
		model = "nova-3"
	}
	provider := &DeepgramSTT{
		apiKey:         apiKey,
		model:          model,
		punctuate:      true,
		smartFormat:    false,
		noDelay:        true,
		endpointingMS:  25,
		fillerWords:    true,
		sampleRate:     16000,
		numChannels:    1,
		interimResults: true,
		vadEvents:      true,
		baseURL:        "https://api.deepgram.com/v1/listen",
		streams:        make(map[*deepgramStream]struct{}),
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func (s *DeepgramSTT) Label() string { return "deepgram.STT" }
func (s *DeepgramSTT) Model() string { return s.model }
func (s *DeepgramSTT) Provider() string {
	return "Deepgram"
}
func (s *DeepgramSTT) InputSampleRate() uint32 { return uint32(s.sampleRate) }
func (s *DeepgramSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, InterimResults: s.interimResults, Diarization: s.enableDiarization, AlignedTranscript: "word", OfflineRecognize: true}
}

func (s *DeepgramSTT) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	streams := make([]*deepgramStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.streams = make(map[*deepgramStream]struct{})
	s.mu.Unlock()

	var closeErr error
	for _, stream := range streams {
		if err := stream.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (s *DeepgramSTT) UpdateOptions(opts ...DeepgramSTTOption) {
	s.mu.Lock()
	for _, opt := range opts {
		opt(s)
	}
	streams := make([]*deepgramStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()
	for _, stream := range streams {
		stream.updateOptions()
	}
}

func (s *DeepgramSTT) Stream(ctx context.Context, languageStr string) (stt.RecognizeStream, error) {
	if err := validateDeepgramSTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}
	if err := validateDeepgramSTTOptions(s); err != nil {
		return nil, err
	}

	languageStr = language.NormalizeLanguage(languageStr)
	if s.detectLanguage {
		return nil, fmt.Errorf("language detection is not supported in streaming mode, please disable it and specify a language")
	}

	header := make(http.Header)
	header.Set("Authorization", "Token "+s.apiKey)

	streamURL := buildDeepgramStreamURL(s, languageStr)
	conn, err := openDeepgramStreamConnection(ctx, s, streamURL, header)
	if err != nil {
		return nil, err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &deepgramStream{
		provider:    s,
		conn:        conn,
		streamURL:   streamURL,
		events:      make(chan *stt.SpeechEvent, 100),
		errCh:       make(chan error, 1),
		ctx:         streamCtx,
		cancel:      cancel,
		sampleRate:  s.sampleRate,
		numChannels: s.numChannels,
		language:    languageStr,
	}
	s.registerStream(stream)

	go stream.readLoop(conn)
	go stream.keepAliveLoop()

	return stream, nil
}

func openDeepgramStreamConnection(ctx context.Context, s *DeepgramSTT, streamURL string, header http.Header) (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, streamURL, header)
	if err != nil {
		return nil, llm.NewAPIConnectionError("failed to connect to deepgram")
	}
	return conn, nil
}

func (s *DeepgramSTT) registerStream(stream *deepgramStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = make(map[*deepgramStream]struct{})
	}
	s.streams[stream] = struct{}{}
}

func (s *DeepgramSTT) unregisterStream(stream *deepgramStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}

func (s *DeepgramSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, languageStr string) (*stt.SpeechEvent, error) {
	if err := validateDeepgramSTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}
	if err := validateDeepgramSTTOptions(s); err != nil {
		return nil, err
	}

	languageStr = language.NormalizeLanguage(languageStr)
	if s.detectLanguage {
		languageStr = ""
	}

	wav := deepgramSTTWAVBytes(frames, uint32(s.sampleRate), uint32(s.numChannels))

	req, err := http.NewRequestWithContext(ctx, "POST", buildDeepgramRecognizeURL(s, languageStr), bytes.NewReader(wav))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "audio/wav")
	req.Header.Set("Authorization", "Token "+s.apiKey)

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
		return nil, llm.NewAPIStatusError("Deepgram STT request failed", resp.StatusCode, "", string(respBody))
	}

	var result dgRecognitionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}

	return deepgramRecognizeSpeechEventForLanguage(result, languageStr), nil
}

func validateDeepgramSTTAPIKey(apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("deepgram API key is required, either as argument or set DEEPGRAM_API_KEY environment variable")
	}
	return nil
}

func deepgramSTTWAVBytes(frames []*model.AudioFrame, defaultSampleRate uint32, defaultNumChannels uint32) []byte {
	sampleRate := defaultSampleRate
	if sampleRate == 0 {
		sampleRate = 16000
	}
	numChannels := defaultNumChannels
	if numChannels == 0 {
		numChannels = 1
	}
	var data bytes.Buffer
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		if frame.SampleRate > 0 && data.Len() == 0 {
			sampleRate = frame.SampleRate
		}
		if frame.NumChannels > 0 && data.Len() == 0 {
			numChannels = frame.NumChannels
		}
		data.Write(frame.Data)
	}

	pcm := data.Bytes()
	dataSize := uint32(len(pcm))
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
	wav.Write(pcm)
	return wav.Bytes()
}

func validateDeepgramSTTOptions(s *DeepgramSTT) error {
	for _, tag := range s.tags {
		if len(tag) > 128 {
			return fmt.Errorf("tag must be no more than 128 characters")
		}
	}
	if strings.HasPrefix(s.model, "nova-3") {
		for _, keyword := range s.keywords {
			if keyword.Keyword != "" {
				return fmt.Errorf("keywords is only available for use with Nova-2, Nova-1, Enhanced, and Base speech to text models; for Nova-3, use Keyterm Prompting")
			}
		}
	}
	if !strings.HasPrefix(s.model, "nova-3") {
		for _, keyterm := range s.keyterms {
			if keyterm != "" {
				return fmt.Errorf("keyterm Prompting is only available for transcription using the Nova-3 Model; to boost recognition of keywords using another model, use the Keywords feature")
			}
		}
	}
	return nil
}

func buildDeepgramStreamURL(s *DeepgramSTT, languageStr string) string {
	u, q := deepgramBaseURL(s, true)
	q.Set("model", deepgramSTTModelForLanguage(s.model, languageStr))
	if languageStr != "" {
		q.Set("language", languageStr)
	}
	q.Set("punctuate", strconv.FormatBool(s.punctuate))
	q.Set("smart_format", strconv.FormatBool(s.smartFormat))
	q.Set("no_delay", strconv.FormatBool(s.noDelay))
	q.Set("interim_results", strconv.FormatBool(s.interimResults))
	q.Set("encoding", "linear16")
	q.Set("sample_rate", strconv.Itoa(s.sampleRate))
	q.Set("channels", strconv.Itoa(s.numChannels))
	if s.endpointingMS == 0 {
		q.Set("endpointing", "false")
	} else {
		q.Set("endpointing", strconv.Itoa(s.endpointingMS))
	}
	q.Set("vad_events", strconv.FormatBool(s.vadEvents))
	q.Set("filler_words", strconv.FormatBool(s.fillerWords))
	q.Set("profanity_filter", strconv.FormatBool(s.profanityFilter))
	q.Set("numerals", strconv.FormatBool(s.numerals))
	q.Set("mip_opt_out", strconv.FormatBool(s.mipOptOut))
	if s.enableDiarization {
		q.Set("diarize", "true")
	}
	addDeepgramSTTAdvancedQuery(q, s)
	u.RawQuery = q.Encode()
	return u.String()
}

func buildDeepgramRecognizeURL(s *DeepgramSTT, languageStr string) string {
	u, q := deepgramBaseURL(s, false)
	q.Set("model", deepgramSTTModelForLanguage(s.model, languageStr))
	q.Set("punctuate", strconv.FormatBool(s.punctuate))
	q.Set("detect_language", strconv.FormatBool(s.detectLanguage))
	q.Set("smart_format", strconv.FormatBool(s.smartFormat))
	q.Set("profanity_filter", strconv.FormatBool(s.profanityFilter))
	q.Set("numerals", strconv.FormatBool(s.numerals))
	q.Set("mip_opt_out", strconv.FormatBool(s.mipOptOut))
	if languageStr != "" {
		q.Set("language", languageStr)
	}
	addDeepgramSTTAdvancedQuery(q, s)
	u.RawQuery = q.Encode()
	return u.String()
}

func addDeepgramSTTAdvancedQuery(q url.Values, s *DeepgramSTT) {
	for _, keyword := range s.keywords {
		if keyword.Keyword != "" {
			q.Add("keywords", keyword.Keyword+":"+strconv.FormatFloat(keyword.Boost, 'f', -1, 64))
		}
	}
	for _, keyterm := range s.keyterms {
		if keyterm != "" {
			q.Add("keyterm", keyterm)
		}
	}
	for _, redact := range s.redact {
		if redact != "" {
			q.Add("redact", redact)
		}
	}
	for _, tag := range s.tags {
		if tag != "" {
			q.Add("tag", tag)
		}
	}
}

func deepgramSTTModelForLanguage(model string, languageStr string) string {
	switch model {
	case "nova-2-meeting", "nova-2-phonecall", "nova-2-finance", "nova-2-conversationalai", "nova-2-voicemail", "nova-2-video", "nova-2-medical", "nova-2-drivethru", "nova-2-automotive":
		if languageStr != "" && languageStr != "en-US" && languageStr != "en" {
			return "nova-2-general"
		}
	}
	return model
}

func deepgramBaseURL(s *DeepgramSTT, websocketURL bool) (*url.URL, url.Values) {
	parsed, err := url.Parse(s.baseURL)
	if err != nil {
		parsed = &url.URL{Scheme: "https", Host: "api.deepgram.com", Path: "/v1/listen"}
	}
	if websocketURL && parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else if websocketURL && parsed.Scheme == "http" {
		parsed.Scheme = "ws"
	} else if !websocketURL && parsed.Scheme == "wss" {
		parsed.Scheme = "https"
	} else if !websocketURL && parsed.Scheme == "ws" {
		parsed.Scheme = "http"
	}
	return parsed, parsed.Query()
}

type deepgramStream struct {
	provider      *DeepgramSTT
	conn          *websocket.Conn
	streamURL     string
	events        chan *stt.SpeechEvent
	errCh         chan error
	mu            sync.Mutex
	closed        bool
	speaking      bool
	reconnectNext bool
	start         float64
	offset        float64

	ctx    context.Context
	cancel context.CancelFunc

	sampleRate   int
	numChannels  int
	language     string
	audioBStream *audio.AudioByteStream
	writeBinary  func([]byte) error
	writeJSON    func(any) error
}

type dgWord struct {
	Word       string  `json:"word"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
	Speaker    *int    `json:"speaker,omitempty"`
}

type dgAlternative struct {
	Transcript string   `json:"transcript"`
	Confidence float64  `json:"confidence"`
	Languages  []string `json:"languages"`
	Words      []dgWord `json:"words"`
}

type dgRecognitionChannel struct {
	Alternatives     []dgAlternative `json:"alternatives"`
	DetectedLanguage string          `json:"detected_language"`
}

type dgRecognitionResponse struct {
	Metadata struct {
		RequestID string `json:"request_id"`
	} `json:"metadata"`
	Results struct {
		Channels []dgRecognitionChannel `json:"channels"`
	} `json:"results"`
}

type dgResponse struct {
	Type        string `json:"type"`
	IsFinal     bool   `json:"is_final"`
	SpeechFinal bool   `json:"speech_final"`
	Channel     struct {
		Alternatives []dgAlternative `json:"alternatives"`
	} `json:"channel"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
	Metadata struct {
		RequestID string `json:"request_id"`
	} `json:"metadata"`
}

func deepgramRecognizeSpeechEvent(resp dgRecognitionResponse) *stt.SpeechEvent {
	return deepgramRecognizeSpeechEventForLanguage(resp, "")
}

func deepgramRecognizeSpeechEventForLanguage(resp dgRecognitionResponse, languageStr string) *stt.SpeechEvent {
	event := &stt.SpeechEvent{
		Type:         stt.SpeechEventFinalTranscript,
		RequestID:    resp.Metadata.RequestID,
		Alternatives: []stt.SpeechData{{Language: languageStr}},
	}

	if len(resp.Results.Channels) == 0 || len(resp.Results.Channels[0].Alternatives) == 0 {
		return event
	}

	channel := resp.Results.Channels[0]
	event.Alternatives = event.Alternatives[:0]
	for _, alt := range channel.Alternatives {
		event.Alternatives = append(event.Alternatives, stt.SpeechData{
			Language:   deepgramRecognizeLanguage(languageStr, channel.DetectedLanguage),
			Text:       alt.Transcript,
			StartTime:  deepgramFirstWordStart(alt.Words),
			EndTime:    deepgramLastWordEnd(alt.Words),
			Confidence: alt.Confidence,
			Words:      deepgramTimedStrings(alt.Words),
		})
	}
	return event
}

func deepgramRecognizeLanguage(languageStr string, detectedLanguage string) string {
	if languageStr == "" {
		return detectedLanguage
	}
	return languageStr
}

func deepgramSpeechEvent(resp dgResponse) *stt.SpeechEvent {
	return deepgramSpeechEventForLanguage(resp, "")
}

func deepgramSpeechEventForLanguage(resp dgResponse, languageStr string) *stt.SpeechEvent {
	return deepgramSpeechEventForLanguageOffset(resp, languageStr, 0)
}

func deepgramSpeechEventForLanguageOffset(resp dgResponse, languageStr string, startTimeOffset float64) *stt.SpeechEvent {
	if resp.Type != "Results" || len(resp.Channel.Alternatives) == 0 {
		return nil
	}
	if resp.Channel.Alternatives[0].Transcript == "" {
		return nil
	}

	event := &stt.SpeechEvent{
		Type:      stt.SpeechEventInterimTranscript,
		RequestID: resp.Metadata.RequestID,
	}
	if resp.IsFinal {
		event.Type = stt.SpeechEventFinalTranscript
	}

	var transcriptBuilder string
	for _, alt := range resp.Channel.Alternatives {
		transcriptBuilder += alt.Transcript
		event.Alternatives = append(event.Alternatives, stt.SpeechData{
			Language:   deepgramLiveLanguage(languageStr, alt.Languages),
			Text:       alt.Transcript,
			Confidence: alt.Confidence,
			StartTime:  deepgramFirstWordStart(alt.Words) + startTimeOffset,
			EndTime:    deepgramFirstWordEnd(alt.Words) + startTimeOffset,
			SpeakerID:  deepgramLiveSpeakerID(alt.Words, resp.IsFinal),
			Words:      deepgramTimedStringsOffset(alt.Words, startTimeOffset),
		})
	}

	if transcriptBuilder == "" {
		return nil
	}

	return event
}

func deepgramLiveLanguage(languageStr string, detected []string) string {
	if languageStr == "multi" && len(detected) > 0 {
		return detected[0]
	}
	return languageStr
}

func deepgramFirstWordStart(words []dgWord) float64 {
	if len(words) == 0 {
		return 0
	}
	return words[0].Start
}

func deepgramFirstWordEnd(words []dgWord) float64 {
	if len(words) == 0 {
		return 0
	}
	return words[0].End
}

func deepgramLastWordEnd(words []dgWord) float64 {
	if len(words) == 0 {
		return 0
	}
	return words[len(words)-1].End
}

func deepgramLiveSpeakerID(words []dgWord, final bool) string {
	if !final {
		return ""
	}
	counts := map[int]int{}
	bestSpeaker := 0
	bestCount := 0
	for _, word := range words {
		if word.Speaker == nil {
			continue
		}
		counts[*word.Speaker]++
		if counts[*word.Speaker] > bestCount {
			bestSpeaker = *word.Speaker
			bestCount = counts[*word.Speaker]
		}
	}
	if bestCount == 0 {
		return ""
	}
	return "S" + strconv.Itoa(bestSpeaker)
}

func deepgramTimedStrings(words []dgWord) []stt.TimedString {
	return deepgramTimedStringsOffset(words, 0)
}

func deepgramTimedStringsOffset(words []dgWord, startTimeOffset float64) []stt.TimedString {
	if len(words) == 0 {
		return nil
	}

	timed := make([]stt.TimedString, 0, len(words))
	for _, word := range words {
		timed = append(timed, stt.TimedString{
			Text:            word.Word,
			StartTime:       word.Start + startTimeOffset,
			EndTime:         word.End + startTimeOffset,
			Confidence:      word.Confidence,
			StartTimeOffset: startTimeOffset,
		})
	}
	return timed
}

func (s *deepgramStream) readLoop(conn *websocket.Conn) {
	defer func() {
		s.mu.Lock()
		stale := conn != s.conn
		s.mu.Unlock()
		if stale {
			return
		}
		_ = s.Close()
		close(s.events)
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && err != io.EOF {
				logger.Logger.Errorw("Deepgram WebSocket read error", err)
				s.sendError(deepgramSTTUnexpectedCloseError(err))
			}
			return
		}

		var resp dgResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		switch resp.Type {
		case "SpeechStarted":
			if s.speaking {
				continue
			}
			s.speaking = true
			s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})

		case "Results":
			if event := deepgramSpeechEventForLanguageOffset(resp, s.language, s.StartTimeOffset()); event != nil {
				if !s.speaking {
					s.speaking = true
					s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech})
				}
				s.sendEvent(event)
			}

			if resp.SpeechFinal && s.speaking {
				s.speaking = false
				s.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventEndOfSpeech})
			}
		}
	}
}

func deepgramSTTUnexpectedCloseError(err error) error {
	statusCode := -1
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) && closeErr.Code != 0 {
		statusCode = closeErr.Code
	}
	return llm.NewAPIStatusError("deepgram connection closed unexpectedly", statusCode, "", err.Error())
}

// keepAliveLoop sends a native KeepAlive payload to prevent Deepgram from dropping idle streams.
func (s *deepgramStream) keepAliveLoop() {
	ticker := time.NewTicker(deepgramSTTKeepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if !s.closed {
				_ = s.conn.WriteJSON(map[string]string{"type": "KeepAlive"})
			}
			s.mu.Unlock()
		}
	}
}

func (s *deepgramStream) sendEvent(ev *stt.SpeechEvent) {
	select {
	case <-s.ctx.Done():
	case s.events <- ev:
	}
}

func (s *deepgramStream) sendError(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *deepgramStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offset
}

func (s *deepgramStream) SetStartTimeOffset(offset float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if offset < 0 {
		offset = 0
	}
	s.offset = offset
}

func (s *deepgramStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.start
}

func (s *deepgramStream) SetStartTime(startTime float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if startTime < 0 {
		startTime = 0
	}
	s.start = startTime
}

func (s *deepgramStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.reconnectNext {
		if err := s.reconnectLocked(); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
		s.reconnectNext = false
	}
	if s.audioBStream == nil {
		s.audioBStream = newDeepgramSTTAudioByteStream(s)
	}
	for _, chunk := range s.audioBStream.Push(frame.Data) {
		if err := s.writeBinaryData(chunk.Data); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
	}
	return nil
}

func (s *deepgramStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.audioBStream != nil {
		for _, chunk := range s.audioBStream.Flush() {
			if err := s.writeBinaryData(chunk.Data); err != nil {
				s.closeAfterWriteFailureLocked()
				return err
			}
		}
	}
	if err := s.writeJSONData(map[string]string{"type": "Finalize"}); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

func (s *deepgramStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	_ = s.writeJSONData(map[string]string{"type": "CloseStream"})
	// Wait a tiny bit for the final transcript
	time.Sleep(50 * time.Millisecond)
	if s.cancel != nil {
		s.cancel()
	}
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	return s.closeConnection()
}

func (s *deepgramStream) updateOptions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.provider == nil {
		return
	}
	nextURL := buildDeepgramStreamURL(s.provider, s.language)
	if nextURL != s.streamURL {
		s.streamURL = nextURL
		s.reconnectNext = true
	}
	s.sampleRate = s.provider.sampleRate
	s.numChannels = s.provider.numChannels
	s.audioBStream = nil
}

func (s *deepgramStream) reconnectLocked() error {
	if s.closed {
		return io.ErrClosedPipe
	}
	if s.provider == nil {
		return nil
	}
	header := make(http.Header)
	header.Set("Authorization", "Token "+s.provider.apiKey)
	conn, err := openDeepgramStreamConnection(s.ctx, s.provider, s.streamURL, header)
	if err != nil {
		return err
	}
	oldConn := s.conn
	s.conn = conn
	_ = oldConn.Close()
	go s.readLoop(conn)
	return nil
}

func newDeepgramSTTAudioByteStream(s *deepgramStream) *audio.AudioByteStream {
	sampleRate := uint32(s.sampleRate)
	if sampleRate == 0 {
		sampleRate = 16000
	}
	numChannels := uint32(s.numChannels)
	if numChannels == 0 {
		numChannels = 1
	}
	return audio.NewAudioByteStream(sampleRate, numChannels, sampleRate/20)
}

func (s *deepgramStream) writeBinaryData(data []byte) error {
	if s.writeBinary != nil {
		return s.writeBinary(data)
	}
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (s *deepgramStream) writeJSONData(payload any) error {
	if s.writeJSON != nil {
		return s.writeJSON(payload)
	}
	if s.conn == nil {
		return io.ErrClosedPipe
	}
	return s.conn.WriteJSON(payload)
}

func (s *deepgramStream) closeAfterWriteFailureLocked() {
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	_ = s.closeConnection()
}

func (s *deepgramStream) closeConnection() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *deepgramStream) Next() (*stt.SpeechEvent, error) {
	select {
	case <-s.ctx.Done():
		return nil, io.EOF
	case err := <-s.errCh:
		return nil, err
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
	}
}
