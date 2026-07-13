package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

var phoneRegex = regexp.MustCompile(`^\+?[1-9]\d{6,14}$`)

type GetPhoneNumberResult struct {
	PhoneNumber string
}

type GetPhoneNumberOptions struct {
	AgentOptions
	ExtraInstructions      string
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ChatContext            *llm.ChatContext
	Tools                  []llm.Tool
}

type GetPhoneNumberTask struct {
	agent.AgentTask[*GetPhoneNumberResult]
	ExtraInstructions      string
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	currentPhoneNumber     string
}

const phoneNumberConfirmationInstruction = "Call `confirm_phone_number` after the user confirmed the phone number is correct."

const PhoneNumberInstructions = "You are only a single step in a broader system, responsible solely for capturing a phone number.\n" +
	"Handle input as noisy voice transcription. Expect that users will say phone numbers aloud with formats like:\n" +
	"- '555 123 4567'\n" +
	"- 'five five five, one two three, four five six seven'\n" +
	"- '+1 555 123 4567'\n" +
	"- 'area code 555, 123 4567'\n" +
	"- '555-123-4567'\n" +
	"Normalize common spoken patterns silently:\n" +
	"- Convert spoken digits to their numeric form: 'five' → 5, 'zero' → 0, 'oh' → 0.\n" +
	"- Remove filler words, pauses, and hesitations.\n" +
	"- Strip dashes, spaces, parentheses, and dots from the number.\n" +
	"- Recognize 'plus' at the start as the international prefix `+`.\n" +
	"- Recognize 'area code' as a prefix for the area code digits.\n" +
	"Don't mention corrections. Treat inputs as possibly imperfect but fix them silently.\n" +
	"Call `update_phone_number` at the first opportunity whenever you form a new hypothesis about the phone number. (before asking any questions or providing any answers.)\n" +
	"Don't invent phone numbers, stick strictly to what the user said.\n" +
	phoneNumberConfirmationInstruction + "\n" +
	"If the number is unclear or invalid, or it takes too much back-and-forth, prompt for it in parts: first the area code, then the remaining digits.\n" +
	"Ask the user to confirm the updated phone number without repeating it back.\n" +
	"Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.\n" +
	"Avoid verbosity by not sharing example phone numbers or formats unless prompted to do so. Do not deviate from the goal of collecting the user's phone number.\n" +
	"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called."

const phoneNumberTextInstructions = "You are only a single step in a broader system, responsible solely for capturing a phone number.\n" +
	"Handle input as typed text. Expect users to type their phone number directly.\n" +
	"Strip dashes, spaces, parentheses, and dots from the number.\n" +
	"If the number looks almost correct but has minor formatting issues, clean it up silently.\n" +
	"Call `update_phone_number` at the first opportunity whenever you form a new hypothesis about the phone number. (before asking any questions or providing any answers.)\n" +
	"Don't invent phone numbers, stick strictly to what the user said.\n" +
	phoneNumberConfirmationInstruction + "\n" +
	"If the number is unclear or invalid, or it takes too much back-and-forth, prompt for it in parts: first the area code, then the remaining digits.\n" +
	"Ask the user to confirm the updated phone number without repeating it back.\n" +
	"Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.\n" +
	"Avoid verbosity by not sharing example phone numbers or formats unless prompted to do so. Do not deviate from the goal of collecting the user's phone number.\n" +
	"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called."

func NewGetPhoneNumberTask(opts GetPhoneNumberOptions) *GetPhoneNumberTask {
	requireConfirmation := true
	if opts.RequireConfirmationSet {
		requireConfirmation = opts.RequireConfirmation
	}
	instructions := phoneNumberInstructions(requireConfirmation, opts.ExtraInstructions)
	textInstructions := phoneNumberTextVariantInstructions(opts.RequireConfirmationSet && opts.RequireConfirmation, opts.ExtraInstructions)
	t := &GetPhoneNumberTask{
		AgentTask:              *agent.NewAgentTask[*GetPhoneNumberResult](instructions),
		ExtraInstructions:      opts.ExtraInstructions,
		RequireConfirmation:    requireConfirmation,
		RequireConfirmationSet: opts.RequireConfirmationSet,
		RequireExplicitAsk:     opts.RequireExplicitAsk,
	}
	t.InstructionVariants = llm.NewInstructions(instructions, textInstructions)
	applyAgentOptions(&t.Agent, opts.AgentOptions)
	if opts.ChatContext != nil {
		t.ChatCtx = opts.ChatContext.Copy()
	}

	t.Agent.Tools = append(append([]llm.Tool{}, opts.Tools...),
		&updatePhoneNumberTool{task: t},
		&declinePhoneNumberCaptureTool{task: t},
	)

	return t
}

func phoneNumberInstructions(requireConfirmation bool, extraInstructions string) string {
	instructions := PhoneNumberInstructions
	if !requireConfirmation {
		instructions = strings.Replace(instructions, "\n"+phoneNumberConfirmationInstruction, "", 1)
	}
	if strings.TrimSpace(extraInstructions) != "" {
		instructions += "\n" + strings.TrimSpace(extraInstructions)
	}
	return instructions
}

func phoneNumberTextVariantInstructions(requireConfirmation bool, extraInstructions string) string {
	instructions := phoneNumberTextInstructions
	if !requireConfirmation {
		instructions = strings.Replace(instructions, "\n"+phoneNumberConfirmationInstruction, "", 1)
	}
	if strings.TrimSpace(extraInstructions) != "" {
		instructions += "\n" + strings.TrimSpace(extraInstructions)
	}
	return instructions
}

func phoneNumberConfirmationRequired(ctx context.Context, requireConfirmation bool, set bool) bool {
	if set {
		return requireConfirmation
	}
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.SpeechHandle == nil {
		return true
	}
	return runCtx.SpeechHandle.InputDetails.Modality == "audio"
}

func (t *GetPhoneNumberTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: phoneNumberOnEnterPrompt(),
			})
		}
	}
}

func phoneNumberOnEnterPrompt() string {
	return "Ask the user to provide their phone number."
}

type updatePhoneNumberTool struct {
	task *GetPhoneNumberTask
}

func (t *updatePhoneNumberTool) ID() string   { return "update_phone_number" }
func (t *updatePhoneNumberTool) Name() string { return "update_phone_number" }
func (t *updatePhoneNumberTool) ToolFlags() llm.ToolFlag {
	if t.task.RequireExplicitAsk {
		return llm.ToolFlagIgnoreOnEnter
	}
	return llm.ToolFlagNone
}
func (t *updatePhoneNumberTool) Description() string {
	return "Update the phone number provided by the user."
}
func (t *updatePhoneNumberTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"phone_number": map[string]any{"type": "string", "description": "The phone number provided by the user, digits only with optional leading +"},
		},
		"required": []string{"phone_number"},
	}
}

func (t *updatePhoneNumberTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		PhoneNumber string `json:"phone_number"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	cleaned := normalizePhoneNumber(params.PhoneNumber)
	if !phoneRegex.MatchString(cleaned) {
		return "", llm.NewToolError(fmt.Sprintf("Invalid phone number provided: %s", params.PhoneNumber))
	}

	t.task.currentPhoneNumber = cleaned
	if !phoneNumberConfirmationRequired(ctx, t.task.RequireConfirmation, t.task.RequireConfirmationSet) {
		_ = t.task.Complete(&GetPhoneNumberResult{PhoneNumber: cleaned})
		return "", nil
	}

	t.task.setConfirmPhoneNumberTool(cleaned)
	return "The phone number has been updated.\nAsk the user to confirm the updated phone number without repeating it back.\nPrompt the user for confirmation, do not call `confirm_phone_number` directly", nil
}

func (t *GetPhoneNumberTask) setConfirmPhoneNumberTool(phoneNumber string) {
	tools := make([]llm.Tool, 0, len(t.Agent.Tools)+1)
	for _, tool := range t.Agent.Tools {
		if tool.ID() == "confirm_phone_number" {
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, &confirmPhoneNumberTool{task: t, phoneNumber: phoneNumber})
	t.Agent.Tools = tools
}

type confirmPhoneNumberTool struct {
	task        *GetPhoneNumberTask
	phoneNumber string
}

func (t *confirmPhoneNumberTool) ID() string   { return "confirm_phone_number" }
func (t *confirmPhoneNumberTool) Name() string { return "confirm_phone_number" }
func (t *confirmPhoneNumberTool) Description() string {
	return "Call after the user confirms the phone number is correct."
}
func (t *confirmPhoneNumberTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *confirmPhoneNumberTool) Execute(ctx context.Context, args string) (string, error) {
	if t.phoneNumber != t.task.currentPhoneNumber {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: phoneNumberStaleConfirmationPrompt(),
			})
		}
		return "", nil
	}
	_ = t.task.Complete(&GetPhoneNumberResult{PhoneNumber: t.phoneNumber})
	return "", nil
}

func phoneNumberStaleConfirmationPrompt() string {
	return "The phone number has changed since confirmation was requested, ask the user to confirm the updated number."
}

func phoneNumberFailureTarget(ctx context.Context, fallback *GetPhoneNumberTask) *GetPhoneNumberTask {
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.Session == nil {
		return fallback
	}
	currentAgent, err := runCtx.Session.CurrentAgent()
	if err != nil {
		return fallback
	}
	if task, ok := currentAgent.(*GetPhoneNumberTask); ok {
		return task
	}
	return fallback
}

type declinePhoneNumberCaptureTool struct {
	task *GetPhoneNumberTask
}

func (t *declinePhoneNumberCaptureTool) ID() string { return "decline_phone_number_capture" }
func (t *declinePhoneNumberCaptureTool) Name() string {
	return "decline_phone_number_capture"
}
func (t *declinePhoneNumberCaptureTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
func (t *declinePhoneNumberCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide a phone number."
}
func (t *declinePhoneNumberCaptureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the user declined to provide the phone number"},
		},
		"required": []string{"reason"},
	}
}

func (t *declinePhoneNumberCaptureTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	_ = phoneNumberFailureTarget(ctx, t.task).Fail(llm.NewToolError(fmt.Sprintf("couldn't get the phone number: %s", params.Reason)))
	return "", nil
}

func normalizePhoneNumber(phoneNumber string) string {
	cleaned := strings.TrimSpace(phoneNumber)
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' || r == '(' || r == ')' || r == '.' {
			return -1
		}
		return r
	}, cleaned)
	if phoneRegex.MatchString(stripped) {
		return stripped
	}
	return normalizeSpokenPhoneNumber(cleaned)
}

func normalizeSpokenPhoneNumber(phoneNumber string) string {
	phoneNumber = trimTrailingSpokenPhoneDigitSignoff(phoneNumber)

	digits := map[string]string{
		"zero":   "0",
		"oh":     "0",
		"o":      "0",
		"owe":    "0",
		"aught":  "0",
		"ought":  "0",
		"naught": "0",
		"nought": "0",
		"one":    "1",
		"won":    "1",
		"two":    "2",
		"to":     "2",
		"too":    "2",
		"three":  "3",
		"tree":   "3",
		"free":   "3",
		"four":   "4",
		"for":    "4",
		"fore":   "4",
		"five":   "5",
		"six":    "6",
		"sex":    "6",
		"seven":  "7",
		"eight":  "8",
		"ate":    "8",
		"nine":   "9",
		"niner":  "9",
	}
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
	teens := map[string]string{
		"ten":       "10",
		"eleven":    "11",
		"twelve":    "12",
		"thirteen":  "13",
		"fourteen":  "14",
		"fifteen":   "15",
		"sixteen":   "16",
		"seventeen": "17",
		"eighteen":  "18",
		"nineteen":  "19",
	}
	fillers := map[string]struct{}{
		"actually": {},
		"ah":       {},
		"and":      {},
		"er":       {},
		"hm":       {},
		"hmm":      {},
		"like":     {},
		"sorry":    {},
		"uh":       {},
		"um":       {},
	}
	var b strings.Builder
	var token strings.Builder
	lastSpokenDigit := false
	repeat := 1
	pendingGroup := ""
	pendingGroupRepeat := 1
	pendingSingleHundred := false
	pendingSingleHundredFull := false
	pendingSingleHundredPrefix := ""
	pendingSingleHundredRepeat := 1
	pendingHundredZeroTail := false
	pendingCountryCodePrefix := false
	lastWrittenDigit := ""
	lastWrittenDigitRepeat := 1
	stop := false
	writeDigit := func(digit string) {
		lastWrittenDigit = digit
		lastWrittenDigitRepeat = repeat
		for range repeat {
			b.WriteString(digit)
		}
		repeat = 1
	}
	flushPendingGroup := func() {
		if pendingGroup == "" {
			return
		}
		group := pendingGroup
		if len(group) == 1 {
			group += "0"
		}
		for range pendingGroupRepeat {
			b.WriteString(group)
		}
		pendingGroup = ""
		pendingGroupRepeat = 1
	}
	flushPendingSingleHundred := func(asHundred bool) {
		if !pendingSingleHundred {
			return
		}
		if asHundred {
			if pendingSingleHundredPrefix != "" {
				for range pendingSingleHundredRepeat {
					b.WriteString(pendingSingleHundredPrefix + "00")
				}
			} else {
				b.WriteString("00")
			}
		}
		pendingSingleHundred = false
		pendingSingleHundredFull = false
		pendingSingleHundredPrefix = ""
		pendingSingleHundredRepeat = 1
	}
	flush := func() {
		if token.Len() == 0 {
			return
		}
		word := token.String()
		if word == "plus" && b.Len() == 0 {
			pendingCountryCodePrefix = false
			flushPendingGroup()
			b.WriteRune('+')
			lastSpokenDigit = false
		} else if word == "country" && b.Len() == 0 {
			pendingCountryCodePrefix = true
			lastSpokenDigit = false
		} else if word == "code" && pendingCountryCodePrefix && b.Len() == 0 {
			pendingCountryCodePrefix = false
			flushPendingGroup()
			b.WriteRune('+')
			lastSpokenDigit = false
		} else if word == "double" {
			pendingCountryCodePrefix = false
			flushPendingGroup()
			repeat = 2
			lastSpokenDigit = false
		} else if word == "triple" {
			pendingCountryCodePrefix = false
			flushPendingGroup()
			repeat = 3
			lastSpokenDigit = false
		} else if word == "quadruple" {
			pendingCountryCodePrefix = false
			flushPendingGroup()
			repeat = 4
			lastSpokenDigit = false
		} else if digit, ok := digits[word]; ok {
			pendingCountryCodePrefix = false
			if pendingHundredZeroTail {
				if pendingSingleHundredPrefix != "" {
					for range pendingSingleHundredRepeat {
						b.WriteString(pendingSingleHundredPrefix + "0" + digit)
					}
				} else {
					b.WriteString("0" + digit)
				}
				pendingHundredZeroTail = false
				pendingSingleHundred = false
				pendingSingleHundredFull = false
				pendingSingleHundredPrefix = ""
				pendingSingleHundredRepeat = 1
				lastSpokenDigit = true
				token.Reset()
				return
			}
			if pendingGroup != "" {
				if digit == "0" && len(pendingGroup) == 1 {
					pendingGroup += "00"
				} else {
					pendingGroup += digit
					flushPendingGroup()
				}
				lastSpokenDigit = true
				token.Reset()
				return
			}
			if pendingSingleHundred {
				if digit == "0" {
					pendingHundredZeroTail = true
					lastSpokenDigit = true
					token.Reset()
					return
				}
				if pendingSingleHundredPrefix != "" {
					group := pendingSingleHundredPrefix + "0"
					if pendingSingleHundredFull {
						group = pendingSingleHundredPrefix + "00"
					}
					for range pendingSingleHundredRepeat {
						b.WriteString(group + digit)
					}
					pendingSingleHundred = false
					pendingSingleHundredFull = false
					pendingSingleHundredPrefix = ""
					pendingSingleHundredRepeat = 1
					lastSpokenDigit = true
					token.Reset()
					return
				} else {
					if pendingSingleHundredFull {
						b.WriteString("00")
					} else {
						b.WriteString("0")
					}
				}
				pendingSingleHundred = false
				pendingSingleHundredFull = false
			}
			flushPendingSingleHundred(true)
			writeDigit(digit)
			lastSpokenDigit = true
		} else if tensDigit, ok := tens[word]; ok {
			pendingCountryCodePrefix = false
			if pendingSingleHundredPrefix != "" {
				pendingGroup = pendingSingleHundredPrefix + tensDigit
				pendingGroupRepeat = pendingSingleHundredRepeat
				pendingSingleHundred = false
				pendingSingleHundredFull = false
				pendingSingleHundredPrefix = ""
				pendingSingleHundredRepeat = 1
				repeat = 1
				lastSpokenDigit = true
				token.Reset()
				return
			}
			flushPendingSingleHundred(false)
			flushPendingGroup()
			pendingGroup = tensDigit
			pendingGroupRepeat = repeat
			repeat = 1
			lastSpokenDigit = true
		} else if teenDigits, ok := teens[word]; ok {
			pendingCountryCodePrefix = false
			flushPendingSingleHundred(false)
			flushPendingGroup()
			writeDigit(teenDigits)
			lastSpokenDigit = true
		} else if word == "hundred" && lastSpokenDigit {
			pendingCountryCodePrefix = false
			flushPendingGroup()
			hundredFull := strings.HasSuffix(b.String(), "8")
			if lastWrittenDigitRepeat > 1 && lastWrittenDigit != "" {
				suffix := strings.Repeat(lastWrittenDigit, lastWrittenDigitRepeat)
				current := b.String()
				if strings.HasSuffix(current, suffix) {
					b.Reset()
					b.WriteString(current[:len(current)-len(suffix)])
					pendingSingleHundredPrefix = lastWrittenDigit
					pendingSingleHundredRepeat = lastWrittenDigitRepeat
				}
			}
			pendingSingleHundred = true
			pendingSingleHundredFull = hundredFull
			lastSpokenDigit = false
		} else if _, ok := fillers[word]; ok {
		} else if isPhoneExtensionLabel(word) {
			pendingCountryCodePrefix = false
			flushPendingSingleHundred(false)
			flushPendingGroup()
			lastSpokenDigit = false
			repeat = 1
			stop = true
		} else {
			pendingCountryCodePrefix = false
			flushPendingSingleHundred(false)
			flushPendingGroup()
			lastSpokenDigit = false
			repeat = 1
		}
		token.Reset()
	}
	for _, r := range strings.ToLower(phoneNumber) {
		switch {
		case r >= '0' && r <= '9':
			flush()
			if stop {
				break
			}
			flushPendingSingleHundred(true)
			writeDigit(string(r))
			lastSpokenDigit = true
		case r == '+' && b.Len() == 0:
			flush()
			if stop {
				break
			}
			b.WriteRune(r)
			lastSpokenDigit = false
		case unicode.IsLetter(r):
			token.WriteRune(r)
		default:
			flush()
			if stop {
				break
			}
		}
		if stop {
			break
		}
	}
	flush()
	if !stop {
		flushPendingGroup()
	}
	return b.String()
}

func trimTrailingSpokenPhoneDigitSignoff(value string) string {
	tokens := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if trim := trimTrailingSpokenPhoneDigitSignoffTokens(tokens); trim >= 0 {
		return strings.Join(tokens[:trim], " ")
	}
	tokens = trimTrailingSpokenPhoneDigitSignoffFillers(tokens)
	if trim := trimTrailingSpokenPhoneDigitSignoffTokens(tokens); trim >= 0 {
		return strings.Join(tokens[:trim], " ")
	}
	return value
}

func trimTrailingSpokenPhoneDigitSignoffFillers(tokens []string) []string {
	for len(tokens) > 0 {
		if tokens[len(tokens)-1] == "you" && len(tokens) >= 2 && tokens[len(tokens)-2] == "for" {
			break
		}
		if !isPhoneDigitSignoffFiller(tokens[len(tokens)-1]) {
			break
		}
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

func isPhoneDigitSignoffFiller(token string) bool {
	switch token {
	case "thanks", "thank", "you", "please", "ok", "okay":
		return true
	default:
		return false
	}
}

func trimTrailingSpokenPhoneDigitSignoffTokens(tokens []string) int {
	if len(tokens) >= 2 {
		suffix := tokens[len(tokens)-2:]
		if suffix[0] == "for" && isPhoneDigitSignoffObject(suffix[1]) {
			return len(tokens) - 2
		}
	}
	if len(tokens) >= 3 {
		suffix := tokens[len(tokens)-3:]
		if suffix[0] == "for" && suffix[1] == "the" && suffix[2] == "day" {
			return len(tokens) - 3
		}
	}
	if len(tokens) >= 7 {
		suffix := tokens[len(tokens)-7:]
		if suffix[0] == "that" && suffix[1] == "will" && suffix[2] == "be" && isPhoneDigitDoneToken(suffix[3]) && suffix[4] == "for" && suffix[5] == "the" && suffix[6] == "day" {
			return len(tokens) - 7
		}
	}
	if len(tokens) >= 6 {
		suffix := tokens[len(tokens)-6:]
		if (suffix[0] == "thatll" || suffix[0] == "that'll") && suffix[1] == "be" && isPhoneDigitDoneToken(suffix[2]) && suffix[3] == "for" && suffix[4] == "the" && suffix[5] == "day" {
			return len(tokens) - 6
		}
	}
	if len(tokens) >= 7 {
		suffix := tokens[len(tokens)-7:]
		if suffix[0] == "that" && suffix[1] == "ll" && suffix[2] == "be" && isPhoneDigitDoneToken(suffix[3]) && suffix[4] == "for" && suffix[5] == "the" && suffix[6] == "day" {
			return len(tokens) - 7
		}
	}
	if len(tokens) >= 6 {
		suffix := tokens[len(tokens)-6:]
		if suffix[0] == "that" && suffix[1] == "is" && isPhoneDigitDoneToken(suffix[2]) && suffix[3] == "for" && suffix[4] == "the" && suffix[5] == "day" {
			return len(tokens) - 6
		}
	}
	if len(tokens) >= 6 {
		suffix := tokens[len(tokens)-6:]
		if suffix[0] == "that" && suffix[1] == "s" && isPhoneDigitDoneToken(suffix[2]) && suffix[3] == "for" && suffix[4] == "the" && suffix[5] == "day" {
			return len(tokens) - 6
		}
	}
	if len(tokens) >= 5 {
		suffix := tokens[len(tokens)-5:]
		if suffix[0] == "thats" && isPhoneDigitDoneToken(suffix[1]) && suffix[2] == "for" && suffix[3] == "the" && suffix[4] == "day" {
			return len(tokens) - 5
		}
	}
	if len(tokens) >= 5 {
		suffix := tokens[len(tokens)-5:]
		if suffix[0] == "that" && suffix[1] == "is" && isPhoneDigitDoneToken(suffix[2]) && suffix[3] == "for" && isPhoneDigitSignoffObject(suffix[4]) {
			return len(tokens) - 5
		}
	}
	if len(tokens) >= 5 {
		suffix := tokens[len(tokens)-5:]
		if suffix[0] == "that" && suffix[1] == "s" && isPhoneDigitDoneToken(suffix[2]) && suffix[3] == "for" && isPhoneDigitSignoffObject(suffix[4]) {
			return len(tokens) - 5
		}
	}
	if len(tokens) >= 4 {
		suffix := tokens[len(tokens)-4:]
		if suffix[0] == "thats" && isPhoneDigitDoneToken(suffix[1]) && suffix[2] == "for" && isPhoneDigitSignoffObject(suffix[3]) {
			return len(tokens) - 4
		}
	}
	return -1
}

func isPhoneDigitDoneToken(token string) bool {
	switch token {
	case "all", "it":
		return true
	default:
		return false
	}
}

func isPhoneDigitSignoffObject(token string) bool {
	switch token {
	case "day", "me", "now", "today", "you":
		return true
	default:
		return false
	}
}

func isPhoneExtensionLabel(word string) bool {
	switch word {
	case "extension", "ext", "extn", "x":
		return true
	default:
		return false
	}
}
