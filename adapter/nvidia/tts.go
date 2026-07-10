package nvidia

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/cavos-io/rtp-agent/core/tts"
)

const (
	defaultNvidiaTTSServer     = "grpc.nvcf.nvidia.com:443"
	defaultNvidiaTTSVoice      = "Magpie-Multilingual.EN-US.Leo"
	defaultNvidiaTTSFunctionID = "877104f7-e885-42b9-8de8-f6e4c6303969"
	defaultNvidiaTTSLanguage   = "en-US"
	defaultNvidiaTTSSampleRate = 16000
	nvidiaAPIKeyEnv            = "NVIDIA_API_KEY"
	nvidiaTTSMissingAPIKey     = "NVIDIA_API_KEY is not set while using SSL. Either pass api_key parameter, set NVIDIA_API_KEY environment variable or disable SSL and use a locally hosted Riva NIM service."
	nvidiaTTSWhitespaceCutset  = " \t\r\n\f\v"
)

var nvidiaTTSNewlineWhitespace = regexp.MustCompile(`\s*\n+\s*`)

type NvidiaTTS struct {
	apiKey       string
	voice        string
	functionID   string
	server       string
	sampleRate   int
	useSSL       bool
	languageCode string
}

type NvidiaTTSOption func(*NvidiaTTS)

func WithNvidiaTTSAPIKey(apiKey string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		if apiKey != "" {
			t.apiKey = apiKey
		}
	}
}

func WithNvidiaTTSServer(server string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		t.server = server
	}
}

func WithNvidiaTTSFunctionID(functionID string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		t.functionID = functionID
	}
}

func WithNvidiaTTSVoice(voice string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		t.voice = voice
	}
}

func WithNvidiaTTSLanguageCode(languageCode string) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		t.languageCode = languageCode
	}
}

func WithNvidiaTTSUseSSL(useSSL bool) NvidiaTTSOption {
	return func(t *NvidiaTTS) {
		t.useSSL = useSSL
	}
}

func NewNvidiaTTS(apiKey string, voice string, opts ...NvidiaTTSOption) (*NvidiaTTS, error) {
	if apiKey == "" {
		apiKey = os.Getenv(nvidiaAPIKeyEnv)
	}
	if voice == "" {
		voice = defaultNvidiaTTSVoice
	}

	provider := &NvidiaTTS{
		apiKey:       apiKey,
		voice:        voice,
		functionID:   defaultNvidiaTTSFunctionID,
		server:       defaultNvidiaTTSServer,
		sampleRate:   defaultNvidiaTTSSampleRate,
		useSSL:       true,
		languageCode: defaultNvidiaTTSLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.useSSL && provider.apiKey == "" {
		return nil, fmt.Errorf("%s", nvidiaTTSMissingAPIKey)
	}
	return provider, nil
}

func (t *NvidiaTTS) Label() string    { return "nvidia.TTS" }
func (t *NvidiaTTS) Model() string    { return t.voice }
func (t *NvidiaTTS) Provider() string { return "nvidia" }
func (t *NvidiaTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true, AlignedTranscript: false}
}
func (t *NvidiaTTS) SampleRate() int  { return t.sampleRate }
func (t *NvidiaTTS) NumChannels() int { return 1 }

func (t *NvidiaTTS) Synthesize(ctx context.Context, text string) (tts.ChunkedStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &nvidiaTTSChunkedStream{ctx: ctx, text: text}, nil
}

func (t *NvidiaTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &nvidiaTTSSynthesizeStream{
		ctx:          ctx,
		stateChanged: make(chan struct{}),
	}, nil
}

type nvidiaTTSSynthesizeStream struct {
	mu            sync.Mutex
	stateChanged  chan struct{}
	ctx           context.Context
	done          bool
	closed        bool
	inputEnded    bool
	hasText       bool
	flushed       bool
	segmentClosed bool
	text          string
	pendingText   string
	readyText     []string
	queuedLen     int
	exception     error
}

type nvidiaTTSChunkedStream struct {
	ctx       context.Context
	text      string
	done      bool
	exception error
}

func (s *nvidiaTTSChunkedStream) Next() (*tts.SynthesizedAudio, error) {
	if s.done {
		if s.exception != nil {
			return nil, s.exception
		}
		return nil, io.EOF
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			return nil, err
		}
	}
	if strings.TrimSpace(s.text) == "" {
		s.done = true
		return nil, io.EOF
	}
	err := fmt.Errorf("nvidia riva tts streaming is not implemented")
	s.done = true
	s.exception = err
	return nil, err
}

func (s *nvidiaTTSChunkedStream) Close() error {
	s.done = true
	return nil
}

func (s *nvidiaTTSChunkedStream) Done() bool {
	return s.done
}

func (s *nvidiaTTSChunkedStream) Exception() error {
	return s.exception
}

func (s *nvidiaTTSSynthesizeStream) notifyLocked() {
	close(s.stateChanged)
	s.stateChanged = make(chan struct{})
}

func (s *nvidiaTTSSynthesizeStream) PushText(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.inputEnded {
		return nil
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			s.notifyLocked()
			return err
		}
	}
	if text == "" {
		return nil
	}
	if s.segmentClosed {
		return nil
	}
	text, collapsePreviousWhitespace := nvidiaTTSNormalizeInputText(text)
	if s.flushed && s.pendingText != "" {
		if collapsePreviousWhitespace {
			s.pendingText = strings.TrimRight(s.pendingText, nvidiaTTSWhitespaceCutset)
		}
		s.pendingText += text
		s.queueCompletedSentenceCandidatesLocked(s.pendingText)
		s.notifyLocked()
		return nil
	}
	s.hasText = true
	if collapsePreviousWhitespace {
		s.text = strings.TrimRight(s.text, nvidiaTTSWhitespaceCutset)
		if s.queuedLen > len(s.text) {
			s.queuedLen = len(s.text)
		}
	}
	s.text += text
	s.queueCompletedSentenceCandidatesLocked(s.text)
	s.notifyLocked()
	return nil
}

func (s *nvidiaTTSSynthesizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.inputEnded {
		return nil
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			s.notifyLocked()
			return err
		}
	}
	if s.hasText {
		s.queuePendingInputLocked()
		s.flushed = true
		s.segmentClosed = true
		s.notifyLocked()
	}
	return nil
}

func (s *nvidiaTTSSynthesizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.inputEnded {
		return nil
	}
	if s.ctx != nil {
		if err := s.ctx.Err(); err != nil {
			s.done = true
			s.exception = err
			s.notifyLocked()
			return err
		}
	}
	if s.hasText {
		s.queuePendingInputLocked()
		s.flushed = true
		s.segmentClosed = true
	}
	s.inputEnded = true
	if !s.hasText {
		s.done = true
	}
	s.notifyLocked()
	return nil
}

func (s *nvidiaTTSSynthesizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.done = true
	s.notifyLocked()
	return nil
}

func (s *nvidiaTTSSynthesizeStream) Next() (*tts.SynthesizedAudio, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.done = true
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.inputEnded && !s.hasText {
			s.done = true
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.inputEnded && strings.TrimSpace(s.text) == "" {
			s.done = true
			s.mu.Unlock()
			return nil, io.EOF
		}
		if s.ctx != nil {
			if err := s.ctx.Err(); err != nil {
				s.done = true
				s.exception = err
				s.mu.Unlock()
				return nil, err
			}
		}
		if s.flushed && s.hasText && s.nextReadyTextLocked() != "" {
			s.popReadyTextLocked()
			err := fmt.Errorf("nvidia riva tts streaming is not implemented")
			s.done = true
			s.exception = err
			s.mu.Unlock()
			return nil, err
		}
		changed := s.stateChanged
		ctx := s.ctx
		s.mu.Unlock()
		if ctx == nil {
			<-changed
			continue
		}
		select {
		case <-changed:
		case <-ctx.Done():
			s.mu.Lock()
			s.done = true
			s.exception = ctx.Err()
			s.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

func (s *nvidiaTTSSynthesizeStream) queuePendingInputLocked() {
	if s.pendingText != "" {
		s.queueReadyTextLocked(s.pendingText)
		s.pendingText = ""
		return
	}
	if s.queuedLen > len(s.text) {
		s.queuedLen = len(s.text)
	}
	s.queueReadyTextLocked(s.text[s.queuedLen:])
}

func (s *nvidiaTTSSynthesizeStream) queueCompletedSentenceCandidatesLocked(text string) {
	for {
		prefix, tail, ok := nvidiaTTSCompletedSentencePrefix(text)
		if !ok {
			return
		}
		s.text = prefix
		s.pendingText = tail
		s.queueReadyTextLocked(prefix)
		s.flushed = true
		text = tail
	}
}

func (s *nvidiaTTSSynthesizeStream) queueReadyTextLocked(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.readyText = append(s.readyText, text)
	s.queuedLen = len(s.text)
}

func (s *nvidiaTTSSynthesizeStream) nextReadyTextLocked() string {
	if len(s.readyText) > 0 {
		return s.readyText[0]
	}
	return strings.TrimSpace(s.text)
}

func (s *nvidiaTTSSynthesizeStream) popReadyTextLocked() {
	if len(s.readyText) == 0 {
		return
	}
	copy(s.readyText, s.readyText[1:])
	s.readyText[len(s.readyText)-1] = ""
	s.readyText = s.readyText[:len(s.readyText)-1]
}

func (s *nvidiaTTSSynthesizeStream) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *nvidiaTTSSynthesizeStream) Exception() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exception
}

func nvidiaTTSNormalizeInputText(text string) (string, bool) {
	return nvidiaTTSNewlineWhitespace.ReplaceAllString(text, " "), nvidiaTTSStartsWithNewlineGroup(text)
}

func nvidiaTTSStartsWithNewlineGroup(text string) bool {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			return true
		case ' ', '\t', '\r', '\f', '\v':
			continue
		default:
			return false
		}
	}
	return false
}

func nvidiaTTSCompletedSentencePrefix(text string) (string, string, bool) {
	trimmed := strings.TrimSpace(text)
	if utf8.RuneCountInString(trimmed) < 21 {
		return "", "", false
	}
	for i, r := range trimmed {
		next := i + len(string(r))
		if next >= len(trimmed) {
			break
		}
		switch r {
		case '.', '!', '?':
			if r == '.' && nvidiaTTSProtectedPeriod(trimmed, i, next) {
				continue
			}
			boundaryEnd, quoted := nvidiaTTSQuotedBoundaryEnd(trimmed, next)
			if !nvidiaTTSASCIIBoundaryTailStartsSentence(trimmed[boundaryEnd:], quoted) {
				continue
			}
			if nvidiaTTSSentenceLongEnough(trimmed[:boundaryEnd]) {
				return strings.TrimSpace(trimmed[:boundaryEnd]), strings.TrimSpace(trimmed[boundaryEnd:]), true
			}
		case '。', '！', '？':
			boundaryEnd, _ := nvidiaTTSQuotedBoundaryEnd(trimmed, next)
			if nvidiaTTSSentenceLongEnough(trimmed[:boundaryEnd]) && strings.TrimSpace(trimmed[boundaryEnd:]) != "" {
				return strings.TrimSpace(trimmed[:boundaryEnd]), strings.TrimSpace(trimmed[boundaryEnd:]), true
			}
		}
	}
	return "", "", false
}

func nvidiaTTSSentenceLongEnough(text string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(text)) >= 20
}

func nvidiaTTSASCIIBoundaryTailStartsSentence(tail string, quoted bool) bool {
	if quoted {
		return tail != "" && nvidiaTTSASCIIWhitespace(tail[0]) && strings.TrimSpace(tail) != ""
	}
	return nvidiaTTSASCIITailStartsCapital(tail)
}

func nvidiaTTSQuotedBoundaryEnd(text string, next int) (int, bool) {
	quoted := false
	for next < len(text) {
		if nvidiaTTSUnicodeCloserAt(text[next:]) {
			_, size := utf8.DecodeRuneInString(text[next:])
			next += size
			continue
		}
		switch text[next] {
		case '"':
			quoted = true
			next++
		case '\'', ')', ']', '}', ',', ':', ';':
			next++
		default:
			return next, quoted
		}
	}
	return next, quoted
}

func nvidiaTTSUnicodeCloserAt(text string) bool {
	for _, closer := range []string{"”", "’", "»", "›", "」", "』", "》", "）", "】"} {
		if strings.HasPrefix(text, closer) {
			return true
		}
	}
	return false
}

func nvidiaTTSProtectedPeriod(text string, dot int, next int) bool {
	if nvidiaTTSProtectedAbbreviation(text[:dot], text[next:]) {
		return true
	}
	if nvidiaTTSProtectedInitial(text, dot, text[next:]) {
		return true
	}
	if nvidiaTTSProtectedSuffix(text, dot, text[next:]) {
		return true
	}
	if nvidiaTTSFirstPhDDot(text, dot) {
		return true
	}
	if nvidiaTTSFinalPhDDot(text, dot) {
		return !nvidiaTTSTailStartsSentence(text[next:])
	}
	if nvidiaTTSProtectedAcronym(text, dot, text[next:]) {
		return true
	}
	if nvidiaTTSProtectedDecimal(text, dot, next) {
		return true
	}
	if nvidiaTTSProtectedWebsite(text[next:]) {
		return true
	}
	return nvidiaTTSProtectedMultipleDots(text, dot, next, text[next:])
}

func nvidiaTTSProtectedDecimal(text string, dot int, next int) bool {
	return dot > 0 && next < len(text) && text[dot-1] >= '0' && text[dot-1] <= '9' && text[next] >= '0' && text[next] <= '9'
}

func nvidiaTTSProtectedWebsite(tail string) bool {
	for _, suffix := range []string{"com", "net", "org", "io", "gov", "edu", "me"} {
		if len(tail) >= len(suffix) && strings.EqualFold(tail[:len(suffix)], suffix) {
			return true
		}
	}
	return false
}

func nvidiaTTSProtectedMultipleDots(text string, dot int, next int, tail string) bool {
	if next < len(text) && text[next] == '.' {
		return true
	}
	if dot > 0 && text[dot-1] == '.' {
		return !nvidiaTTSASCIITailStartsCapital(tail)
	}
	return false
}

func nvidiaTTSProtectedAbbreviation(prefix string, tail string) bool {
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return false
	}
	switch fields[len(fields)-1] {
	case "Mr", "St", "Mrs", "Ms", "Dr", "Prof", "Capt", "Cpt", "Lt",
		"Adm", "Col", "Gen", "Gov", "Maj", "Pres", "Rep", "Rev", "Sen", "Sgt",
		"Jan", "Feb", "Mar", "Apr", "Jun", "Jul", "Aug", "Sep", "Sept", "Oct", "Nov", "Dec":
		return !nvidiaTTSTailStartsSentence(tail)
	default:
		return false
	}
}

func nvidiaTTSProtectedAcronym(text string, dot int, tail string) bool {
	if dot < 1 || !nvidiaTTSASCIIAlpha(text[dot-1]) {
		return false
	}
	if letters := nvidiaTTSAcronymLettersEndingAt(text, dot); letters >= 2 {
		if letters > 3 {
			return false
		}
		return !nvidiaTTSTailStartsSentence(tail)
	}
	next := dot + 1
	return next+1 < len(text) && nvidiaTTSASCIIAlpha(text[next]) && text[next+1] == '.'
}

func nvidiaTTSAcronymLettersEndingAt(text string, dot int) int {
	letters := 0
	for i := dot; i >= 1; i -= 2 {
		if text[i] != '.' || !nvidiaTTSASCIIAlpha(text[i-1]) {
			break
		}
		letters++
	}
	return letters
}

func nvidiaTTSASCIIAlpha(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func nvidiaTTSFirstPhDDot(text string, dot int) bool {
	return dot >= 2 && text[dot-2:dot+1] == "Ph." && dot+2 < len(text) && text[dot+1:dot+3] == "D."
}

func nvidiaTTSFinalPhDDot(text string, dot int) bool {
	return dot >= 4 && text[dot-4:dot+1] == "Ph.D."
}

func nvidiaTTSProtectedInitial(text string, dot int, tail string) bool {
	if dot < 2 || !nvidiaTTSASCIIAlpha(text[dot-1]) {
		return false
	}
	prev := text[dot-2]
	switch prev {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return !nvidiaTTSASCIITailStartsCapital(tail)
	default:
		return false
	}
}

func nvidiaTTSASCIITailStartsCapital(tail string) bool {
	if tail == "" || !nvidiaTTSASCIIWhitespace(tail[0]) {
		return false
	}
	trimmed := strings.TrimSpace(tail)
	trimmed = strings.TrimLeft(trimmed, "\"'‘“([{")
	trimmed = strings.TrimLeft(trimmed, "-— ")
	trimmed = strings.TrimLeft(trimmed, "#*• ")
	trimmed = strings.TrimLeft(trimmed, `/\@&% `)
	return trimmed != "" && trimmed[0] >= 'A' && trimmed[0] <= 'Z'
}

func nvidiaTTSProtectedSuffix(text string, dot int, tail string) bool {
	for _, suffix := range []string{"Inc", "Ltd", "LLC", "Corp", "Jr", "Sr", "Co"} {
		start := dot - len(suffix)
		if start <= 0 || !nvidiaTTSASCIIWhitespace(text[start-1]) || text[start:dot] != suffix {
			continue
		}
		return !nvidiaTTSTailStartsSentence(tail)
	}
	for _, suffix := range []string{"adj", "adv", "agric", "appt", "approx", "assoc", "assn", "avg", "bldg", "chem", "co", "comm", "comp", "cong", "corp", "del", "dept", "dist", "div", "dr", "eng", "engr", "est", "etc", "fig", "ga", "govt", "hr", "inc", "ind", "inst", "intl", "jr", "ltd", "mach", "mfg", "min", "misc", "mktg", "mo", "mtg", "natl", "org", "pp", "prof", "rd", "ref", "rev", "sec", "serv", "sr", "sta", "tech", "tel", "trans", "univ", "util", "vol", "vs"} {
		start := dot - len(suffix)
		if start <= 0 || !nvidiaTTSASCIIWhitespace(text[start-1]) || text[start:dot] != suffix {
			continue
		}
		return !nvidiaTTSTailStartsSentence(tail)
	}
	for _, suffix := range []string{"Agric", "Ala", "Ariz", "Ark", "Assoc", "Assn", "Asst", "Atty", "Ave", "Bldg", "Blvd", "Bros", "Calif", "Ch", "Chem", "Cmdr", "Colo", "Comm", "Comp", "Cong", "Conn", "Del", "Dept", "Dir", "Dist", "Div", "Eng", "Engr", "Esq", "Fig", "Fla", "Fr", "Ga", "Govt", "Hon", "Hosp", "Hr", "Ill", "Ind", "Inst", "Intl", "Kans", "Ky", "Lab", "Mach", "Mass", "Med", "Mfg", "Mich", "Mgr", "Minn", "Miss", "Messrs", "Mktg", "Mmes", "Mont", "Msgr", "Mt", "Mtg", "Natl", "Neb", "Nev", "No", "Okla", "Ore", "Org", "Penn", "Rd", "Ref", "Sec", "Serv", "Sta", "Supt", "Tech", "Tel", "Tenn", "Tex", "Trans", "Univ", "Util", "Va", "Vol", "Vt", "Wash", "Wis", "Wyo"} {
		start := dot - len(suffix)
		if start <= 0 || !nvidiaTTSASCIIWhitespace(text[start-1]) || text[start:dot] != suffix {
			continue
		}
		return !nvidiaTTSTailStartsSentence(tail)
	}
	return false
}

func nvidiaTTSASCIIWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

func nvidiaTTSTailStartsSentence(tail string) bool {
	tail = strings.TrimLeft(tail, "\"',:;)]}”")
	if tail == "" || !nvidiaTTSASCIIWhitespace(tail[0]) {
		return false
	}
	trimmed := strings.TrimLeft(tail, nvidiaTTSWhitespaceCutset)
	trimmed = strings.TrimLeft(trimmed, "\"'‘“([{")
	trimmed = strings.TrimLeft(trimmed, "-— ")
	trimmed = strings.TrimLeft(trimmed, "#*• ")
	trimmed = strings.TrimLeft(trimmed, `/\@&% `)
	if trimmed == "" {
		return false
	}
	for _, starter := range []string{"Mr", "Mrs", "Ms", "Dr", "Prof", "Capt", "Cpt", "Lt", "Wherever"} {
		if strings.HasPrefix(trimmed, starter) {
			return true
		}
	}
	for _, starter := range []string{"I'd", "I've", "I'm", "It's", "That's", "There's", "They're", "We've", "You're", "We're", "Can't", "Let's", "We'll", "We’ll", "You'll", "You’ll", "I'll", "I’ll", "Don't", "Don’t"} {
		if strings.HasPrefix(trimmed, starter) {
			return true
		}
	}
	for _, starter := range []string{"I", "You", "Can", "Do", "Is", "Are", "No", "Not", "If", "As", "For", "On", "In", "At", "To", "Why", "When", "Where", "Also", "Then", "Let", "He", "She", "It", "They", "Their", "Our", "We", "But", "However", "That", "This", "Next", "Please", "Should", "Now", "Today", "After", "Before", "Because", "Since", "While", "Once", "Maybe", "Yes", "Sure", "Alright", "Absolutely", "Got it", "Okay", "OK", "Right", "Great", "Perfect", "Excellent", "Fine", "Sounds good", "Sounds great", "Sounds fine", "Sounds perfect", "Sounds excellent", "Sounds right", "Sounds okay", "Sounds alright", "First", "Second", "Finally", "Take", "Go", "And", "Or", "Yet", "Still", "Instead", "Meanwhile", "Later", "Soon", "There", "Here", "These", "Those", "Another", "Any", "Some", "All", "Each", "Every", "Most", "Many", "Much", "Several", "Both", "Neither", "One", "Two", "Three", "Last", "Previous", "New", "Only", "Other", "More", "Will", "Have", "Had", "Did", "Does", "A", "The", "My", "Your", "His", "Her", "Its", "What", "Who", "Which", "How", "About", "Over", "Under", "Through", "From", "By", "With", "Without", "During", "Until", "Though", "Although", "Whenever", "Whatever", "Whether"} {
		if strings.HasPrefix(trimmed, starter) && len(trimmed) > len(starter) {
			switch trimmed[len(starter)] {
			case ' ', '\t', '\n', '\r', ',', ':', ';':
				return true
			}
		}
	}
	return false
}
