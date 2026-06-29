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
	language          string
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
	closed            bool
}

type DeepgramKeyword struct {
	Keyword string
	Boost   float64
}

type DeepgramSTTOption func(*DeepgramSTT)

func WithDeepgramSTTModel(model string) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		if model != "" {
			s.model = model
		}
	}
}

const deepgramSTTKeepAliveInterval = 5 * time.Second
const deepgramSTTUsageInterval = 5 * time.Second
const deepgramSTTRequestTimeout = 30 * time.Second
const deepgramSTTKeepAliveMessage = `{"type": "KeepAlive"}`
const deepgramSTTFinalizeMessage = `{"type": "Finalize"}`
const deepgramSTTCloseStreamMessage = `{"type": "CloseStream"}`

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

func WithDeepgramSTTLanguage(languageStr string) DeepgramSTTOption {
	return func(s *DeepgramSTT) {
		if languageStr != "" {
			s.language = language.NormalizeLanguage(languageStr)
		}
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
		language:       "en-US",
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
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
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

func (s *DeepgramSTT) UpdateOptions(opts ...DeepgramSTTOption) error {
	s.mu.Lock()
	next := &DeepgramSTT{
		apiKey:            s.apiKey,
		model:             s.model,
		language:          s.language,
		punctuate:         s.punctuate,
		smartFormat:       s.smartFormat,
		noDelay:           s.noDelay,
		endpointingMS:     s.endpointingMS,
		enableDiarization: s.enableDiarization,
		fillerWords:       s.fillerWords,
		sampleRate:        s.sampleRate,
		numChannels:       s.numChannels,
		interimResults:    s.interimResults,
		vadEvents:         s.vadEvents,
		detectLanguage:    s.detectLanguage,
		profanityFilter:   s.profanityFilter,
		numerals:          s.numerals,
		mipOptOut:         s.mipOptOut,
		keywords:          append([]DeepgramKeyword(nil), s.keywords...),
		keyterms:          append([]string(nil), s.keyterms...),
		redact:            append([]string(nil), s.redact...),
		tags:              append([]string(nil), s.tags...),
		baseURL:           s.baseURL,
	}
	oldLanguage := s.language
	for _, opt := range opts {
		opt(next)
	}
	if err := validateDeepgramSTTOptions(next); err != nil {
		s.mu.Unlock()
		return err
	}
	s.apiKey = next.apiKey
	s.model = next.model
	s.language = next.language
	s.punctuate = next.punctuate
	s.smartFormat = next.smartFormat
	s.noDelay = next.noDelay
	s.endpointingMS = next.endpointingMS
	s.enableDiarization = next.enableDiarization
	s.fillerWords = next.fillerWords
	s.sampleRate = next.sampleRate
	s.numChannels = next.numChannels
	s.interimResults = next.interimResults
	s.vadEvents = next.vadEvents
	s.detectLanguage = next.detectLanguage
	s.profanityFilter = next.profanityFilter
	s.numerals = next.numerals
	s.mipOptOut = next.mipOptOut
	s.keywords = next.keywords
	s.keyterms = next.keyterms
	s.redact = next.redact
	s.tags = next.tags
	s.baseURL = next.baseURL
	languageChanged := oldLanguage != s.language
	streams := make([]*deepgramStream, 0, len(s.streams))
	for stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()
	for _, stream := range streams {
		stream.updateOptions(languageChanged)
	}
	return nil
}

func (s *DeepgramSTT) Stream(ctx context.Context, languageStr string) (stt.RecognizeStream, error) {
	if s.isClosed() {
		return nil, io.ErrClosedPipe
	}
	if err := validateDeepgramSTTAPIKey(s.apiKey); err != nil {
		return nil, err
	}
	if err := validateDeepgramSTTOptions(s); err != nil {
		return nil, err
	}

	languageStr = s.resolveLanguage(languageStr)
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
	if s.isClosed() {
		conn.Close()
		return nil, io.ErrClosedPipe
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream := &deepgramStream{
		provider:          s,
		conn:              conn,
		streamURL:         streamURL,
		events:            make(chan *stt.SpeechEvent, 100),
		errCh:             make(chan error, 1),
		ctx:               streamCtx,
		cancel:            cancel,
		sampleRate:        s.sampleRate,
		numChannels:       s.numChannels,
		language:          languageStr,
		enableDiarization: s.enableDiarization,
		tags:              append([]string(nil), s.tags...),
		connStart:         time.Now(),
	}
	if !s.registerStream(stream) {
		cancel()
		conn.Close()
		return nil, io.ErrClosedPipe
	}

	go stream.readLoop(conn)
	go stream.keepAliveLoop()

	return stream, nil
}

func (s *DeepgramSTT) isClosed() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func openDeepgramStreamConnection(ctx context.Context, s *DeepgramSTT, streamURL string, header http.Header) (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, streamURL, header)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError("failed to connect to deepgram")
	}
	return conn, nil
}

func (s *DeepgramSTT) registerStream(stream *deepgramStream) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if s.streams == nil {
		s.streams = make(map[*deepgramStream]struct{})
	}
	s.streams[stream] = struct{}{}
	return true
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

	languageStr = s.resolveLanguage(languageStr)
	if s.detectLanguage {
		languageStr = ""
	}

	wav := deepgramSTTWAVBytes(frames, uint32(s.sampleRate), uint32(s.numChannels))

	reqCtx, cancel := context.WithTimeout(ctx, deepgramSTTRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", buildDeepgramRecognizeURL(s, languageStr), bytes.NewReader(wav))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "audio/wav")
	req.Header.Set("Authorization", "Token "+s.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, llm.NewAPITimeoutError(err.Error())
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, llm.NewAPIStatusError("Deepgram STT request failed", resp.StatusCode, "", string(respBody))
	}

	var result dgRecognitionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		return nil, llm.NewAPIConnectionError(err.Error())
	}
	if err := deepgramRecognizeValidateReferenceResponse(result); err != nil {
		return nil, llm.NewAPIConnectionError(err.Error())
	}

	return deepgramRecognizeSpeechEventForLanguage(result, languageStr), nil
}

func (s *DeepgramSTT) resolveLanguage(languageStr string) string {
	if normalized := language.NormalizeLanguage(languageStr); normalized != "" {
		return normalized
	}
	return s.language
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
	languageStr = s.resolveLanguage(languageStr)
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
	languageStr = s.resolveLanguage(languageStr)
	if s.detectLanguage {
		languageStr = ""
	}
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
	addDeepgramSTTRecognizeAdvancedQuery(q, s)
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

func addDeepgramSTTRecognizeAdvancedQuery(q url.Values, s *DeepgramSTT) {
	for _, keyword := range s.keywords {
		if keyword.Keyword != "" {
			q.Add("keywords", keyword.Keyword+":"+strconv.FormatFloat(keyword.Boost, 'f', -1, 64))
		}
	}
	for _, redact := range s.redact {
		if redact != "" {
			q.Add("redact", redact)
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
	provider       *DeepgramSTT
	conn           *websocket.Conn
	streamURL      string
	events         chan *stt.SpeechEvent
	errCh          chan error
	mu             sync.Mutex
	closed         bool
	eventsClosed   bool
	closeDraining  bool
	inputEnded     bool
	speaking       bool
	reconnectNext  bool
	requestID      string
	start          float64
	offset         float64
	connStart      time.Time
	reportedAudio  float64
	usageTotal     float64
	usageLastFlush time.Time

	ctx    context.Context
	cancel context.CancelFunc

	sampleRate        int
	numChannels       int
	language          string
	enableDiarization bool
	tags              []string
	rateGuard         stt.SampleRateGuard
	inputAudio        deepgramSTTInputAudioNormalizer
	audioBStream      *audio.AudioByteStream
	writeBinary       func([]byte) error
	writeJSON         func(any) error
	writeText         func(string) error
}

type dgWord struct {
	Word       string  `json:"word"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
	Speaker    *int    `json:"speaker,omitempty"`
}

type dgAlternative struct {
	Transcript     string
	Confidence     float64
	confidenceSeen bool
	Languages      []string
	languagesSeen  bool
	parsedJSON     bool
	Words          []dgWord
	wordsSeen      bool
}

func (a *dgAlternative) UnmarshalJSON(data []byte) error {
	var raw struct {
		Transcript string   `json:"transcript"`
		Confidence float64  `json:"confidence"`
		Languages  []string `json:"languages"`
		Words      []dgWord `json:"words"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	a.Transcript = raw.Transcript
	a.Confidence = raw.Confidence
	a.Languages = raw.Languages
	a.Words = raw.Words
	a.parsedJSON = true
	_, a.confidenceSeen = fields["confidence"]
	_, a.languagesSeen = fields["languages"]
	_, a.wordsSeen = fields["words"]
	return nil
}

type dgRecognitionChannel struct {
	Alternatives     []dgAlternative
	DetectedLanguage string
	alternativesSeen bool
}

func (c *dgRecognitionChannel) UnmarshalJSON(data []byte) error {
	var raw struct {
		Alternatives     []dgAlternative `json:"alternatives"`
		DetectedLanguage string          `json:"detected_language"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	c.Alternatives = raw.Alternatives
	c.DetectedLanguage = raw.DetectedLanguage
	_, c.alternativesSeen = fields["alternatives"]
	return nil
}

type dgRecognitionResponse struct {
	Metadata struct {
		RequestID string `json:"request_id"`
	} `json:"metadata"`
	Results struct {
		Channels []dgRecognitionChannel `json:"channels"`
	} `json:"results"`
	requestIDSeen bool
	channelsSeen  bool
}

func (r *dgRecognitionResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
		Metadata struct {
			RequestID string `json:"request_id"`
		} `json:"metadata"`
		Results struct {
			Channels []dgRecognitionChannel `json:"channels"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var metadataFields map[string]json.RawMessage
	if metadataRaw, ok := fields["metadata"]; ok {
		_ = json.Unmarshal(metadataRaw, &metadataFields)
	}
	var resultFields map[string]json.RawMessage
	if resultsRaw, ok := fields["results"]; ok {
		_ = json.Unmarshal(resultsRaw, &resultFields)
	}

	r.Metadata = raw.Metadata
	r.Results = raw.Results
	_, r.requestIDSeen = metadataFields["request_id"]
	_, r.channelsSeen = resultFields["channels"]
	return nil
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
	parsedJSON       bool
	isFinalSeen      bool
	speechFinalSeen  bool
	requestIDSeen    bool
	alternativesSeen bool
}

func (r *dgResponse) UnmarshalJSON(data []byte) error {
	var raw struct {
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
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var metadataFields map[string]json.RawMessage
	if metadataRaw, ok := fields["metadata"]; ok {
		_ = json.Unmarshal(metadataRaw, &metadataFields)
	}
	var channelFields map[string]json.RawMessage
	if channelRaw, ok := fields["channel"]; ok {
		_ = json.Unmarshal(channelRaw, &channelFields)
	}

	r.Type = raw.Type
	r.IsFinal = raw.IsFinal
	r.SpeechFinal = raw.SpeechFinal
	r.Channel = raw.Channel
	r.Start = raw.Start
	r.Duration = raw.Duration
	r.Metadata = raw.Metadata
	r.parsedJSON = true
	_, r.isFinalSeen = fields["is_final"]
	_, r.speechFinalSeen = fields["speech_final"]
	_, r.requestIDSeen = metadataFields["request_id"]
	_, r.alternativesSeen = channelFields["alternatives"]
	return nil
}

func deepgramRecognizeSpeechEvent(resp dgRecognitionResponse) *stt.SpeechEvent {
	return deepgramRecognizeSpeechEventForLanguage(resp, "")
}

func deepgramRecognizeValidateReferenceResponse(resp dgRecognitionResponse) error {
	if !resp.requestIDSeen || !resp.channelsSeen || len(resp.Results.Channels) == 0 {
		return fmt.Errorf("malformed deepgram recognition response")
	}
	channel := resp.Results.Channels[0]
	if !channel.alternativesSeen {
		return fmt.Errorf("malformed deepgram recognition channel")
	}
	for _, alt := range channel.Alternatives {
		if alt.parsedJSON && (!alt.confidenceSeen || !alt.wordsSeen) {
			return fmt.Errorf("malformed deepgram recognition alternative")
		}
	}
	return nil
}

func deepgramRecognizeSpeechEventForLanguage(resp dgRecognitionResponse, languageStr string) *stt.SpeechEvent {
	event := &stt.SpeechEvent{
		Type:      stt.SpeechEventFinalTranscript,
		RequestID: resp.Metadata.RequestID,
	}

	if len(resp.Results.Channels) == 0 || len(resp.Results.Channels[0].Alternatives) == 0 {
		return event
	}

	channel := resp.Results.Channels[0]
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
		if deepgramLiveMalformedAlternative(alt) {
			return nil
		}
		if deepgramLiveMissingDetectedLanguage(languageStr, alt) {
			return nil
		}
		transcriptBuilder += alt.Transcript
		startTime, endTime := deepgramLiveTranscriptTimes(resp, alt.Words)
		event.Alternatives = append(event.Alternatives, stt.SpeechData{
			Language:   deepgramLiveLanguage(languageStr, alt.Languages),
			Text:       alt.Transcript,
			Confidence: alt.Confidence,
			StartTime:  startTime + startTimeOffset,
			EndTime:    endTime + startTimeOffset,
			SpeakerID:  deepgramLiveSpeakerID(alt.Words, resp.IsFinal),
			Words:      deepgramTimedStringsOffset(alt.Words, startTimeOffset),
		})
	}

	if transcriptBuilder == "" {
		return nil
	}

	return event
}

func deepgramLiveMalformedAlternative(alt dgAlternative) bool {
	return alt.parsedJSON && (!alt.confidenceSeen || !alt.wordsSeen)
}

func deepgramLiveMissingDetectedLanguage(languageStr string, alt dgAlternative) bool {
	return languageStr == "multi" && alt.languagesSeen && len(alt.Languages) == 0
}

func deepgramLiveTranscriptTimes(resp dgResponse, words []dgWord) (float64, float64) {
	return deepgramFirstWordStart(words), deepgramFirstWordEnd(words)
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
			StartTimeOffset: startTimeOffset,
		})
	}
	return timed
}

func (s *deepgramStream) readLoop(conn *websocket.Conn) {
	defer func() {
		s.mu.Lock()
		stale := conn != s.conn
		if !stale && !s.closed {
			s.closed = true
			if s.cancel != nil {
				s.cancel()
			}
			if s.provider != nil {
				s.provider.unregisterStream(s)
			}
			_ = s.closeConnection()
		}
		closeEvents := !stale && !s.closeDraining
		if closeEvents {
			s.eventsClosed = true
		}
		s.mu.Unlock()
		if stale || !closeEvents {
			return
		}
		close(s.events)
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if !s.isClosed() && !s.hasInputEnded() {
				if reconnectErr := s.reconnectAfterUnexpectedClose(conn); reconnectErr == nil {
					return
				} else {
					logger.Logger.Errorw("Deepgram WebSocket reconnect error", reconnectErr)
					s.sendError(reconnectErr)
				}
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
			if resp.malformedReferenceResults() {
				continue
			}
			if resp.malformedReferenceLanguage(s.language) {
				continue
			}
			s.setRequestID(resp.Metadata.RequestID)
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

func (r dgResponse) malformedReferenceResults() bool {
	if !r.parsedJSON {
		return false
	}
	if !r.isFinalSeen || !r.speechFinalSeen || !r.requestIDSeen || !r.alternativesSeen {
		return true
	}
	for _, alt := range r.Channel.Alternatives {
		if deepgramLiveMalformedAlternative(alt) {
			return true
		}
	}
	return false
}

func (r dgResponse) malformedReferenceLanguage(languageStr string) bool {
	if languageStr != "multi" {
		return false
	}
	for _, alt := range r.Channel.Alternatives {
		if deepgramLiveMissingDetectedLanguage(languageStr, alt) {
			return true
		}
	}
	return false
}

// keepAliveLoop sends a native KeepAlive payload to prevent Deepgram from dropping idle streams.
func (s *deepgramStream) keepAliveLoop() {
	ticker := time.NewTicker(deepgramSTTKeepAliveInterval)
	defer ticker.Stop()
	s.sendKeepAlive()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.sendKeepAlive()
		}
	}
}

func (s *deepgramStream) sendKeepAlive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.sendKeepAliveLocked()
	}
}

func (s *deepgramStream) sendKeepAliveLocked() {
	_ = s.writeTextData(deepgramSTTKeepAliveMessage, map[string]string{"type": "KeepAlive"})
}

func (s *deepgramStream) sendEvent(ev *stt.SpeechEvent) {
	select {
	case <-s.ctx.Done():
	case s.events <- ev:
	}
}

func (s *deepgramStream) sendRecognitionUsage(frame *model.AudioFrame) {
	if s.ctx == nil || s.events == nil || frame == nil {
		return
	}
	duration := audio.CalculateFrameDuration(frame)
	if duration <= 0 {
		return
	}
	s.usageTotal += duration
	if s.usageLastFlush.IsZero() {
		s.usageLastFlush = time.Now()
		return
	}
	if time.Since(s.usageLastFlush) >= deepgramSTTUsageInterval {
		s.flushRecognitionUsageLocked()
	}
}

func (s *deepgramStream) sendRecognitionUsageDuration(duration float64) {
	if s.ctx == nil || s.events == nil || duration <= 0 {
		return
	}
	s.reportedAudio += duration
	s.sendEvent(&stt.SpeechEvent{
		Type:      stt.SpeechEventRecognitionUsage,
		RequestID: s.requestID,
		RecognitionUsage: &stt.RecognitionUsage{
			AudioDuration: duration,
		},
	})
}

func (s *deepgramStream) flushRecognitionUsageLocked() {
	if s.usageTotal <= 0 {
		return
	}
	duration := s.usageTotal
	s.usageTotal = 0
	s.usageLastFlush = time.Now()
	s.sendRecognitionUsageDuration(duration)
}

func (s *deepgramStream) sendError(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *deepgramStream) setRequestID(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requestID = requestID
}

func (s *deepgramStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offset
}

func (s *deepgramStream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offset = offset
}

func (s *deepgramStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.start
}

func (s *deepgramStream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.start = startTime
}

func (s *deepgramStream) PushFrame(frame *model.AudioFrame) error {
	if frame == nil || len(frame.Data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
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
	if err := s.rateGuard.Check(frame); err != nil {
		return err
	}
	normalizedFrame, err := s.inputAudio.normalize(frame, uint32(s.sampleRate))
	if err != nil {
		return err
	}
	if s.audioBStream == nil {
		s.audioBStream = newDeepgramSTTAudioByteStream(s)
	}
	for _, chunk := range s.audioBStream.Push(normalizedFrame.Data) {
		if err := s.writeBinaryData(chunk.Data); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
		s.sendRecognitionUsage(chunk)
	}
	return nil
}

func (s *deepgramStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	flushedFrame := false
	if tail := s.inputAudio.flush(); tail != nil {
		if s.audioBStream == nil {
			s.audioBStream = newDeepgramSTTAudioByteStream(s)
		}
		for _, chunk := range s.audioBStream.Push(tail.Data) {
			flushedFrame = true
			if err := s.writeBinaryData(chunk.Data); err != nil {
				s.closeAfterWriteFailureLocked()
				return err
			}
			s.sendRecognitionUsage(chunk)
		}
	}
	if s.audioBStream != nil {
		for _, chunk := range s.audioBStream.Flush() {
			flushedFrame = true
			if err := s.writeBinaryData(chunk.Data); err != nil {
				s.closeAfterWriteFailureLocked()
				return err
			}
			s.sendRecognitionUsage(chunk)
		}
	}
	if !flushedFrame {
		return nil
	}
	s.flushRecognitionUsageLocked()
	if err := s.writeTextData(deepgramSTTFinalizeMessage, map[string]string{"type": "Finalize"}); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	return nil
}

type deepgramSTTInputAudioNormalizer struct {
	sampleRate  uint32
	numChannels uint32
	targetRate  uint32
	remainder   uint64
	lastSample  []byte
}

func (n *deepgramSTTInputAudioNormalizer) normalize(frame *model.AudioFrame, targetRate uint32) (*model.AudioFrame, error) {
	if frame == nil || targetRate == 0 || frame.SampleRate == targetRate {
		n.reset()
		return frame, nil
	}
	if frame.SampleRate == 0 {
		return nil, fmt.Errorf("cannot resample audio with zero sample rate")
	}
	if frame.NumChannels == 0 {
		return nil, fmt.Errorf("cannot resample audio with zero channels")
	}
	if len(frame.Data)%2 != 0 {
		return nil, fmt.Errorf("cannot resample non-16-bit PCM audio")
	}
	samplesPerChannel := frame.SamplesPerChannel
	if samplesPerChannel == 0 {
		samplesPerChannel = uint32(len(frame.Data)) / frame.NumChannels / 2
	}
	expectedBytes := int(samplesPerChannel * frame.NumChannels * 2)
	if len(frame.Data) < expectedBytes {
		return nil, fmt.Errorf("audio frame data is shorter than declared sample count")
	}
	if n.sampleRate != frame.SampleRate || n.numChannels != frame.NumChannels || n.targetRate != targetRate {
		n.sampleRate = frame.SampleRate
		n.numChannels = frame.NumChannels
		n.targetRate = targetRate
		n.remainder = 0
		n.lastSample = nil
	}
	if samplesPerChannel == 0 {
		return &model.AudioFrame{
			SampleRate:        targetRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: 0,
			ParticipantID:     frame.ParticipantID,
		}, nil
	}

	scaledSamples := uint64(samplesPerChannel)*uint64(targetRate) + n.remainder
	outSamples := uint32(scaledSamples / uint64(frame.SampleRate))
	n.remainder = scaledSamples % uint64(frame.SampleRate)
	out := make([]byte, int(outSamples*frame.NumChannels*2))
	channelCount := int(frame.NumChannels)
	for outIdx := uint32(0); outIdx < outSamples; outIdx++ {
		srcIdx := uint32(uint64(outIdx) * uint64(frame.SampleRate) / uint64(targetRate))
		if srcIdx >= samplesPerChannel {
			srcIdx = samplesPerChannel - 1
		}
		for ch := 0; ch < channelCount; ch++ {
			inOffset := (int(srcIdx)*channelCount + ch) * 2
			outOffset := (int(outIdx)*channelCount + ch) * 2
			copy(out[outOffset:outOffset+2], frame.Data[inOffset:inOffset+2])
		}
	}
	if n.remainder > 0 {
		offset := int((samplesPerChannel - 1) * frame.NumChannels * 2)
		n.lastSample = append(n.lastSample[:0], frame.Data[offset:offset+int(frame.NumChannels*2)]...)
	} else {
		n.lastSample = nil
	}

	return &model.AudioFrame{
		Data:              out,
		SampleRate:        targetRate,
		NumChannels:       frame.NumChannels,
		SamplesPerChannel: outSamples,
		ParticipantID:     frame.ParticipantID,
	}, nil
}

func (n *deepgramSTTInputAudioNormalizer) flush() *model.AudioFrame {
	if n == nil || n.remainder == 0 || len(n.lastSample) == 0 || n.targetRate == 0 || n.numChannels == 0 {
		return nil
	}
	data := append([]byte(nil), n.lastSample...)
	frame := &model.AudioFrame{
		Data:              data,
		SampleRate:        n.targetRate,
		NumChannels:       n.numChannels,
		SamplesPerChannel: 1,
	}
	n.remainder = 0
	n.lastSample = nil
	return frame
}

func (n *deepgramSTTInputAudioNormalizer) reset() {
	n.sampleRate = 0
	n.numChannels = 0
	n.targetRate = 0
	n.remainder = 0
	n.lastSample = nil
}

func (s *deepgramStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if s.closed {
		return io.ErrClosedPipe
	}
	flushedFrame := false
	if tail := s.inputAudio.flush(); tail != nil {
		if s.audioBStream == nil {
			s.audioBStream = newDeepgramSTTAudioByteStream(s)
		}
		for _, chunk := range s.audioBStream.Push(tail.Data) {
			flushedFrame = true
			if err := s.writeBinaryData(chunk.Data); err != nil {
				s.closeAfterWriteFailureLocked()
				return err
			}
			s.sendRecognitionUsage(chunk)
		}
	}
	if s.audioBStream != nil {
		for _, chunk := range s.audioBStream.Flush() {
			flushedFrame = true
			if err := s.writeBinaryData(chunk.Data); err != nil {
				s.closeAfterWriteFailureLocked()
				return err
			}
			s.sendRecognitionUsage(chunk)
		}
	}
	if flushedFrame {
		s.flushRecognitionUsageLocked()
		if err := s.writeTextData(deepgramSTTFinalizeMessage, map[string]string{"type": "Finalize"}); err != nil {
			s.closeAfterWriteFailureLocked()
			return err
		}
	}
	if err := s.writeTextData(deepgramSTTCloseStreamMessage, map[string]string{"type": "CloseStream"}); err != nil {
		s.closeAfterWriteFailureLocked()
		return err
	}
	s.inputEnded = true
	return nil
}

func (s *deepgramStream) sendConnectionUsageRemainderLocked() {
	if s.eventsClosed {
		return
	}
	if s.connStart.IsZero() {
		return
	}
	s.flushRecognitionUsageLocked()
	remainder := time.Since(s.connStart).Seconds() - s.reportedAudio
	s.connStart = time.Time{}
	s.reportedAudio = 0
	if remainder > 0 {
		s.sendRecognitionUsageDuration(remainder)
	}
}

func (s *deepgramStream) cancelIfUsageDeliveryWouldBlockLocked() {
	if s.cancel == nil || s.events == nil {
		return
	}
	if s.usageTotal <= 0 && s.connStart.IsZero() {
		return
	}
	if cap(s.events) == 0 || len(s.events) >= cap(s.events) {
		s.cancel()
	}
}

func (s *deepgramStream) Close() error {
	if !s.mu.TryLock() {
		if s.cancel != nil {
			s.cancel()
		}
		s.mu.Lock()
	}
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	shouldSendClose := !s.inputEnded
	s.closed = true
	s.inputEnded = true
	s.closeDraining = true
	if shouldSendClose {
		_ = s.writeTextData(deepgramSTTCloseStreamMessage, map[string]string{"type": "CloseStream"})
	}
	s.mu.Unlock()

	// Keep receive-side state unlocked so a final transcript can drain after CloseStream.
	time.Sleep(50 * time.Millisecond)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeDraining = false
	s.cancelIfUsageDeliveryWouldBlockLocked()
	s.sendConnectionUsageRemainderLocked()
	if s.cancel != nil {
		s.cancel()
	}
	if s.provider != nil {
		s.provider.unregisterStream(s)
	}
	return s.closeConnection()
}

func (s *deepgramStream) updateOptions(languageChanged bool) {
	s.mu.Lock()
	if s.closed || s.provider == nil {
		s.mu.Unlock()
		return
	}
	if languageChanged {
		s.language = s.provider.language
	}
	nextURL := buildDeepgramStreamURL(s.activeConfigLocked(), s.language)
	s.streamURL = nextURL
	s.reconnectNext = true
	reconnectNow := s.conn != nil
	s.sampleRate = s.provider.sampleRate
	s.numChannels = s.provider.numChannels
	s.audioBStream = nil
	s.mu.Unlock()

	if reconnectNow {
		go s.reconnectNow()
	}
}

func (s *deepgramStream) activeConfigLocked() *DeepgramSTT {
	return &DeepgramSTT{
		model:             s.provider.model,
		language:          s.provider.language,
		punctuate:         s.provider.punctuate,
		smartFormat:       s.provider.smartFormat,
		noDelay:           s.provider.noDelay,
		endpointingMS:     s.provider.endpointingMS,
		enableDiarization: s.enableDiarization,
		fillerWords:       s.provider.fillerWords,
		sampleRate:        s.provider.sampleRate,
		numChannels:       s.provider.numChannels,
		interimResults:    s.provider.interimResults,
		vadEvents:         s.provider.vadEvents,
		profanityFilter:   s.provider.profanityFilter,
		numerals:          s.provider.numerals,
		mipOptOut:         s.provider.mipOptOut,
		keywords:          append([]DeepgramKeyword(nil), s.provider.keywords...),
		keyterms:          append([]string(nil), s.provider.keyterms...),
		redact:            append([]string(nil), s.provider.redact...),
		tags:              append([]string(nil), s.tags...),
		baseURL:           s.provider.baseURL,
	}
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
	s.sendConnectionUsageRemainderLocked()
	s.connStart = time.Now()
	_ = oldConn.Close()
	go s.readLoop(conn)
	s.sendKeepAliveLocked()
	return nil
}

func (s *deepgramStream) reconnectAfterUnexpectedClose(conn *websocket.Conn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.inputEnded || conn != s.conn {
		return nil
	}
	s.advanceTimingForReconnectLocked(time.Now())
	s.audioBStream = nil
	if err := s.reconnectLocked(); err != nil {
		s.closed = true
		if s.cancel != nil {
			s.cancel()
		}
		_ = s.closeConnection()
		return err
	}
	s.reconnectNext = false
	return nil
}

func (s *deepgramStream) advanceTimingForReconnectLocked(now time.Time) {
	nowSeconds := float64(now.UnixNano()) / 1e9
	if s.start > 0 && nowSeconds > s.start {
		s.offset += nowSeconds - s.start
	}
	s.start = nowSeconds
}

func (s *deepgramStream) reconnectNow() {
	s.mu.Lock()
	if s.closed || !s.reconnectNext {
		s.mu.Unlock()
		return
	}
	err := s.reconnectLocked()
	if err == nil {
		s.reconnectNext = false
	} else {
		s.closed = true
		if s.cancel != nil {
			s.cancel()
		}
		_ = s.closeConnection()
	}
	s.mu.Unlock()

	if err != nil {
		s.sendError(err)
	}
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

func (s *deepgramStream) writeTextData(payload string, fallback any) error {
	if s.writeText != nil {
		return s.writeText(payload)
	}
	if s.conn != nil {
		return s.conn.WriteMessage(websocket.TextMessage, []byte(payload))
	}
	return s.writeJSONData(fallback)
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

func (s *deepgramStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *deepgramStream) hasInputEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inputEnded
}

func (s *deepgramStream) Next() (*stt.SpeechEvent, error) {
	select {
	case event, ok := <-s.events:
		if ok {
			return event, nil
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
			return nil, io.EOF
		}
	default:
	}

	if s.isClosed() {
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		select {
		case err := <-s.errCh:
			return nil, err
		default:
		}
		return nil, io.EOF
	}

	select {
	case <-s.ctx.Done():
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
		return nil, io.EOF
	case err := <-s.errCh:
		select {
		case event, ok := <-s.events:
			if ok {
				return event, nil
			}
		default:
		}
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
