package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
)

type GetDtmfResult struct {
	UserInput string
}

type GetDtmfOptions struct {
	NumDigits           int
	AskForConfirmation  bool
	DtmfInputTimeout    time.Duration
	DtmfInputTimeoutSet bool
	DtmfStopEvent       beta.DtmfEvent
	ExtraInstructions   string
	ChatContext         *llm.ChatContext
}

type GetDtmfTask struct {
	agent.AgentTask[*GetDtmfResult]
	NumDigits          int
	AskForConfirmation bool
	DtmfInputTimeout   time.Duration
	DtmfStopEvent      beta.DtmfEvent
	currDtmfInputs     []beta.DtmfEvent
	dtmfReplyRunning   bool
	mu                 sync.Mutex
	timer              *time.Timer
	dtmfStopCh         chan struct{}
	eventUnsubscribes  []func()
	exited             bool
	userState          agent.UserState
	agentState         agent.AgentState
}

func ValidateDtmfNumDigits(numDigits int) error {
	if numDigits <= 0 {
		return fmt.Errorf("num_digits must be greater than 0")
	}
	return nil
}

func NewGetDtmfTask(numDigits int, askConfirmation bool) (*GetDtmfTask, error) {
	return NewGetDtmfTaskWithOptions(GetDtmfOptions{
		NumDigits:          numDigits,
		AskForConfirmation: askConfirmation,
	})
}

func NewGetDtmfTaskWithOptions(opts GetDtmfOptions) (*GetDtmfTask, error) {
	if err := ValidateDtmfNumDigits(opts.NumDigits); err != nil {
		return nil, err
	}
	inputTimeout := opts.DtmfInputTimeout
	if inputTimeout == 0 && !opts.DtmfInputTimeoutSet {
		inputTimeout = 4 * time.Second
	}
	stopEvent := opts.DtmfStopEvent
	if stopEvent == "" {
		stopEvent = beta.DtmfEventPound
	}
	if _, err := beta.DtmfEventToCode(stopEvent); err != nil {
		return nil, fmt.Errorf("invalid DTMF stop event: %w", err)
	}

	instructions := "You are a single step in a broader system, responsible solely for gathering digits input from the user. " +
		"You will either receive a sequence of digits through dtmf events tagged by <dtmf_inputs>, or " +
		"user will directly say the digits to you. You should be able to handle both cases. "

	if opts.AskForConfirmation {
		instructions += "Once user has confirmed the digits (by verbally spoken or entered manually), call `confirm_inputs` with the inputs."
	} else {
		instructions += "If user provides the digits through voice and it is valid, call `record_inputs` with the inputs."
	}
	if extra := strings.TrimSpace(opts.ExtraInstructions); extra != "" {
		instructions += "\n" + extra
	}

	t := &GetDtmfTask{
		AgentTask:          *agent.NewAgentTask[*GetDtmfResult](instructions),
		NumDigits:          opts.NumDigits,
		AskForConfirmation: opts.AskForConfirmation,
		DtmfInputTimeout:   inputTimeout,
		DtmfStopEvent:      stopEvent,
		currDtmfInputs:     make([]beta.DtmfEvent, 0),
		userState:          agent.UserStateListening,
		agentState:         agent.AgentStateInitializing,
	}
	if opts.ChatContext != nil {
		t.ChatCtx = opts.ChatContext.Copy()
	}

	if opts.AskForConfirmation {
		t.Agent.Tools = []llm.Tool{&confirmInputsTool{task: t}}
	} else {
		t.Agent.Tools = []llm.Tool{&recordInputsTool{task: t}}
	}

	return t, nil
}

func (t *GetDtmfTask) OnEnter() {
	activity := t.Agent.GetActivity()
	if activity == nil || activity.Session == nil {
		return
	}

	stopCh := make(chan struct{})
	t.mu.Lock()
	t.dtmfStopCh = stopCh
	t.exited = false
	t.userState = activity.Session.UserState()
	t.agentState = activity.Session.AgentState()
	t.mu.Unlock()

	dtmfEvents, unsubscribeDTMF := activity.Session.SubscribeSipDTMFEvents()
	drainSipDTMFEvents(dtmfEvents)
	userStateEvents, unsubscribeUserState := activity.Session.SubscribeUserStateChangedEvents()
	agentStateEvents, unsubscribeAgentState := activity.Session.SubscribeAgentStateChangedEvents()
	t.mu.Lock()
	t.eventUnsubscribes = []func(){unsubscribeDTMF, unsubscribeUserState, unsubscribeAgentState}
	t.mu.Unlock()
	go func() {
		for {
			select {
			case <-stopCh:
				return
			case ev := <-dtmfEvents:
				t.onSipDTMFReceived(ev.Digit)
			case ev := <-userStateEvents:
				t.onUserStateChanged(ev.NewState)
			case ev := <-agentStateEvents:
				t.onAgentStateChanged(ev.NewState)
			}
		}
	}()

	_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
		ToolChoice: "none",
	})
}

func drainSipDTMFEvents(events <-chan agent.SipDTMFEvent) {
	for {
		select {
		case <-events:
		default:
			return
		}
	}
}

func (t *GetDtmfTask) OnExit() {
	t.mu.Lock()
	unsubscribes := t.eventUnsubscribes
	t.eventUnsubscribes = nil
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	if t.dtmfStopCh != nil {
		close(t.dtmfStopCh)
		t.dtmfStopCh = nil
	}
	t.exited = true
	t.currDtmfInputs = t.currDtmfInputs[:0]
	t.mu.Unlock()
	for _, unsubscribe := range unsubscribes {
		if unsubscribe != nil {
			unsubscribe()
		}
	}
}

func (t *GetDtmfTask) onSipDTMFReceived(digit string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.exited {
		return
	}
	if t.dtmfReplyRunning {
		return
	}

	if digit == string(t.DtmfStopEvent) {
		t.cancelDtmfReplyTimerLocked()
		go t.generateDtmfReply()
		return
	}

	event := beta.DtmfEvent(digit)
	if _, err := beta.DtmfEventToCode(event); err != nil {
		logger.Logger.Warnw("ignoring invalid DTMF input", err, "digit", digit)
		return
	}

	t.currDtmfInputs = append(t.currDtmfInputs, event)
	logger.Logger.Infow("DTMF inputs", "inputs", beta.FormatDtmf(t.currDtmfInputs))

	t.updateDtmfReplyTimerLocked()
}

func (t *GetDtmfTask) onUserStateChanged(state agent.UserState) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.exited {
		return
	}
	if t.dtmfReplyRunning {
		return
	}
	t.userState = state
	t.updateDtmfReplyTimerLocked()
}

func (t *GetDtmfTask) onAgentStateChanged(state agent.AgentState) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.exited {
		return
	}
	if t.dtmfReplyRunning {
		return
	}
	t.agentState = state
	t.updateDtmfReplyTimerLocked()
}

func (t *GetDtmfTask) updateDtmfReplyTimerLocked() {
	if t.dtmfReplyRunning || len(t.currDtmfInputs) == 0 {
		return
	}

	if t.userState == agent.UserStateSpeaking ||
		t.agentState == agent.AgentStateSpeaking ||
		t.agentState == agent.AgentStateThinking {
		t.cancelDtmfReplyTimerLocked()
		return
	}

	t.scheduleDtmfReplyLocked()
}

func (t *GetDtmfTask) scheduleDtmfReplyLocked() {
	t.cancelDtmfReplyTimerLocked()
	t.timer = time.AfterFunc(t.DtmfInputTimeout, t.generateDtmfReply)
}

func (t *GetDtmfTask) cancelDtmfReplyTimerLocked() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

func (t *GetDtmfTask) generateDtmfReply() {
	t.mu.Lock()
	if t.exited {
		t.mu.Unlock()
		return
	}
	t.dtmfReplyRunning = true
	inputs := make([]beta.DtmfEvent, len(t.currDtmfInputs))
	copy(inputs, t.currDtmfInputs)
	t.currDtmfInputs = make([]beta.DtmfEvent, 0)
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.dtmfReplyRunning = false
		t.mu.Unlock()
	}()

	dtmfStr := beta.FormatDtmf(inputs)
	logger.Logger.Debugw("Generating DTMF reply", "currentInputs", dtmfStr)

	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			if speech := session.CurrentSpeech(); speech != nil {
				_ = speech.Interrupt(false)
			}
		}
	}

	if len(inputs) != t.NumDigits {
		t.finishWithError(dtmfInputLengthError(t.NumDigits, len(inputs)))
		return
	}

	if !t.AskForConfirmation {
		t.finishWithResult(&GetDtmfResult{UserInput: dtmfStr})
		return
	}

	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), buildDtmfConfirmationInstructions(dtmfStr))
		}
	}
}

func buildDtmfConfirmationInstructions(dtmfStr string) string {
	return "User has entered the following valid digits on the telephone keypad:\n" +
		fmt.Sprintf("<dtmf_inputs>%s</dtmf_inputs>\n", dtmfStr) +
		"Please ask the user to confirm the keypad entry without reading the digits back. " +
		"Once you are sure, call `confirm_inputs` with the inputs."
}

func (t *GetDtmfTask) finishWithResult(result *GetDtmfResult) {
	if err := t.Complete(result); err == nil {
		t.OnExit()
	}
}

func (t *GetDtmfTask) finishWithError(err error) {
	if failErr := t.Fail(err); failErr == nil {
		t.OnExit()
	}
}

type confirmInputsTool struct {
	task *GetDtmfTask
}

func (t *confirmInputsTool) ID() string   { return "confirm_inputs" }
func (t *confirmInputsTool) Name() string { return "confirm_inputs" }
func (t *confirmInputsTool) Description() string {
	return "Finalize the collected digit inputs after explicit user confirmation.\n\n" +
		"Use this ONLY after the user has already confirmed the keypad entry is correct.\n\n" +
		"Do not use this tool to capture the initial digits."
}
func (t *confirmInputsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"inputs": map[string]any{
				"type":  "array",
				"items": dtmfInputItemSchema(),
			},
		},
		"required": []string{"inputs"},
	}
}

func (t *confirmInputsTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Inputs []string `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	dtmfEvents, err := parseDtmfInputs(params.Inputs)
	if err != nil {
		return "", err
	}
	dtmfEvents = t.task.trimTrailingStopEvent(dtmfEvents)
	if !t.task.finishIfDtmfInputLengthMismatch(dtmfEvents) {
		return "", nil
	}

	t.task.finishWithResult(&GetDtmfResult{UserInput: beta.FormatDtmf(dtmfEvents)})
	return "", nil
}

type recordInputsTool struct {
	task *GetDtmfTask
}

func (t *recordInputsTool) ID() string   { return "record_inputs" }
func (t *recordInputsTool) Name() string { return "record_inputs" }
func (t *recordInputsTool) Description() string {
	return "Record the collected digit inputs without additional confirmation.\n\n" +
		"Call this tool as soon as a valid sequence of digits has been provided by the user (via DTMF or spoken)."
}
func (t *recordInputsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"inputs": map[string]any{
				"type":  "array",
				"items": dtmfInputItemSchema(),
			},
		},
		"required": []string{"inputs"},
	}
}

func dtmfInputItemSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "*", "#", "A", "B", "C", "D"},
	}
}

func (t *recordInputsTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Inputs []string `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	dtmfEvents, err := parseDtmfInputs(params.Inputs)
	if err != nil {
		return "", err
	}
	dtmfEvents = t.task.trimTrailingStopEvent(dtmfEvents)
	if !t.task.finishIfDtmfInputLengthMismatch(dtmfEvents) {
		return "", nil
	}

	t.task.finishWithResult(&GetDtmfResult{UserInput: beta.FormatDtmf(dtmfEvents)})
	return "", nil
}

func (t *GetDtmfTask) trimTrailingStopEvent(events []beta.DtmfEvent) []beta.DtmfEvent {
	if len(events) == t.NumDigits+1 && events[len(events)-1] == t.DtmfStopEvent {
		return events[:len(events)-1]
	}
	return events
}

func (t *GetDtmfTask) finishIfDtmfInputLengthMismatch(events []beta.DtmfEvent) bool {
	if len(events) == t.NumDigits {
		return true
	}
	t.finishWithError(dtmfInputLengthError(t.NumDigits, len(events)))
	return false
}

func dtmfInputLengthError(want int, got int) error {
	return llm.NewToolError(fmt.Sprintf("Digits input not fully received. Expect %d digits, got %d", want, got))
}

func parseDtmfInputs(inputs []string) ([]beta.DtmfEvent, error) {
	dtmfEvents := make([]beta.DtmfEvent, 0, len(inputs))
	pendingRepeat := 1
	pendingRepeatWord := ""
	for i := 0; i < len(inputs); i++ {
		v := inputs[i]
		if normalizeDtmfToken(v) == "number" && i+1 < len(inputs) && normalizeDtmfToken(inputs[i+1]) == "key" {
			v = "number sign"
			i++
		} else if normalizeDtmfToken(v) == "number" && i+1 < len(inputs) && isDtmfSymbolSuffix(inputs[i+1]) {
			v = "number sign"
			i++
		} else if normalizeDtmfToken(v) == "hash" && i+1 < len(inputs) && isDtmfSymbolSuffix(inputs[i+1]) {
			v = "hash"
			i++
		} else if normalizeDtmfToken(v) == "pound" && i+1 < len(inputs) && isDtmfSymbolSuffix(inputs[i+1]) {
			v = "pound"
			i++
		} else if isDtmfKeyAlias(v) && i+1 < len(inputs) && isDtmfSymbolSuffix(inputs[i+1]) {
			i++
		}
		if i+1 < len(inputs) && normalizeDtmfToken(inputs[i+1]) == "key" && isDtmfKeyAlias(v) {
			i++
		}
		if phraseEvents, consumed, ok := consumeSplitDtmfInputPhrase(inputs, i); ok {
			for range pendingRepeat {
				dtmfEvents = append(dtmfEvents, phraseEvents...)
			}
			i += consumed - 1
			pendingRepeat = 1
			pendingRepeatWord = ""
			continue
		}
		if repeat, ok := dtmfRepeatWord(v); ok {
			pendingRepeat = repeat
			pendingRepeatWord = v
			continue
		}
		repeat, input := normalizeDtmfRepeat(v)
		repeat *= pendingRepeat
		pendingRepeat = 1
		pendingRepeatWord = ""
		if phraseEvents, ok := normalizeDtmfIndividualInputPhrase(input); ok {
			for range repeat {
				dtmfEvents = append(dtmfEvents, phraseEvents...)
			}
			continue
		}
		if phraseEvents, ok := normalizeDtmfInputPhrase(input); ok {
			for range repeat {
				dtmfEvents = append(dtmfEvents, phraseEvents...)
			}
			continue
		}
		event := normalizeDtmfInput(input)
		if _, err := beta.DtmfEventToCode(event); err != nil {
			return nil, err
		}
		for range repeat {
			dtmfEvents = append(dtmfEvents, event)
		}
	}
	if pendingRepeat != 1 {
		return nil, fmt.Errorf("invalid DTMF event: %s", pendingRepeatWord)
	}
	return dtmfEvents, nil
}

func consumeSplitDtmfInputPhrase(inputs []string, start int) ([]beta.DtmfEvent, int, bool) {
	max := start + 5
	if max > len(inputs) {
		max = len(inputs)
	}
	for end := max; end > start+1; end-- {
		phrase := strings.Join(inputs[start:end], " ")
		events, ok := normalizeDtmfInputPhrase(phrase)
		if ok {
			return events, end - start, true
		}
	}
	return nil, 0, false
}

func isDtmfSymbolSuffix(input string) bool {
	switch normalizeDtmfToken(input) {
	case "button", "mark", "sign", "symbol":
		return true
	default:
		return false
	}
}

func isDtmfDigitInput(input string) bool {
	event := normalizeDtmfInput(input)
	code, err := beta.DtmfEventToCode(event)
	return err == nil && code >= 0 && code <= 9
}

func normalizeDtmfInputPhrase(input string) ([]beta.DtmfEvent, bool) {
	tokens := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(input)), func(r rune) bool {
		return r == ' ' || r == '.' || r == ',' || r == '!' || r == '?' || r == ';' || r == ':'
	})
	tokens = filterDtmfPhraseTokens(tokens)
	tens := map[string]string{
		"twenty":  "2",
		"thirty":  "3",
		"forty":   "4",
		"fifty":   "5",
		"sixty":   "6",
		"seventy": "7",
		"eighty":  "8",
		"ninety":  "9",
	}
	teens := map[string][]beta.DtmfEvent{
		"ten":       {"1", "0"},
		"eleven":    {"1", "1"},
		"twelve":    {"1", "2"},
		"thirteen":  {"1", "3"},
		"fourteen":  {"1", "4"},
		"fifteen":   {"1", "5"},
		"sixteen":   {"1", "6"},
		"seventeen": {"1", "7"},
		"eighteen":  {"1", "8"},
		"nineteen":  {"1", "9"},
	}
	if len(tokens) < 1 || len(tokens) > 4 {
		return nil, false
	}
	if len(tokens) == 1 {
		if events, ok := teens[tokens[0]]; ok {
			return events, true
		}
		event := normalizeDtmfInput(tokens[0])
		if _, err := beta.DtmfEventToCode(event); err == nil {
			return []beta.DtmfEvent{event}, true
		}
		return nil, false
	}
	if len(tokens) == 2 {
		first := normalizeDtmfInput(tokens[0])
		if _, err := beta.DtmfEventToCode(first); err == nil {
			if events, ok := teens[tokens[1]]; ok {
				return append([]beta.DtmfEvent{first}, events...), true
			}
		}
	}
	if len(tokens) == 3 {
		first := normalizeDtmfInput(tokens[0])
		if _, err := beta.DtmfEventToCode(first); err == nil {
			if tokens[1] == "hundred" {
				last := normalizeDtmfInput(tokens[2])
				if _, err := beta.DtmfEventToCode(last); err == nil {
					return []beta.DtmfEvent{first, beta.DtmfEvent("0"), last}, true
				}
			}
			if tensDigit, ok := tens[tokens[1]]; ok {
				last := normalizeDtmfInput(tokens[2])
				if _, err := beta.DtmfEventToCode(last); err == nil {
					return []beta.DtmfEvent{first, beta.DtmfEvent(tensDigit), last}, true
				}
			}
		}
	}
	if len(tokens) == 4 {
		first := normalizeDtmfInput(tokens[0])
		if _, err := beta.DtmfEventToCode(first); err == nil {
			if tokens[1] == "hundred" {
				if tokens[2] == "oh" || tokens[2] == "o" || tokens[2] == "owe" || tokens[2] == "zero" || tokens[2] == "aught" || tokens[2] == "ought" || tokens[2] == "naught" || tokens[2] == "nought" {
					last := normalizeDtmfInput(tokens[3])
					if _, err := beta.DtmfEventToCode(last); err == nil {
						return []beta.DtmfEvent{first, beta.DtmfEvent("0"), last}, true
					}
				}
				if tensDigit, ok := tens[tokens[2]]; ok {
					last := normalizeDtmfInput(tokens[3])
					if _, err := beta.DtmfEventToCode(last); err == nil {
						return []beta.DtmfEvent{first, beta.DtmfEvent(tensDigit), last}, true
					}
				}
			}
			if tensDigit, ok := tens[tokens[1]]; ok && (tokens[2] == "oh" || tokens[2] == "o" || tokens[2] == "owe" || tokens[2] == "zero" || tokens[2] == "aught" || tokens[2] == "ought" || tokens[2] == "naught" || tokens[2] == "nought") {
				last := normalizeDtmfInput(tokens[3])
				if _, err := beta.DtmfEventToCode(last); err == nil {
					return []beta.DtmfEvent{first, beta.DtmfEvent(tensDigit), beta.DtmfEvent("0"), beta.DtmfEvent("0"), last}, true
				}
			}
		}
	}
	tensDigit, ok := tens[tokens[0]]
	if !ok {
		return nil, false
	}
	if len(tokens) == 2 {
		last := normalizeDtmfInput(tokens[1])
		if _, err := beta.DtmfEventToCode(last); err != nil {
			return nil, false
		}
		return []beta.DtmfEvent{beta.DtmfEvent(tensDigit), last}, true
	}
	if tokens[1] != "oh" && tokens[1] != "o" && tokens[1] != "owe" && tokens[1] != "zero" && tokens[1] != "aught" && tokens[1] != "ought" && tokens[1] != "naught" && tokens[1] != "nought" {
		return nil, false
	}
	last := normalizeDtmfInput(tokens[2])
	if _, err := beta.DtmfEventToCode(last); err != nil {
		return nil, false
	}
	return []beta.DtmfEvent{beta.DtmfEvent(tensDigit), beta.DtmfEvent("0"), beta.DtmfEvent("0"), last}, true
}

func normalizeDtmfIndividualInputPhrase(input string) ([]beta.DtmfEvent, bool) {
	tokens := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(input)), func(r rune) bool {
		return r == ' ' || r == '.' || r == ',' || r == '!' || r == '?' || r == ';' || r == ':'
	})
	return normalizeDtmfIndividualTokenPhrase(filterDtmfPhraseTokens(tokens))
}

func filterDtmfPhraseTokens(tokens []string) []string {
	tokens = trimTrailingDtmfSignoffTokens(tokens)
	filtered := tokens[:0]
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if i+2 < len(tokens) && token == "one" && tokens[i+1] == "time" && tokens[i+2] == "password" {
			i += 2
			continue
		}
		if token == "number" && i+1 < len(tokens) && isDtmfDigitInput(tokens[i+1]) {
			continue
		}
		if token == "number" && i+1 < len(tokens) && isDtmfFiller(tokens[i+1]) && normalizeDtmfToken(tokens[i+1]) != "key" {
			continue
		}
		if token == "to" && i+1 < len(tokens) && isDtmfFiller(tokens[i+1]) {
			continue
		}
		if base, ok := strings.CutSuffix(normalizeDtmfToken(token), "'s"); ok && isDtmfFiller(base) {
			continue
		}
		if isDtmfFiller(token) {
			continue
		}
		filtered = append(filtered, token)
	}
	return filtered
}

func trimTrailingDtmfSignoffTokens(tokens []string) []string {
	if trimmed, ok := trimTrailingDtmfForSignoffTokens(tokens); ok {
		return trimmed
	}

	trailing := map[string]struct{}{
		"complete": {}, "completed": {}, "done": {}, "entered": {}, "finished": {}, "ok": {}, "okay": {}, "please": {}, "sent": {}, "submitted": {}, "thanks": {}, "thank": {}, "you": {},
	}
	if len(tokens) >= 2 &&
		normalizeDtmfToken(tokens[len(tokens)-1]) == "done" &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "all" {
		tokens = tokens[:len(tokens)-2]
	}
	for len(tokens) > 0 {
		last := normalizeDtmfToken(tokens[len(tokens)-1])
		if last == "you" && len(tokens) >= 2 && normalizeDtmfToken(tokens[len(tokens)-2]) == "for" {
			break
		}
		if _, ok := trailing[last]; !ok {
			break
		}
		tokens = tokens[:len(tokens)-1]
	}
	if trimmed, ok := trimTrailingDtmfForSignoffTokens(tokens); ok {
		return trimmed
	}
	if len(tokens) >= 2 &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "for" &&
		isDtmfSignoffObjectToken(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-2]
	}
	if len(tokens) >= 2 &&
		normalizeDtmfToken(tokens[len(tokens)-1]) == "it" &&
		(normalizeDtmfToken(tokens[len(tokens)-2]) == "that's" || normalizeDtmfToken(tokens[len(tokens)-2]) == "thats") {
		return tokens[:len(tokens)-2]
	}
	if len(tokens) >= 2 &&
		normalizeDtmfToken(tokens[len(tokens)-1]) == "all" &&
		(normalizeDtmfToken(tokens[len(tokens)-2]) == "that's" || normalizeDtmfToken(tokens[len(tokens)-2]) == "thats") {
		return tokens[:len(tokens)-2]
	}
	if len(tokens) >= 3 &&
		(normalizeDtmfToken(tokens[len(tokens)-1]) == "it" || normalizeDtmfToken(tokens[len(tokens)-1]) == "all") &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "is" &&
		normalizeDtmfToken(tokens[len(tokens)-3]) == "that" {
		return tokens[:len(tokens)-3]
	}
	if len(tokens) >= 4 &&
		(normalizeDtmfToken(tokens[len(tokens)-1]) == "it" || normalizeDtmfToken(tokens[len(tokens)-1]) == "all") &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "be" &&
		normalizeDtmfToken(tokens[len(tokens)-3]) == "will" &&
		normalizeDtmfToken(tokens[len(tokens)-4]) == "that" {
		return tokens[:len(tokens)-4]
	}
	if len(tokens) >= 4 &&
		(normalizeDtmfToken(tokens[len(tokens)-1]) == "it" || normalizeDtmfToken(tokens[len(tokens)-1]) == "all") &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "be" &&
		normalizeDtmfToken(tokens[len(tokens)-3]) == "ll" &&
		normalizeDtmfToken(tokens[len(tokens)-4]) == "that" {
		return tokens[:len(tokens)-4]
	}
	if len(tokens) >= 3 &&
		(normalizeDtmfToken(tokens[len(tokens)-1]) == "it" || normalizeDtmfToken(tokens[len(tokens)-1]) == "all") &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "be" &&
		isDtmfThatWillContractionToken(tokens[len(tokens)-3]) {
		return tokens[:len(tokens)-3]
	}
	return tokens
}

func trimTrailingDtmfForSignoffTokens(tokens []string) ([]string, bool) {
	if len(tokens) >= 7 &&
		normalizeDtmfToken(tokens[len(tokens)-7]) == "that" &&
		normalizeDtmfToken(tokens[len(tokens)-6]) == "will" &&
		normalizeDtmfToken(tokens[len(tokens)-5]) == "be" &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-4]) &&
		normalizeDtmfToken(tokens[len(tokens)-3]) == "for" &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "the" &&
		normalizeDtmfToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-7], true
	}
	if len(tokens) >= 6 &&
		isDtmfThatWillContractionToken(tokens[len(tokens)-6]) &&
		normalizeDtmfToken(tokens[len(tokens)-5]) == "be" &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-4]) &&
		normalizeDtmfToken(tokens[len(tokens)-3]) == "for" &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "the" &&
		normalizeDtmfToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-6], true
	}
	if len(tokens) >= 7 &&
		normalizeDtmfToken(tokens[len(tokens)-7]) == "that" &&
		normalizeDtmfToken(tokens[len(tokens)-6]) == "ll" &&
		normalizeDtmfToken(tokens[len(tokens)-5]) == "be" &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-4]) &&
		normalizeDtmfToken(tokens[len(tokens)-3]) == "for" &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "the" &&
		normalizeDtmfToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-7], true
	}
	if len(tokens) >= 6 &&
		normalizeDtmfToken(tokens[len(tokens)-6]) == "that" &&
		normalizeDtmfToken(tokens[len(tokens)-5]) == "will" &&
		normalizeDtmfToken(tokens[len(tokens)-4]) == "be" &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-3]) &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "for" &&
		isDtmfSignoffObjectToken(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-6], true
	}
	if len(tokens) >= 5 &&
		isDtmfThatWillContractionToken(tokens[len(tokens)-5]) &&
		normalizeDtmfToken(tokens[len(tokens)-4]) == "be" &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-3]) &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "for" &&
		isDtmfSignoffObjectToken(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-5], true
	}
	if len(tokens) >= 6 &&
		normalizeDtmfToken(tokens[len(tokens)-6]) == "that" &&
		normalizeDtmfToken(tokens[len(tokens)-5]) == "ll" &&
		normalizeDtmfToken(tokens[len(tokens)-4]) == "be" &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-3]) &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "for" &&
		isDtmfSignoffObjectToken(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-6], true
	}
	if len(tokens) >= 6 &&
		normalizeDtmfToken(tokens[len(tokens)-6]) == "that" &&
		normalizeDtmfToken(tokens[len(tokens)-5]) == "is" &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-4]) &&
		normalizeDtmfToken(tokens[len(tokens)-3]) == "for" &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "the" &&
		normalizeDtmfToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-6], true
	}
	if len(tokens) >= 5 &&
		isDtmfThatContractionToken(tokens[len(tokens)-5]) &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-4]) &&
		normalizeDtmfToken(tokens[len(tokens)-3]) == "for" &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "the" &&
		normalizeDtmfToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-5], true
	}
	if len(tokens) >= 5 &&
		normalizeDtmfToken(tokens[len(tokens)-5]) == "that" &&
		normalizeDtmfToken(tokens[len(tokens)-4]) == "is" &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-3]) &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "for" &&
		isDtmfSignoffObjectToken(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-5], true
	}
	if len(tokens) >= 4 &&
		isDtmfThatContractionToken(tokens[len(tokens)-4]) &&
		isDtmfDoneSignoffToken(tokens[len(tokens)-3]) &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "for" &&
		isDtmfSignoffObjectToken(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-4], true
	}
	if len(tokens) >= 3 &&
		normalizeDtmfToken(tokens[len(tokens)-3]) == "for" &&
		normalizeDtmfToken(tokens[len(tokens)-2]) == "the" &&
		normalizeDtmfToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-3], true
	}
	return tokens, false
}

func isDtmfThatContractionToken(token string) bool {
	switch normalizeDtmfToken(token) {
	case "that's", "thats":
		return true
	default:
		return false
	}
}

func isDtmfThatWillContractionToken(token string) bool {
	switch normalizeDtmfToken(token) {
	case "that'll", "thatll":
		return true
	default:
		return false
	}
}

func isDtmfDoneSignoffToken(token string) bool {
	switch normalizeDtmfToken(token) {
	case "it", "all":
		return true
	default:
		return false
	}
}

func isDtmfSignoffObjectToken(token string) bool {
	switch normalizeDtmfToken(token) {
	case "day", "me", "now", "today", "you":
		return true
	default:
		return false
	}
}

func normalizeDtmfIndividualTokenPhrase(tokens []string) ([]beta.DtmfEvent, bool) {
	if len(tokens) < 2 {
		return nil, false
	}
	events := make([]beta.DtmfEvent, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if normalizeDtmfToken(token) == "number" && i+1 < len(tokens) && isDtmfSymbolSuffix(tokens[i+1]) {
			token = "number sign"
			i++
		} else if isDtmfKeyAlias(token) && i+1 < len(tokens) && (isDtmfSymbolSuffix(tokens[i+1]) || normalizeDtmfToken(tokens[i+1]) == "key") {
			i++
		}
		event := normalizeDtmfInput(token)
		if _, err := beta.DtmfEventToCode(event); err != nil {
			return nil, false
		}
		events = append(events, event)
	}
	return events, true
}

func isDtmfKeyAlias(input string) bool {
	switch normalizeDtmfKeyAliasToken(input) {
	case "star", "asterisk", "pound", "hash", "hashtag", "octothorpe", "number sign":
		return true
	default:
		return false
	}
}

func dtmfRepeatWord(input string) (int, bool) {
	switch normalizeDtmfToken(input) {
	case "single":
		return 1, true
	case "double":
		return 2, true
	case "triple":
		return 3, true
	case "quadruple":
		return 4, true
	default:
		return 1, false
	}
}

func normalizeDtmfRepeat(input string) (int, string) {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(input)), func(r rune) bool {
		return r == ' ' || r == '.' || r == ',' || r == '!' || r == '?' || r == ';' || r == ':'
	})
	if len(parts) == 3 {
		if repeat, ok := dtmfRepeatWord(parts[0]); ok && isDtmfFiller(parts[1]) {
			return repeat, parts[2]
		}
	}
	if len(parts) == 2 {
		if repeat, ok := dtmfRepeatWord(parts[0]); ok {
			return repeat, parts[1]
		}
	}
	trimmed := normalizeDtmfToken(input)
	for _, repeatWord := range []struct {
		word   string
		repeat int
	}{
		{"single", 1},
		{"double", 2},
		{"triple", 3},
		{"quadruple", 4},
	} {
		if rest, ok := strings.CutPrefix(trimmed, repeatWord.word+" "); ok {
			return repeatWord.repeat, rest
		}
	}
	return 1, input
}

func isDtmfFiller(input string) bool {
	switch normalizeDtmfToken(input) {
	case "access", "account", "actually", "ah", "and", "authorization", "be", "case", "choose", "claim", "code", "confirmation", "customer", "enter", "er", "extension", "hm", "hmm", "i", "i'd", "id", "invoice", "is", "key", "like", "member", "menu", "my", "option", "order", "otp", "passcode", "pin", "policy", "press", "reference", "reservation", "routing", "select", "sorry", "subscriber", "the", "ticket", "uh", "um", "verification", "want", "will", "would":
		return true
	default:
		return false
	}
}

func normalizeDtmfInput(input string) beta.DtmfEvent {
	token := normalizeDtmfKeyAliasToken(input)
	if letter, ok := strings.CutPrefix(token, "letter "); ok {
		token = strings.TrimSpace(letter)
	}
	switch token {
	case "zero", "oh", "o", "owe", "aught", "ought", "naught", "nought":
		return beta.DtmfEvent("0")
	case "one", "won":
		return beta.DtmfEvent("1")
	case "two", "to", "too":
		return beta.DtmfEvent("2")
	case "three", "tree", "free":
		return beta.DtmfEvent("3")
	case "four", "for", "fore":
		return beta.DtmfEvent("4")
	case "five":
		return beta.DtmfEvent("5")
	case "six", "sex":
		return beta.DtmfEvent("6")
	case "seven":
		return beta.DtmfEvent("7")
	case "eight", "ate":
		return beta.DtmfEvent("8")
	case "nine", "niner":
		return beta.DtmfEvent("9")
	case "star", "asterisk":
		return beta.DtmfEvent("*")
	case "pound", "hash", "hashtag", "octothorpe", "number sign":
		return beta.DtmfEvent("#")
	case "a", "b", "c", "d":
		return beta.DtmfEvent(strings.ToUpper(token))
	case "ay", "aye":
		return beta.DtmfEvent("A")
	case "bee":
		return beta.DtmfEvent("B")
	case "see", "sea":
		return beta.DtmfEvent("C")
	case "dee":
		return beta.DtmfEvent("D")
	default:
		return beta.DtmfEvent(input)
	}
}

func normalizeDtmfKeyAliasToken(input string) string {
	token := normalizeDtmfToken(input)
	for _, suffix := range []string{" sign", " symbol", " key", " mark"} {
		base, ok := strings.CutSuffix(token, suffix)
		if !ok || base == "" {
			continue
		}
		if base == "number" {
			return "number sign"
		}
		return base
	}
	return token
}

func normalizeDtmfToken(input string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(input)), ".,!?;:")
}
