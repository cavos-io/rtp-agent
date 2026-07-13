package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cavos-io/rtp-agent/core/agent"
	beta "github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
)

var emailRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._%+\-]*@(?:[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?\.)+[A-Za-z]{2,}$`)

type GetEmailResult struct {
	Email string
}

type GetEmailOptions struct {
	AgentOptions
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ExtraInstructions      string
	Instructions           *beta.InstructionParts
	ChatContext            *llm.ChatContext
	Tools                  []llm.Tool
}

type GetEmailTask struct {
	agent.AgentTask[*GetEmailResult]
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	currentEmail           string
	emailConfirmed         bool
}

const emailConfirmationInstruction = "Call `confirm_email_address` after the user confirmed the email address is correct."

const emailPersona = "You are only a single step in a broader system, responsible solely for capturing an email address."

const emailInstructionsBeforeConfirmation = emailPersona + `
Handle input as noisy voice transcription. Expect that users will say emails aloud with formats like:
- 'john dot doe at gmail dot com'
- 'susan underscore smith at yahoo dot co dot uk'
- 'dave dash b at protonmail dot com'
- 'jane at example' (partial—prompt for the domain)
- 'theo t h e o at livekit dot io' (name followed by spelling)
Normalize common spoken patterns silently:
- Convert words like 'dot', 'underscore', 'dash', 'plus' into symbols: ` + "`.`, `_`, `-`, `+`." + `
- Convert 'at' to ` + "`@`" + `.
- Recognize patterns where users speak their name or a word, followed by spelling: e.g., 'john j o h n'.
- Filter out filler words or hesitations.
- Assume some spelling if contextually obvious (e.g. 'mike b two two' → mikeb22).
Don't mention corrections. Treat inputs as possibly imperfect but fix them silently.
Call ` + "`update_email_address`" + ` at the first opportunity whenever you form a new hypothesis about the email. (before asking any questions or providing any answers.)
Don't invent new email addresses, stick strictly to what the user said.
`

const emailTextInstructionsBeforeConfirmation = emailPersona + `
Handle input as typed text. Expect users to type their email address directly in standard format.
If the address looks almost correct but has minor typos (e.g. missing '@' or domain), prompt for clarification.
Call ` + "`update_email_address`" + ` at the first opportunity whenever you form a new hypothesis about the email. (before asking any questions or providing any answers.)
Don't invent new email addresses, stick strictly to what the user said.
`

const emailInstructionsAfterConfirmation = `If the email is unclear or invalid, or it takes too much back-and-forth, prompt for it in parts: first the part before the '@', then the domain—only if needed.
Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.
Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.`

const EmailInstructions = emailInstructionsBeforeConfirmation + emailConfirmationInstruction + "\n" + emailInstructionsAfterConfirmation

const emailInstructionsWithoutConfirmation = emailInstructionsBeforeConfirmation + emailInstructionsAfterConfirmation

const emailTextInstructions = emailTextInstructionsBeforeConfirmation + emailConfirmationInstruction + "\n" + emailInstructionsAfterConfirmation

const emailTextInstructionsWithoutConfirmation = emailTextInstructionsBeforeConfirmation + emailInstructionsAfterConfirmation

func NewGetEmailTask(opts GetEmailOptions) *GetEmailTask {
	requireConfirmation := true
	if opts.RequireConfirmationSet {
		requireConfirmation = opts.RequireConfirmation
	}
	instructions := EmailInstructions
	if !requireConfirmation {
		instructions = emailInstructionsWithoutConfirmation
	}
	instructions = applyInstructionParts(instructions, emailPersona, opts.Instructions)
	extraInstructions := opts.ExtraInstructions
	if opts.Instructions != nil {
		extraInstructions = ""
	}
	instructions = appendEmailExtraInstructions(instructions, extraInstructions)
	textInstructions := emailTextVariantInstructions(opts.RequireConfirmationSet && opts.RequireConfirmation, opts.Instructions)
	textInstructions = appendEmailExtraInstructions(textInstructions, extraInstructions)
	t := &GetEmailTask{
		AgentTask:              *agent.NewAgentTask[*GetEmailResult](instructions),
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
		&updateEmailTool{task: t},
		&declineEmailCaptureTool{task: t},
	)

	return t
}

func appendEmailExtraInstructions(instructions string, extraInstructions string) string {
	if extra := strings.TrimSpace(extraInstructions); extra != "" {
		return strings.TrimRight(instructions, "\n") + "\n" + extra
	}
	return instructions
}

func emailTextVariantInstructions(requireConfirmation bool, parts *beta.InstructionParts) string {
	instructions := emailTextInstructions
	if !requireConfirmation {
		instructions = emailTextInstructionsWithoutConfirmation
	}
	return applyInstructionParts(instructions, emailPersona, parts)
}

func emailConfirmationRequired(ctx context.Context, requireConfirmation bool, set bool) bool {
	if set {
		return requireConfirmation
	}
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.SpeechHandle == nil {
		return true
	}
	return runCtx.SpeechHandle.InputDetails.Modality == "audio"
}

func (t *GetEmailTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: emailOnEnterPrompt(),
			})
		}
	}
}

func emailOnEnterPrompt() string {
	return "Ask the user to provide an email address."
}

type updateEmailTool struct {
	task *GetEmailTask
}

func (t *updateEmailTool) ID() string   { return "update_email_address" }
func (t *updateEmailTool) Name() string { return "update_email_address" }
func (t *updateEmailTool) ToolFlags() llm.ToolFlag {
	if t.task.RequireExplicitAsk {
		return llm.ToolFlagIgnoreOnEnter
	}
	return llm.ToolFlagNone
}
func (t *updateEmailTool) Description() string {
	return "Update the email address provided by the user."
}
func (t *updateEmailTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"email": map[string]any{"type": "string", "description": "The email address provided by the user"},
		},
		"required": []string{"email"},
	}
}

func (t *updateEmailTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	email := normalizeEmailAddress(params.Email)
	if !emailRegex.MatchString(email) {
		return "", llm.NewToolError(fmt.Sprintf("Invalid email address provided: %s", email))
	}

	t.task.currentEmail = email

	if !emailConfirmationRequired(ctx, t.task.RequireConfirmation, t.task.RequireConfirmationSet) {
		_ = t.task.Complete(&GetEmailResult{Email: email})
		return "", nil
	}

	t.task.setConfirmEmailTool(email)
	return "The email has been updated.\nAsk the user to confirm the updated email address without repeating it back.\nPrompt the user for confirmation, do not call `confirm_email_address` directly", nil
}

func (t *GetEmailTask) setConfirmEmailTool(email string) {
	tools := make([]llm.Tool, 0, len(t.Agent.Tools)+1)
	for _, tool := range t.Agent.Tools {
		if tool.ID() == "confirm_email_address" {
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, &confirmEmailTool{task: t, email: email})
	t.Agent.Tools = tools
}

type confirmEmailTool struct {
	task  *GetEmailTask
	email string
}

func (t *confirmEmailTool) ID() string   { return "confirm_email_address" }
func (t *confirmEmailTool) Name() string { return "confirm_email_address" }
func (t *confirmEmailTool) Description() string {
	return "Call after the user confirms the email address is correct."
}
func (t *confirmEmailTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *confirmEmailTool) Execute(ctx context.Context, args string) (string, error) {
	if t.task.currentEmail == "" {
		return "", fmt.Errorf("error: no email address was provided, update_email_address must be called before")
	}
	if t.email != t.task.currentEmail {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: emailStaleConfirmationPrompt(),
			})
		}
		return "", nil
	}

	t.task.emailConfirmed = true
	_ = t.task.Complete(&GetEmailResult{Email: t.email})
	return "", nil
}

func emailStaleConfirmationPrompt() string {
	return "The email has changed since confirmation was requested, ask the user to confirm the updated email."
}

func normalizeEmailAddress(email string) string {
	cleaned := strings.TrimSpace(email)
	if emailRegex.MatchString(cleaned) {
		return cleaned
	}

	symbols := map[string]string{
		"at":         "@",
		"dot":        ".",
		"period":     ".",
		"point":      ".",
		"underscore": "_",
		"dash":       "-",
		"hyphen":     "-",
		"minus":      "-",
		"plus":       "+",
	}
	digits := map[string]string{
		"zero":   "0",
		"oh":     "0",
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

	tokens := normalizeSpokenEmailTokens(strings.Fields(strings.ToLower(cleaned)))
	tokens = trimTrailingSpokenEmailFiller(tokens)
	var b strings.Builder
	afterAt := false
	digitRepeat := 1
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch token {
		case "single":
			if i+1 < len(tokens) && isSpokenEmailNumericLead(tokens[i+1]) {
				digitRepeat = 1
				continue
			}
		case "double":
			digitRepeat = 2
			continue
		case "triple":
			digitRepeat = 3
			continue
		case "quadruple":
			digitRepeat = 4
			continue
		}
		if token == "full" && i+1 < len(tokens) && tokens[i+1] == "stop" {
			b.WriteString(".")
			digitRepeat = 1
			i++
			continue
		}
		if token == "under" && i+1 < len(tokens) && tokens[i+1] == "score" {
			b.WriteString("_")
			digitRepeat = 1
			i++
			if i+1 < len(tokens) && isSpokenEmailSymbolSuffix(tokens[i+1]) {
				i++
			}
			continue
		}
		if token == "hy" && i+1 < len(tokens) && tokens[i+1] == "phen" {
			b.WriteString("-")
			digitRepeat = 1
			i++
			if i+1 < len(tokens) && isSpokenEmailSymbolSuffix(tokens[i+1]) {
				i++
			}
			continue
		}
		if symbol, ok := symbols[token]; ok {
			b.WriteString(symbol)
			digitRepeat = 1
			if symbol == "@" {
				afterAt = true
			}
			if token == "at" && i+2 < len(tokens) && tokens[i+1] == "the" && tokens[i+2] == "rate" {
				i += 2
			} else if token == "at" && i+1 < len(tokens) && tokens[i+1] == "rate" {
				i++
			} else if token == "at" && i+1 < len(tokens) && isSpokenEmailSymbolSuffix(tokens[i+1]) {
				i++
			} else if token == "plus" && i+1 < len(tokens) && isSpokenEmailSymbolSuffix(tokens[i+1]) {
				i++
			} else if (token == "dot" || token == "period" || token == "point") && i+1 < len(tokens) && isSpokenEmailSymbolSuffix(tokens[i+1]) {
				i++
			} else if (token == "underscore" || token == "dash" || token == "hyphen" || token == "minus") && i+1 < len(tokens) && isSpokenEmailSymbolSuffix(tokens[i+1]) {
				i++
			}
			continue
		}
		if afterAt {
			if lastByteIs(&b, '.') {
				if suffix, j, ok := consumeSpokenEmailLetterSuffix(tokens, i); ok {
					b.WriteString(suffix)
					digitRepeat = 1
					i = j - 1
					continue
				}
			}
			if token == "gee" && i+1 < len(tokens) && isSpokenEmailMailToken(tokens[i+1]) {
				b.WriteString("gmail")
				digitRepeat = 1
				i++
				continue
			}
			if token == "yah" && i+1 < len(tokens) && isSpokenEmailHooToken(tokens[i+1]) {
				b.WriteString("yahoo")
				digitRepeat = 1
				i++
				continue
			}
			if token == "eye" && i+1 < len(tokens) && tokens[i+1] == "cloud" {
				b.WriteString("icloud")
				digitRepeat = 1
				i++
				continue
			}
			if (token == "ay" || token == "aye") && i+2 < len(tokens) && (tokens[i+1] == "oh" || tokens[i+1] == "o" || tokens[i+1] == "owe") && tokens[i+2] == "ell" {
				b.WriteString("aol")
				digitRepeat = 1
				i += 2
				continue
			}
			if (token == "ay" || token == "aye") && i+2 < len(tokens) && tokens[i+1] == "tee" && tokens[i+2] == "tee" {
				b.WriteString("att")
				digitRepeat = 1
				i += 2
				continue
			}
			if token == "em" && i+2 < len(tokens) && tokens[i+1] == "ess" && tokens[i+2] == "en" {
				b.WriteString("msn")
				digitRepeat = 1
				i += 2
				continue
			}
			if token == "ess" && i+3 < len(tokens) && tokens[i+1] == "bee" && tokens[i+2] == "see" && tokens[i+3] == "global" {
				b.WriteString("sbcglobal")
				digitRepeat = 1
				i += 3
				continue
			}
			if (token == "see" || token == "cee" || token == "sea") && i+2 < len(tokens) && (tokens[i+1] == "oh" || tokens[i+1] == "o" || tokens[i+1] == "owe") && tokens[i+2] == "ex" {
				b.WriteString("cox")
				digitRepeat = 1
				i += 2
				continue
			}
			if suffix, ok := fusedSpokenEmailDotSuffix(token); ok {
				b.WriteString(".")
				b.WriteString(suffix)
				digitRepeat = 1
				continue
			}
		}
		if letter, ok := spokenEmailLetterName(token); ok && i+1 < len(tokens) && isSpokenEmailNumericLead(tokens[i+1]) {
			b.WriteString(letter)
			digitRepeat = 1
			continue
		}
		if tensDigit, ok := spokenEmailTensDigit(token); ok && i+2 < len(tokens) && (tokens[i+1] == "oh" || tokens[i+1] == "o" || tokens[i+1] == "owe" || tokens[i+1] == "zero" || tokens[i+1] == "aught" || tokens[i+1] == "ought" || tokens[i+1] == "naught" || tokens[i+1] == "nought") {
			if digit, ok := digits[tokens[i+2]]; ok {
				b.WriteString(tensDigit)
				b.WriteString("00")
				b.WriteString(digit)
				digitRepeat = 1
				i += 2
				continue
			}
		}
		if digit, ok := digits[token]; ok && i+1 < len(tokens) && tokens[i+1] == "hundred" {
			group := digit
			writeGroup := func(value string) {
				for range digitRepeat {
					b.WriteString(value)
				}
				digitRepeat = 1
			}
			if i+2 < len(tokens) {
				if tensDigit, ok := spokenEmailTensDigit(tokens[i+2]); ok {
					group += tensDigit
					if i+4 < len(tokens) && (tokens[i+3] == "oh" || tokens[i+3] == "o" || tokens[i+3] == "owe" || tokens[i+3] == "zero" || tokens[i+3] == "aught" || tokens[i+3] == "ought" || tokens[i+3] == "naught" || tokens[i+3] == "nought") {
						if tail, ok := digits[tokens[i+4]]; ok {
							writeGroup(group + "00" + tail)
							i += 4
							continue
						}
					}
					if i+3 < len(tokens) {
						if tail, ok := digits[tokens[i+3]]; ok {
							writeGroup(group + tail)
							i += 3
							continue
						}
					}
					writeGroup(group + "0")
					i += 2
					continue
				}
				if tail, ok := digits[tokens[i+2]]; ok {
					if tail == "0" && i+3 < len(tokens) {
						if nextTail, ok := digits[tokens[i+3]]; ok {
							writeGroup(group + "0" + nextTail)
							i += 3
							continue
						}
					}
					writeGroup(group + "0" + tail)
					i += 2
					continue
				}
			}
			writeGroup(group + "00")
			i++
			continue
		}
		if digit, ok := digits[token]; ok {
			for range digitRepeat {
				b.WriteString(digit)
			}
			digitRepeat = 1
			continue
		}
		if len(token) > 1 {
			spelled, j, ok := consumeSpokenEmailSpelling(tokens, i+1)
			if ok && spelled == token {
				b.WriteString(spelled)
				digitRepeat = 1
				i = j - 1
				continue
			}
		}
		b.WriteString(token)
		digitRepeat = 1
	}
	return b.String()
}

func lastByteIs(b *strings.Builder, want byte) bool {
	if b.Len() == 0 {
		return false
	}
	value := b.String()
	return value[len(value)-1] == want
}

func consumeSpokenEmailLetterSuffix(tokens []string, start int) (string, int, bool) {
	var b strings.Builder
	i := start
	for ; i < len(tokens); i++ {
		token := tokens[i]
		if token == "dot" || token == "period" || token == "point" || token == "full" {
			break
		}
		if len(token) == 1 && token >= "a" && token <= "z" {
			b.WriteString(token)
			continue
		}
		if letter, ok := spokenEmailSuffixLetter(token); ok {
			b.WriteString(letter)
			continue
		}
		if b.Len() == 0 {
			if suffix, ok := spokenEmailSuffixHomophone(token); ok {
				return suffix, i + 1, true
			}
		}
		return "", start, false
	}
	if b.Len() < 2 {
		return "", start, false
	}
	return b.String(), i, true
}

func spokenEmailSuffixLetter(token string) (string, bool) {
	switch token {
	case "ee":
		return "e", true
	case "oh", "owe":
		return "o", true
	default:
		return spokenEmailLetterName(token)
	}
}

func spokenEmailSuffixHomophone(token string) (string, bool) {
	switch token {
	case "calm", "con":
		return "com", true
	default:
		return "", false
	}
}

func fusedSpokenEmailDotSuffix(token string) (string, bool) {
	switch token {
	case "dotcom":
		return "com", true
	case "dotnet":
		return "net", true
	case "dotorg":
		return "org", true
	case "dotio":
		return "io", true
	case "dotco":
		return "co", true
	case "dotuk":
		return "uk", true
	case "dotedu":
		return "edu", true
	case "dotgov":
		return "gov", true
	default:
		suffix, ok := strings.CutPrefix(token, "dot")
		if !ok || !isAlphabeticEmailSuffix(suffix) {
			return "", false
		}
		return suffix, true
	}
}

func isSpokenEmailMailToken(token string) bool {
	return token == "mail" || token == "male"
}

func isSpokenEmailHooToken(token string) bool {
	return token == "hoo" || token == "who"
}

func isAlphabeticEmailSuffix(suffix string) bool {
	if len(suffix) < 2 || len(suffix) > 63 {
		return false
	}
	for _, r := range suffix {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func isSpokenEmailSymbolSuffix(token string) bool {
	return token == "sign" || token == "symbol" || token == "key" || token == "mark"
}

func spokenEmailTensDigit(word string) (string, bool) {
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
	digit, ok := tens[word]
	return digit, ok
}

func spokenEmailLetterName(token string) (string, bool) {
	switch token {
	case "ay", "aye":
		return "a", true
	case "be", "bee":
		return "b", true
	case "cee", "sea", "see":
		return "c", true
	case "dee":
		return "d", true
	case "eff":
		return "f", true
	case "gee":
		return "g", true
	case "aitch", "haitch":
		return "h", true
	case "eye":
		return "i", true
	case "jay":
		return "j", true
	case "kay":
		return "k", true
	case "el", "ell":
		return "l", true
	case "em":
		return "m", true
	case "en":
		return "n", true
	case "oh", "owe":
		return "o", true
	case "pea", "pee":
		return "p", true
	case "cue", "queue":
		return "q", true
	case "are":
		return "r", true
	case "ess":
		return "s", true
	case "tea", "tee":
		return "t", true
	case "ewe", "you":
		return "u", true
	case "vee":
		return "v", true
	case "ex":
		return "x", true
	case "why":
		return "y", true
	case "zed", "zee":
		return "z", true
	default:
		return "", false
	}
}

func isSpokenEmailNumericLead(token string) bool {
	switch token {
	case "zero", "oh", "owe", "aught", "ought", "naught", "nought",
		"one", "won",
		"two", "to", "too",
		"three", "tree", "free",
		"four", "for", "fore",
		"five", "six", "seven",
		"eight", "ate",
		"nine", "niner",
		"double", "triple", "quadruple":
		return true
	default:
		_, ok := spokenEmailTensDigit(token)
		return ok
	}
}

func normalizeSpokenEmailTokens(tokens []string) []string {
	filler := map[string]struct{}{
		"um": {}, "uh": {}, "er": {}, "ah": {}, "hmm": {}, "like": {}, "actually": {}, "sorry": {}, "and": {},
	}
	preamble := map[string]struct{}{
		"my": {}, "the": {}, "email": {}, "address": {}, "is": {}, "it": {}, "its": {}, "it's": {}, "spell": {}, "spelled": {},
	}

	out := make([]string, 0, len(tokens))
	started := false
	seenPreamble := false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		token = cleanSpokenEmailToken(token)
		if token == "" {
			continue
		}
		if _, ok := filler[token]; ok {
			continue
		}
		if !started {
			if token == "no" {
				continue
			}
			if token == "space" {
				continue
			}
			if token == "all" {
				continue
			}
			if token == "lower" {
				continue
			}
			if token == "lowercase" {
				continue
			}
			if token == "case" {
				continue
			}
			if token == "caps" {
				continue
			}
			if token == "capital" {
				continue
			}
			if token == "upper" {
				continue
			}
			if token == "uppercase" {
				continue
			}
			if token == "e" && i+1 < len(tokens) && cleanSpokenEmailToken(tokens[i+1]) == "mail" {
				if i+2 < len(tokens) {
					next := cleanSpokenEmailToken(tokens[i+2])
					if next == "is" || next == "address" {
						i++
						seenPreamble = true
						continue
					}
				}
			}
			if token == "e-mail" {
				seenPreamble = true
				continue
			}
			if token == "e-mail's" {
				seenPreamble = true
				continue
			}
			if token == "s" && i > 0 && cleanSpokenEmailToken(tokens[i-1]) == "email" {
				continue
			}
			if seenPreamble && token == "will" && i+1 < len(tokens) && cleanSpokenEmailToken(tokens[i+1]) == "be" {
				i++
				continue
			}
			if base, ok := strings.CutSuffix(token, "'s"); ok {
				if _, ok := preamble[base]; ok {
					seenPreamble = true
					continue
				}
			}
			if _, ok := preamble[token]; ok {
				seenPreamble = true
				continue
			}
		}
		started = true
		out = append(out, token)
	}
	return out
}

func trimTrailingSpokenEmailFiller(tokens []string) []string {
	if len(tokens) == 0 || !hasSpokenEmailAddressShape(tokens) {
		return tokens
	}
	if len(tokens) >= 2 && tokens[len(tokens)-1] == "it" &&
		(tokens[len(tokens)-2] == "that's" || tokens[len(tokens)-2] == "thats") {
		tokens = tokens[:len(tokens)-2]
	}
	if len(tokens) >= 2 && tokens[len(tokens)-1] == "all" &&
		(tokens[len(tokens)-2] == "that's" || tokens[len(tokens)-2] == "thats") {
		tokens = tokens[:len(tokens)-2]
	}
	if len(tokens) >= 3 &&
		(tokens[len(tokens)-1] == "it" || tokens[len(tokens)-1] == "all") &&
		tokens[len(tokens)-2] == "is" &&
		tokens[len(tokens)-3] == "that" {
		tokens = tokens[:len(tokens)-3]
	}
	if len(tokens) >= 3 &&
		(tokens[len(tokens)-3] == "that'll" || tokens[len(tokens)-3] == "thatll") &&
		tokens[len(tokens)-2] == "be" &&
		(tokens[len(tokens)-1] == "it" || tokens[len(tokens)-1] == "all") {
		tokens = tokens[:len(tokens)-3]
	}
	if len(tokens) >= 4 &&
		tokens[len(tokens)-4] == "that" &&
		tokens[len(tokens)-3] == "will" &&
		tokens[len(tokens)-2] == "be" &&
		(tokens[len(tokens)-1] == "it" || tokens[len(tokens)-1] == "all") {
		tokens = tokens[:len(tokens)-4]
	}
	if len(tokens) >= 4 &&
		tokens[len(tokens)-4] == "that" &&
		tokens[len(tokens)-3] == "ll" &&
		tokens[len(tokens)-2] == "be" &&
		(tokens[len(tokens)-1] == "it" || tokens[len(tokens)-1] == "all") {
		tokens = tokens[:len(tokens)-4]
	}
	if trimmed := trimTrailingSpokenEmailSignoffParts(tokens); len(trimmed) != len(tokens) {
		return trimmed
	}
	trailing := map[string]struct{}{
		"all": {}, "done": {}, "ok": {}, "okay": {}, "please": {}, "thanks": {}, "thank": {}, "you": {},
	}
	for len(tokens) > 0 {
		last := tokens[len(tokens)-1]
		if last == "you" && len(tokens) >= 2 && tokens[len(tokens)-2] == "for" {
			break
		}
		if _, ok := trailing[last]; !ok {
			break
		}
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) >= 5 &&
		(tokens[len(tokens)-5] == "that's" || tokens[len(tokens)-5] == "thats") &&
		(tokens[len(tokens)-4] == "it" || tokens[len(tokens)-4] == "all") &&
		tokens[len(tokens)-3] == "for" &&
		tokens[len(tokens)-2] == "the" &&
		tokens[len(tokens)-1] == "day" {
		tokens = tokens[:len(tokens)-5]
	}
	if len(tokens) >= 6 &&
		tokens[len(tokens)-6] == "that" &&
		tokens[len(tokens)-5] == "is" &&
		(tokens[len(tokens)-4] == "it" || tokens[len(tokens)-4] == "all") &&
		tokens[len(tokens)-3] == "for" &&
		tokens[len(tokens)-2] == "the" &&
		tokens[len(tokens)-1] == "day" {
		tokens = tokens[:len(tokens)-6]
	}
	if len(tokens) >= 7 &&
		tokens[len(tokens)-7] == "that" &&
		tokens[len(tokens)-6] == "will" &&
		tokens[len(tokens)-5] == "be" &&
		(tokens[len(tokens)-4] == "it" || tokens[len(tokens)-4] == "all") &&
		tokens[len(tokens)-3] == "for" &&
		tokens[len(tokens)-2] == "the" &&
		tokens[len(tokens)-1] == "day" {
		tokens = tokens[:len(tokens)-7]
	}
	if len(tokens) >= 6 &&
		(tokens[len(tokens)-6] == "that'll" || tokens[len(tokens)-6] == "thatll") &&
		tokens[len(tokens)-5] == "be" &&
		(tokens[len(tokens)-4] == "it" || tokens[len(tokens)-4] == "all") &&
		tokens[len(tokens)-3] == "for" &&
		tokens[len(tokens)-2] == "the" &&
		tokens[len(tokens)-1] == "day" {
		tokens = tokens[:len(tokens)-6]
	}
	if len(tokens) >= 7 &&
		tokens[len(tokens)-7] == "that" &&
		tokens[len(tokens)-6] == "ll" &&
		tokens[len(tokens)-5] == "be" &&
		(tokens[len(tokens)-4] == "it" || tokens[len(tokens)-4] == "all") &&
		tokens[len(tokens)-3] == "for" &&
		tokens[len(tokens)-2] == "the" &&
		tokens[len(tokens)-1] == "day" {
		tokens = tokens[:len(tokens)-7]
	}
	if len(tokens) >= 5 &&
		(tokens[len(tokens)-5] == "that'll" || tokens[len(tokens)-5] == "thatll") &&
		tokens[len(tokens)-4] == "be" &&
		(tokens[len(tokens)-3] == "it" || tokens[len(tokens)-3] == "all") &&
		tokens[len(tokens)-2] == "for" &&
		isSpokenEmailSignoffObject(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-5]
	}
	if len(tokens) >= 6 &&
		tokens[len(tokens)-6] == "that" &&
		tokens[len(tokens)-5] == "ll" &&
		tokens[len(tokens)-4] == "be" &&
		(tokens[len(tokens)-3] == "it" || tokens[len(tokens)-3] == "all") &&
		tokens[len(tokens)-2] == "for" &&
		isSpokenEmailSignoffObject(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-6]
	}
	if len(tokens) >= 4 &&
		(tokens[len(tokens)-4] == "that's" || tokens[len(tokens)-4] == "thats") &&
		(tokens[len(tokens)-3] == "it" || tokens[len(tokens)-3] == "all") &&
		tokens[len(tokens)-2] == "for" &&
		(tokens[len(tokens)-1] == "now" || tokens[len(tokens)-1] == "me" || tokens[len(tokens)-1] == "today") {
		tokens = tokens[:len(tokens)-4]
	}
	if len(tokens) >= 5 &&
		tokens[len(tokens)-5] == "that" &&
		tokens[len(tokens)-4] == "is" &&
		(tokens[len(tokens)-3] == "it" || tokens[len(tokens)-3] == "all") &&
		tokens[len(tokens)-2] == "for" &&
		(tokens[len(tokens)-1] == "now" || tokens[len(tokens)-1] == "me" || tokens[len(tokens)-1] == "today") {
		tokens = tokens[:len(tokens)-5]
	}
	if len(tokens) >= 2 &&
		tokens[len(tokens)-2] == "for" &&
		isSpokenEmailSignoffObject(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-2]
	}
	if len(tokens) >= 3 &&
		tokens[len(tokens)-3] == "for" &&
		tokens[len(tokens)-2] == "the" &&
		tokens[len(tokens)-1] == "day" {
		tokens = tokens[:len(tokens)-3]
	}
	return tokens
}

func trimTrailingSpokenEmailSignoffParts(tokens []string) []string {
	if len(tokens) >= 5 &&
		tokens[len(tokens)-5] == "that" &&
		tokens[len(tokens)-4] == "is" &&
		(tokens[len(tokens)-3] == "it" || tokens[len(tokens)-3] == "all") &&
		tokens[len(tokens)-2] == "for" &&
		isSpokenEmailSignoffObject(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-5]
	}
	if len(tokens) >= 5 &&
		tokens[len(tokens)-5] == "that" &&
		tokens[len(tokens)-4] == "s" &&
		(tokens[len(tokens)-3] == "it" || tokens[len(tokens)-3] == "all") &&
		tokens[len(tokens)-2] == "for" &&
		isSpokenEmailSignoffObject(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-5]
	}
	if len(tokens) >= 4 &&
		(tokens[len(tokens)-4] == "that's" || tokens[len(tokens)-4] == "thats") &&
		(tokens[len(tokens)-3] == "it" || tokens[len(tokens)-3] == "all") &&
		tokens[len(tokens)-2] == "for" &&
		isSpokenEmailSignoffObject(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-4]
	}
	if len(tokens) >= 2 &&
		tokens[len(tokens)-2] == "for" &&
		isSpokenEmailSignoffObject(tokens[len(tokens)-1]) {
		return tokens[:len(tokens)-2]
	}
	return tokens
}

func isSpokenEmailSignoffObject(token string) bool {
	switch token {
	case "day", "me", "now", "today", "you":
		return true
	default:
		return false
	}
}

func hasSpokenEmailAddressShape(tokens []string) bool {
	seenAt := false
	for _, token := range tokens {
		if token == "at" {
			seenAt = true
			continue
		}
		if seenAt {
			if _, ok := fusedSpokenEmailDotSuffix(token); ok {
				return true
			}
			if token == "dot" || token == "period" || token == "point" {
				return true
			}
			if token == "full" {
				continue
			}
			if token == "stop" {
				return true
			}
		}
	}
	return false
}

func consumeSpokenEmailSpelling(tokens []string, start int) (string, int, bool) {
	var spelled strings.Builder
	repeat := 1
	for j := start; j < len(tokens); j++ {
		switch tokens[j] {
		case "single":
			repeat = 1
			continue
		case "double":
			if j+1 < len(tokens) && (tokens[j+1] == "u" || tokens[j+1] == "ewe" || tokens[j+1] == "you") {
				spelled.WriteString("w")
				repeat = 1
				j++
				continue
			}
			repeat = 2
			continue
		case "triple":
			repeat = 3
			continue
		case "quadruple":
			repeat = 4
			continue
		}
		letter := tokens[j]
		if !isSingleEmailLetter(letter) {
			var ok bool
			letter, ok = spokenEmailLetterName(tokens[j])
			if !ok {
				return spelled.String(), j, spelled.Len() > 0
			}
		}
		if !isSingleEmailLetter(letter) {
			return spelled.String(), j, spelled.Len() > 0
		}
		for range repeat {
			spelled.WriteString(letter)
		}
		repeat = 1
	}
	return spelled.String(), len(tokens), spelled.Len() > 0
}

func cleanSpokenEmailToken(token string) string {
	return strings.Trim(token, ".,!?;:")
}

func isSingleEmailLetter(token string) bool {
	return len(token) == 1 && token[0] >= 'a' && token[0] <= 'z'
}

func emailFailureTarget(ctx context.Context, fallback *GetEmailTask) *GetEmailTask {
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.Session == nil {
		return fallback
	}
	currentAgent, err := runCtx.Session.CurrentAgent()
	if err != nil {
		return fallback
	}
	if task, ok := currentAgent.(*GetEmailTask); ok {
		return task
	}
	return fallback
}

type declineEmailCaptureTool struct {
	task *GetEmailTask
}

func (t *declineEmailCaptureTool) ID() string   { return "decline_email_capture" }
func (t *declineEmailCaptureTool) Name() string { return "decline_email_capture" }
func (t *declineEmailCaptureTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
func (t *declineEmailCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide an email address."
}
func (t *declineEmailCaptureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the user declined to provide the email address"},
		},
		"required": []string{"reason"},
	}
}

func (t *declineEmailCaptureTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	_ = emailFailureTarget(ctx, t.task).Fail(llm.NewToolError(fmt.Sprintf("couldn't get the email address: %s", params.Reason)))
	return "", nil
}
