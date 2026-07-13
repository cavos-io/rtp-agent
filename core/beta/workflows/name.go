package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GetNameResult struct {
	FirstName  string
	MiddleName string
	LastName   string
}

type GetNameOptions struct {
	AgentOptions
	FirstName              bool
	MiddleName             bool
	LastName               bool
	NamePartsSet           bool
	NameFormat             string
	VerifySpelling         bool
	ExtraInstructions      string
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ChatContext            *llm.ChatContext
	Tools                  []llm.Tool
}

type GetNameTask struct {
	agent.AgentTask[*GetNameResult]
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	CollectFirstName       bool
	CollectMiddleName      bool
	CollectLastName        bool
	VerifySpelling         bool
	nameFormat             string
	firstName              string
	middleName             string
	lastName               string
}

const nameConfirmationInstruction = "Call `confirm_name` after the user confirmed the name is correct."

const nameInstructionsBeforeConfirmation = `You are only a single step in a broader system, responsible solely for capturing the user's name.
You need to naturally collect the name parts in this order: %s.
Handle input as noisy voice transcription. Expect that users will say names aloud and may:
- Say their name followed by spelling: e.g., 'Michael m i c h a e l'
- Use phonetic alphabet: e.g., 'Mike as in Mike India Charlie Hotel Alpha Echo Lima'
- Have names with special characters or hyphens: e.g., 'Mary-Jane' or 'O'Brien'
- Have names from various cultural backgrounds with different pronunciation patterns
Normalize common spoken patterns silently:
- Convert 'dash' or 'hyphen' to ` + "`-`" + `.
- Convert 'apostrophe' to ` + "`'`" + `.
- Recognize when users spell out their name letter by letter.
- Filter out filler words or hesitations.
- Capitalize the first letter of each name part appropriately.
Don't mention corrections. Treat inputs as possibly imperfect but fix them silently.
%sCall ` + "`update_name`" + ` at the first opportunity whenever you form a new hypothesis about the name. (before asking any questions or providing any answers.)
Don't invent names, stick strictly to what the user said.
`

const nameTextInstructionsBeforeConfirmation = `You are only a single step in a broader system, responsible solely for capturing the user's name.
You need to naturally collect the name parts in this order: %s.
Handle input as typed text. Expect users to type their name directly.
Capitalize the first letter of each name part appropriately.
If the name contains special characters or hyphens (e.g., 'Mary-Jane' or 'O'Brien'), preserve them as typed.
%sCall ` + "`update_name`" + ` at the first opportunity whenever you form a new hypothesis about the name. (before asking any questions or providing any answers.)
Don't invent names, stick strictly to what the user said.
`

const nameInstructionsAfterConfirmation = `If the name is unclear or it takes too much back-and-forth, prompt for each name part separately.
Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.
Avoid verbosity by not sharing example names or spellings unless prompted to do so. Do not deviate from the goal of collecting the user's name.
Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.`

func NewGetNameTask(opts GetNameOptions) *GetNameTask {
	if !opts.FirstName && !opts.MiddleName && !opts.LastName {
		if opts.NamePartsSet {
			panic("At least one of first_name, middle_name, or last_name must be True")
		}
		opts.FirstName = true
	}
	requireConfirmation := true
	if opts.RequireConfirmationSet {
		requireConfirmation = opts.RequireConfirmation
	}
	nameFormat := strings.TrimSpace(opts.NameFormat)
	if nameFormat == "" {
		nameFormat = buildNameFormat(opts.FirstName, opts.MiddleName, opts.LastName)
	}
	spellingInstructions := ""
	if opts.VerifySpelling {
		spellingInstructions = "After receiving the name, always verify the spelling by asking the user to confirm or spell out the name letter by letter. When confirming, spell out each name part letter by letter to the user. "
	}
	instructions := nameInstructions(requireConfirmation, nameFormat, spellingInstructions)
	textInstructions := nameTextInstructions(opts.RequireConfirmationSet && opts.RequireConfirmation, nameFormat, spellingInstructions)
	if strings.TrimSpace(opts.ExtraInstructions) != "" {
		extra := "\n" + strings.TrimSpace(opts.ExtraInstructions)
		instructions += extra
		textInstructions += extra
	}
	t := &GetNameTask{
		AgentTask:              *agent.NewAgentTask[*GetNameResult](instructions),
		RequireConfirmation:    requireConfirmation,
		RequireConfirmationSet: opts.RequireConfirmationSet,
		RequireExplicitAsk:     opts.RequireExplicitAsk,
		CollectFirstName:       opts.FirstName,
		CollectMiddleName:      opts.MiddleName,
		CollectLastName:        opts.LastName,
		VerifySpelling:         opts.VerifySpelling,
		nameFormat:             nameFormat,
	}
	t.InstructionVariants = llm.NewInstructions(instructions, textInstructions)
	applyAgentOptions(&t.Agent, opts.AgentOptions)
	if opts.ChatContext != nil {
		t.ChatCtx = opts.ChatContext.Copy()
	}
	t.Agent.Tools = append(append([]llm.Tool{}, opts.Tools...),
		&updateNameTool{task: t},
		&declineNameCaptureTool{task: t},
	)
	return t
}

func nameInstructions(requireConfirmation bool, nameFormat string, spellingInstructions string) string {
	beforeConfirmation := fmt.Sprintf(nameInstructionsBeforeConfirmation, nameFormat, spellingInstructions)
	if !requireConfirmation {
		return beforeConfirmation + nameInstructionsAfterConfirmation
	}
	return beforeConfirmation + nameConfirmationInstruction + "\n" + nameInstructionsAfterConfirmation
}

func nameTextInstructions(requireConfirmation bool, nameFormat string, spellingInstructions string) string {
	beforeConfirmation := fmt.Sprintf(nameTextInstructionsBeforeConfirmation, nameFormat, spellingInstructions)
	if !requireConfirmation {
		return beforeConfirmation + nameInstructionsAfterConfirmation
	}
	return beforeConfirmation + nameConfirmationInstruction + "\n" + nameInstructionsAfterConfirmation
}

func nameConfirmationRequired(ctx context.Context, requireConfirmation bool, set bool) bool {
	if set {
		return requireConfirmation
	}
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.SpeechHandle == nil {
		return true
	}
	return runCtx.SpeechHandle.InputDetails.Modality == "audio"
}

func (t *GetNameTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: nameOnEnterPrompt(t.nameFormat),
			})
		}
	}
}

func nameOnEnterPrompt(nameFormat string) string {
	return fmt.Sprintf(
		"Get the user's name (follow this order %q but do not mention the format). First scan the conversation - if a name was already given earlier, ask a short confirmation question rather than asking from scratch. If context about what the name is FOR was provided (a role like 'cardholder', 'guest', 'emergency contact'), anchor your confirmation question to that role so the user knows which name you mean - don't ask abstractly. When pointing at where an existing name came from, reference the source in the conversation (the earlier step, the booking they mentioned), not a presumption about how the name appears in the destination. Only ask fresh when the conversation has no name yet.",
		nameFormat,
	)
}

func buildNameFormat(firstName bool, middleName bool, lastName bool) string {
	parts := make([]string, 0, 3)
	if firstName {
		parts = append(parts, "{first_name}")
	}
	if middleName {
		parts = append(parts, "{middle_name}")
	}
	if lastName {
		parts = append(parts, "{last_name}")
	}
	return strings.Join(parts, " ")
}

func (t *GetNameTask) completeName() {
	t.Complete(&GetNameResult{
		FirstName:  t.firstName,
		MiddleName: t.middleName,
		LastName:   t.lastName,
	})
}

func (t *GetNameTask) fullName() string {
	formatted := t.nameFormat
	replacements := map[string]string{
		"{first_name}":  t.firstName,
		"{middle_name}": t.middleName,
		"{last_name}":   t.lastName,
	}
	for placeholder, value := range replacements {
		formatted = strings.ReplaceAll(formatted, placeholder, value)
	}
	return strings.TrimSpace(formatted)
}

type updateNameTool struct {
	task *GetNameTask
}

func (t *updateNameTool) ID() string   { return "update_name" }
func (t *updateNameTool) Name() string { return "update_name" }
func (t *updateNameTool) ToolFlags() llm.ToolFlag {
	if t.task.RequireExplicitAsk {
		return llm.ToolFlagIgnoreOnEnter
	}
	return llm.ToolFlagNone
}
func (t *updateNameTool) Description() string {
	return "Update the name provided by the user."
}
func (t *updateNameTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"first_name":  map[string]any{"type": "string", "description": "The user's first name."},
			"middle_name": map[string]any{"type": "string", "description": "The user's middle name, if collected."},
			"last_name":   map[string]any{"type": "string", "description": "The user's last name, if collected."},
		},
	}
}

func (t *updateNameTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		FirstName  string `json:"first_name"`
		MiddleName string `json:"middle_name"`
		LastName   string `json:"last_name"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	firstName := normalizeNamePart(params.FirstName)
	middleName := normalizeNamePart(params.MiddleName)
	lastName := normalizeNamePart(params.LastName)
	if err := t.task.validateNameParts(firstName, middleName, lastName); err != nil {
		return "", err
	}

	t.task.firstName = firstName
	t.task.middleName = middleName
	t.task.lastName = lastName
	if !nameConfirmationRequired(ctx, t.task.RequireConfirmation, t.task.RequireConfirmationSet) {
		t.task.completeName()
		return "", nil
	}

	t.task.setConfirmNameTool(firstName, middleName, lastName)
	if t.task.VerifySpelling {
		return "The name has been updated.\nAsk the user to confirm the updated name spelling without repeating it back.\nPrompt the user for confirmation, do not call `confirm_name` directly", nil
	}
	return "The name has been updated.\nAsk the user to confirm the updated name without repeating it back.\nPrompt the user for confirmation, do not call `confirm_name` directly", nil
}

func (t *GetNameTask) validateNameParts(firstName string, middleName string, lastName string) error {
	errors := make([]string, 0, 3)
	if t.CollectFirstName && firstName == "" {
		errors = append(errors, "first name is required but was not provided")
	}
	if t.CollectMiddleName && middleName == "" {
		errors = append(errors, "middle name is required but was not provided")
	}
	if t.CollectLastName && lastName == "" {
		errors = append(errors, "last name is required but was not provided")
	}
	for _, part := range []struct {
		label string
		value string
	}{
		{label: "first", value: firstName},
		{label: "middle", value: middleName},
		{label: "last", value: lastName},
	} {
		if part.value != "" && !containsLetter(part.value) {
			errors = append(errors, fmt.Sprintf("%s name '%s' contains no letters - that doesn't look like a name", part.label, part.value))
		}
	}
	if len(errors) > 0 {
		return llm.NewToolError("Incomplete name: " + strings.Join(errors, "; "))
	}
	return nil
}

func containsLetter(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func normalizeNamePart(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parts := strings.Fields(trimmed)
	parts = trimSpokenNamePreamble(parts)
	parts = filterSpokenNameFillers(parts)
	parts = trimTrailingSpokenNameFiller(parts)
	if len(parts) == 0 {
		return ""
	}
	if len(parts) <= 1 {
		return capitalizeNamePart(parts[0])
	}
	if name, ok := normalizeNameFollowedBySpelling(parts); ok {
		return name
	}
	if phonetic, ok := normalizePhoneticNameSpelling(parts); ok {
		return phonetic
	}
	if containsSpokenNameSymbol(parts) {
		var out strings.Builder
		attachNext := false
		for i := 0; i < len(parts); i++ {
			part := parts[i]
			lower := cleanSpokenNameToken(part)
			if lower == "hy" && i+1 < len(parts) && cleanSpokenNameToken(parts[i+1]) == "phen" {
				out.WriteString("-")
				attachNext = true
				i++
				if i+1 < len(parts) && isSpokenNameSymbolSuffix(parts[i+1]) {
					i++
				}
				continue
			}
			if lower == "single" && i+1 < len(parts) && cleanSpokenNameToken(parts[i+1]) == "quote" {
				out.WriteString("'")
				attachNext = true
				i++
				if i+1 < len(parts) && isSpokenNameSymbolSuffix(parts[i+1]) {
					i++
				}
				continue
			}
			if symbol, ok := spokenNameSymbols[lower]; ok {
				out.WriteString(symbol)
				attachNext = true
				if i+1 < len(parts) && isSpokenNameSymbolSuffix(parts[i+1]) {
					i++
				}
				continue
			}
			if out.Len() > 0 && !attachNext {
				out.WriteRune(' ')
			}
			out.WriteString(part)
			attachNext = false
		}
		return capitalizeNamePart(out.String())
	}
	var b strings.Builder
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if letter, consumed, ok := consumeSpokenNameLetter(parts, i); ok {
			b.WriteString(letter)
			i += consumed - 1
			continue
		}
		if repeat, ok := spokenNameRepeatWord(part); ok {
			if i+1 >= len(parts) {
				return capitalizeNamePart(trimmed)
			}
			next := cleanSpokenNameToken(parts[i+1])
			if letter, ok := spokenNameLetterAlias(next); ok {
				next = letter
			}
			runes := []rune(next)
			if len(runes) != 1 || !unicode.IsLetter(runes[0]) {
				return capitalizeNamePart(trimmed)
			}
			for range repeat {
				b.WriteRune(runes[0])
			}
			i++
			continue
		}
		token := cleanSpokenNameToken(part)
		if letter, ok := spokenNameLetterAlias(token); ok {
			token = letter
		}
		runes := []rune(token)
		if len(runes) != 1 || !unicode.IsLetter(runes[0]) {
			return capitalizeNamePart(trimmed)
		}
		b.WriteRune(runes[0])
	}
	return capitalizeNamePart(b.String())
}

func normalizeNameFollowedBySpelling(parts []string) (string, bool) {
	spoken := cleanSpokenNameToken(parts[0])
	if spoken == "" || !containsLetter(spoken) {
		return "", false
	}
	spelled, ok := spelledNameLetters(parts[1:])
	if !ok || !strings.EqualFold(spoken, spelled) {
		return "", false
	}
	return capitalizeNamePart(strings.Trim(parts[0], ".,!?;:")), true
}

func spelledNameLetters(parts []string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if letter, consumed, ok := consumeSpokenNameLetter(parts, i); ok {
			b.WriteString(letter)
			i += consumed - 1
			continue
		}
		if repeat, ok := spokenNameRepeatWord(part); ok {
			if i+1 >= len(parts) {
				return "", false
			}
			next := cleanSpokenNameToken(parts[i+1])
			if letter, ok := spokenNameLetterAlias(next); ok {
				next = letter
			}
			runes := []rune(next)
			if len(runes) != 1 || !unicode.IsLetter(runes[0]) {
				return "", false
			}
			for range repeat {
				b.WriteRune(runes[0])
			}
			i++
			continue
		}
		token := cleanSpokenNameToken(part)
		if letter, ok := spokenNameLetterAlias(token); ok {
			token = letter
		}
		runes := []rune(token)
		if len(runes) != 1 || !unicode.IsLetter(runes[0]) {
			return "", false
		}
		b.WriteRune(runes[0])
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String(), true
}

func consumeSpokenNameLetter(parts []string, i int) (string, int, bool) {
	if i+1 >= len(parts) || cleanSpokenNameToken(parts[i]) != "double" {
		return "", 0, false
	}
	next := cleanSpokenNameToken(parts[i+1])
	if next == "u" || next == "ewe" || next == "you" {
		return "w", 2, true
	}
	return "", 0, false
}

func spokenNameLetterAlias(word string) (string, bool) {
	aliases := map[string]string{
		"ay":     "a",
		"aye":    "a",
		"bee":    "b",
		"be":     "b",
		"cee":    "c",
		"sea":    "c",
		"see":    "c",
		"dee":    "d",
		"ee":     "e",
		"eff":    "f",
		"gee":    "g",
		"aitch":  "h",
		"haitch": "h",
		"eye":    "i",
		"jay":    "j",
		"kay":    "k",
		"el":     "l",
		"ell":    "l",
		"em":     "m",
		"en":     "n",
		"oh":     "o",
		"owe":    "o",
		"pea":    "p",
		"pee":    "p",
		"cue":    "q",
		"queue":  "q",
		"are":    "r",
		"ess":    "s",
		"tea":    "t",
		"tee":    "t",
		"ewe":    "u",
		"you":    "u",
		"vee":    "v",
		"ex":     "x",
		"why":    "y",
		"zed":    "z",
		"zee":    "z",
	}
	letter, ok := aliases[word]
	return letter, ok
}

func normalizePhoneticNameSpelling(parts []string) (string, bool) {
	asIndex := -1
	for i := 1; i+1 < len(parts); i++ {
		if cleanSpokenNameToken(parts[i]) == "as" && cleanSpokenNameToken(parts[i+1]) == "in" {
			asIndex = i
			break
		}
	}
	if asIndex == -1 || asIndex+2 >= len(parts) {
		return "", false
	}
	var spelled strings.Builder
	for i := asIndex + 2; i < len(parts); i++ {
		token := cleanSpokenNameToken(parts[i])
		if token == "x" && i+1 < len(parts) && cleanSpokenNameToken(parts[i+1]) == "ray" {
			spelled.WriteRune('x')
			i++
			continue
		}
		letter, ok := spokenNamePhoneticLetters[token]
		if !ok {
			return "", false
		}
		spelled.WriteRune(letter)
	}
	if spelled.Len() == 0 {
		return "", false
	}
	return capitalizeNamePart(spelled.String()), true
}

func capitalizeNamePart(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	capitalizeNext := true
	for i, r := range runes {
		if capitalizeNext && unicode.IsLetter(r) {
			runes[i] = unicode.ToUpper(r)
			capitalizeNext = false
			continue
		}
		capitalizeNext = r == '-' || r == '\'' || unicode.IsSpace(r)
	}
	return string(runes)
}

func trimSpokenNamePreamble(parts []string) []string {
	if len(parts) >= 4 &&
		isSpokenNameFieldLabel(parts[0]) &&
		cleanSpokenNameToken(parts[1]) == "name" &&
		cleanSpokenNameToken(parts[2]) == "is" {
		return parts[3:]
	}
	if len(parts) >= 5 &&
		isSpokenNameFieldLabel(parts[0]) &&
		cleanSpokenNameToken(parts[1]) == "name" &&
		cleanSpokenNameToken(parts[2]) == "will" &&
		cleanSpokenNameToken(parts[3]) == "be" {
		return parts[4:]
	}
	if len(parts) >= 3 &&
		isSpokenNameFieldLabel(parts[0]) &&
		cleanSpokenNameToken(parts[1]) == "name's" {
		return parts[2:]
	}
	if len(parts) >= 4 &&
		isSpokenNameFieldLabel(parts[0]) &&
		cleanSpokenNameToken(parts[1]) == "name" &&
		cleanSpokenNameToken(parts[2]) == "s" {
		return parts[3:]
	}
	if len(parts) >= 3 &&
		isSpokenNameFieldLabel(parts[0]) &&
		cleanSpokenNameToken(parts[1]) == "is" {
		return parts[2:]
	}
	if len(parts) >= 4 &&
		isSpokenNameFieldLabel(parts[0]) &&
		cleanSpokenNameToken(parts[1]) == "will" &&
		cleanSpokenNameToken(parts[2]) == "be" {
		return parts[3:]
	}
	if len(parts) >= 3 &&
		cleanSpokenNameToken(parts[0]) == "my" &&
		cleanSpokenNameToken(parts[1]) == "name" &&
		cleanSpokenNameToken(parts[2]) == "is" {
		return parts[3:]
	}
	if len(parts) >= 4 &&
		cleanSpokenNameToken(parts[0]) == "my" &&
		cleanSpokenNameToken(parts[1]) == "name" &&
		cleanSpokenNameToken(parts[2]) == "will" &&
		cleanSpokenNameToken(parts[3]) == "be" {
		return parts[4:]
	}
	if len(parts) >= 2 &&
		cleanSpokenNameToken(parts[0]) == "my" &&
		cleanSpokenNameToken(parts[1]) == "name's" {
		return parts[2:]
	}
	if len(parts) >= 3 &&
		cleanSpokenNameToken(parts[0]) == "my" &&
		cleanSpokenNameToken(parts[1]) == "name" &&
		cleanSpokenNameToken(parts[2]) == "s" {
		return parts[3:]
	}
	if len(parts) >= 3 &&
		cleanSpokenNameToken(parts[0]) == "the" &&
		cleanSpokenNameToken(parts[1]) == "name" &&
		cleanSpokenNameToken(parts[2]) == "is" {
		return parts[3:]
	}
	if len(parts) >= 4 &&
		cleanSpokenNameToken(parts[0]) == "the" &&
		cleanSpokenNameToken(parts[1]) == "name" &&
		cleanSpokenNameToken(parts[2]) == "will" &&
		cleanSpokenNameToken(parts[3]) == "be" {
		return parts[4:]
	}
	if len(parts) >= 2 &&
		cleanSpokenNameToken(parts[0]) == "the" &&
		cleanSpokenNameToken(parts[1]) == "name's" {
		return parts[2:]
	}
	if len(parts) >= 3 &&
		cleanSpokenNameToken(parts[0]) == "the" &&
		cleanSpokenNameToken(parts[1]) == "name" &&
		cleanSpokenNameToken(parts[2]) == "s" {
		return parts[3:]
	}
	if len(parts) >= 2 &&
		cleanSpokenNameToken(parts[0]) == "name" &&
		cleanSpokenNameToken(parts[1]) == "is" {
		return parts[2:]
	}
	if len(parts) >= 3 &&
		cleanSpokenNameToken(parts[0]) == "name" &&
		cleanSpokenNameToken(parts[1]) == "will" &&
		cleanSpokenNameToken(parts[2]) == "be" {
		return parts[3:]
	}
	if len(parts) >= 1 && cleanSpokenNameToken(parts[0]) == "name's" {
		return parts[1:]
	}
	if len(parts) >= 2 &&
		cleanSpokenNameToken(parts[0]) == "name" &&
		cleanSpokenNameToken(parts[1]) == "s" {
		return parts[2:]
	}
	return parts
}

func isSpokenNameFieldLabel(part string) bool {
	switch cleanSpokenNameToken(part) {
	case "first", "middle", "last":
		return true
	default:
		return false
	}
}

func filterSpokenNameFillers(parts []string) []string {
	out := parts[:0]
	for _, part := range parts {
		if _, ok := spokenNameFillers[cleanSpokenNameToken(part)]; ok {
			continue
		}
		out = append(out, part)
	}
	return out
}

func trimTrailingSpokenNameFiller(parts []string) []string {
	if trimmed := trimTrailingSpokenNameSignoffParts(parts); len(trimmed) != len(parts) {
		return trimmed
	}
	trailing := map[string]struct{}{
		"done": {}, "ok": {}, "okay": {}, "please": {}, "thanks": {}, "thank": {}, "you": {},
	}
	if len(parts) >= 2 &&
		cleanSpokenNameToken(parts[len(parts)-1]) == "done" &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "all" {
		parts = parts[:len(parts)-2]
	}
	for len(parts) > 0 {
		last := cleanSpokenNameToken(parts[len(parts)-1])
		if last == "you" && len(parts) >= 2 && cleanSpokenNameToken(parts[len(parts)-2]) == "for" {
			break
		}
		if _, ok := trailing[last]; !ok {
			break
		}
		parts = parts[:len(parts)-1]
	}
	if len(parts) >= 5 &&
		(cleanSpokenNameToken(parts[len(parts)-5]) == "that's" || cleanSpokenNameToken(parts[len(parts)-5]) == "thats") &&
		(cleanSpokenNameToken(parts[len(parts)-4]) == "it" || cleanSpokenNameToken(parts[len(parts)-4]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-3]) == "for" &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "the" &&
		cleanSpokenNameToken(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 6 &&
		cleanSpokenNameToken(parts[len(parts)-6]) == "that" &&
		cleanSpokenNameToken(parts[len(parts)-5]) == "is" &&
		(cleanSpokenNameToken(parts[len(parts)-4]) == "it" || cleanSpokenNameToken(parts[len(parts)-4]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-3]) == "for" &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "the" &&
		cleanSpokenNameToken(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 7 &&
		cleanSpokenNameToken(parts[len(parts)-7]) == "that" &&
		cleanSpokenNameToken(parts[len(parts)-6]) == "will" &&
		cleanSpokenNameToken(parts[len(parts)-5]) == "be" &&
		(cleanSpokenNameToken(parts[len(parts)-4]) == "it" || cleanSpokenNameToken(parts[len(parts)-4]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-3]) == "for" &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "the" &&
		cleanSpokenNameToken(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-7]
	}
	if len(parts) >= 6 &&
		(cleanSpokenNameToken(parts[len(parts)-6]) == "that'll" || cleanSpokenNameToken(parts[len(parts)-6]) == "thatll") &&
		cleanSpokenNameToken(parts[len(parts)-5]) == "be" &&
		(cleanSpokenNameToken(parts[len(parts)-4]) == "it" || cleanSpokenNameToken(parts[len(parts)-4]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-3]) == "for" &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "the" &&
		cleanSpokenNameToken(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 7 &&
		cleanSpokenNameToken(parts[len(parts)-7]) == "that" &&
		cleanSpokenNameToken(parts[len(parts)-6]) == "ll" &&
		cleanSpokenNameToken(parts[len(parts)-5]) == "be" &&
		(cleanSpokenNameToken(parts[len(parts)-4]) == "it" || cleanSpokenNameToken(parts[len(parts)-4]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-3]) == "for" &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "the" &&
		cleanSpokenNameToken(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-7]
	}
	if len(parts) >= 5 &&
		(cleanSpokenNameToken(parts[len(parts)-5]) == "that'll" || cleanSpokenNameToken(parts[len(parts)-5]) == "thatll") &&
		cleanSpokenNameToken(parts[len(parts)-4]) == "be" &&
		(cleanSpokenNameToken(parts[len(parts)-3]) == "it" || cleanSpokenNameToken(parts[len(parts)-3]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "for" &&
		isSpokenNameSignoffObject(cleanSpokenNameToken(parts[len(parts)-1])) {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 6 &&
		cleanSpokenNameToken(parts[len(parts)-6]) == "that" &&
		cleanSpokenNameToken(parts[len(parts)-5]) == "ll" &&
		cleanSpokenNameToken(parts[len(parts)-4]) == "be" &&
		(cleanSpokenNameToken(parts[len(parts)-3]) == "it" || cleanSpokenNameToken(parts[len(parts)-3]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "for" &&
		isSpokenNameSignoffObject(cleanSpokenNameToken(parts[len(parts)-1])) {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 4 &&
		(cleanSpokenNameToken(parts[len(parts)-4]) == "that's" || cleanSpokenNameToken(parts[len(parts)-4]) == "thats") &&
		(cleanSpokenNameToken(parts[len(parts)-3]) == "it" || cleanSpokenNameToken(parts[len(parts)-3]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "for" &&
		(cleanSpokenNameToken(parts[len(parts)-1]) == "now" || cleanSpokenNameToken(parts[len(parts)-1]) == "me" || cleanSpokenNameToken(parts[len(parts)-1]) == "today") {
		return parts[:len(parts)-4]
	}
	if len(parts) >= 5 &&
		cleanSpokenNameToken(parts[len(parts)-5]) == "that" &&
		cleanSpokenNameToken(parts[len(parts)-4]) == "is" &&
		(cleanSpokenNameToken(parts[len(parts)-3]) == "it" || cleanSpokenNameToken(parts[len(parts)-3]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "for" &&
		(cleanSpokenNameToken(parts[len(parts)-1]) == "now" || cleanSpokenNameToken(parts[len(parts)-1]) == "me" || cleanSpokenNameToken(parts[len(parts)-1]) == "today") {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 2 &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "for" &&
		isSpokenNameSignoffObject(cleanSpokenNameToken(parts[len(parts)-1])) {
		return parts[:len(parts)-2]
	}
	if len(parts) >= 3 &&
		cleanSpokenNameToken(parts[len(parts)-3]) == "for" &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "the" &&
		cleanSpokenNameToken(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-3]
	}
	if len(parts) >= 2 &&
		cleanSpokenNameToken(parts[len(parts)-1]) == "it" &&
		(cleanSpokenNameToken(parts[len(parts)-2]) == "that's" || cleanSpokenNameToken(parts[len(parts)-2]) == "thats") {
		return parts[:len(parts)-2]
	}
	if len(parts) >= 2 &&
		cleanSpokenNameToken(parts[len(parts)-1]) == "all" &&
		(cleanSpokenNameToken(parts[len(parts)-2]) == "that's" || cleanSpokenNameToken(parts[len(parts)-2]) == "thats") {
		return parts[:len(parts)-2]
	}
	if len(parts) >= 3 &&
		(cleanSpokenNameToken(parts[len(parts)-1]) == "it" || cleanSpokenNameToken(parts[len(parts)-1]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "is" &&
		cleanSpokenNameToken(parts[len(parts)-3]) == "that" {
		return parts[:len(parts)-3]
	}
	if len(parts) >= 3 &&
		(cleanSpokenNameToken(parts[len(parts)-3]) == "that'll" || cleanSpokenNameToken(parts[len(parts)-3]) == "thatll") &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "be" &&
		(cleanSpokenNameToken(parts[len(parts)-1]) == "it" || cleanSpokenNameToken(parts[len(parts)-1]) == "all") {
		return parts[:len(parts)-3]
	}
	if len(parts) >= 4 &&
		cleanSpokenNameToken(parts[len(parts)-4]) == "that" &&
		cleanSpokenNameToken(parts[len(parts)-3]) == "will" &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "be" &&
		(cleanSpokenNameToken(parts[len(parts)-1]) == "it" || cleanSpokenNameToken(parts[len(parts)-1]) == "all") {
		return parts[:len(parts)-4]
	}
	if len(parts) >= 4 &&
		cleanSpokenNameToken(parts[len(parts)-4]) == "that" &&
		cleanSpokenNameToken(parts[len(parts)-3]) == "ll" &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "be" &&
		(cleanSpokenNameToken(parts[len(parts)-1]) == "it" || cleanSpokenNameToken(parts[len(parts)-1]) == "all") {
		return parts[:len(parts)-4]
	}
	return parts
}

func trimTrailingSpokenNameSignoffParts(parts []string) []string {
	if len(parts) >= 5 &&
		cleanSpokenNameToken(parts[len(parts)-5]) == "that" &&
		cleanSpokenNameToken(parts[len(parts)-4]) == "is" &&
		(cleanSpokenNameToken(parts[len(parts)-3]) == "it" || cleanSpokenNameToken(parts[len(parts)-3]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "for" &&
		isSpokenNameSignoffObject(cleanSpokenNameToken(parts[len(parts)-1])) {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 5 &&
		cleanSpokenNameToken(parts[len(parts)-5]) == "that" &&
		cleanSpokenNameToken(parts[len(parts)-4]) == "s" &&
		(cleanSpokenNameToken(parts[len(parts)-3]) == "it" || cleanSpokenNameToken(parts[len(parts)-3]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "for" &&
		isSpokenNameSignoffObject(cleanSpokenNameToken(parts[len(parts)-1])) {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 4 &&
		(cleanSpokenNameToken(parts[len(parts)-4]) == "that's" || cleanSpokenNameToken(parts[len(parts)-4]) == "thats") &&
		(cleanSpokenNameToken(parts[len(parts)-3]) == "it" || cleanSpokenNameToken(parts[len(parts)-3]) == "all") &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "for" &&
		isSpokenNameSignoffObject(cleanSpokenNameToken(parts[len(parts)-1])) {
		return parts[:len(parts)-4]
	}
	if len(parts) >= 2 &&
		cleanSpokenNameToken(parts[len(parts)-2]) == "for" &&
		isSpokenNameSignoffObject(cleanSpokenNameToken(parts[len(parts)-1])) {
		return parts[:len(parts)-2]
	}
	return parts
}

func isSpokenNameSignoffObject(token string) bool {
	switch token {
	case "day", "me", "now", "today", "you":
		return true
	default:
		return false
	}
}

var spokenNameFillers = map[string]struct{}{
	"actually": {},
	"ah":       {},
	"er":       {},
	"hm":       {},
	"hmm":      {},
	"like":     {},
	"spell":    {},
	"spelled":  {},
	"sorry":    {},
	"uh":       {},
	"um":       {},
}

var spokenNamePhoneticLetters = map[string]rune{
	"alfa":     'a',
	"alpha":    'a',
	"bravo":    'b',
	"charlie":  'c',
	"delta":    'd',
	"echo":     'e',
	"foxtrot":  'f',
	"golf":     'g',
	"hotel":    'h',
	"india":    'i',
	"juliet":   'j',
	"juliett":  'j',
	"kilo":     'k',
	"lima":     'l',
	"mike":     'm',
	"november": 'n',
	"oscar":    'o',
	"papa":     'p',
	"quebec":   'q',
	"romeo":    'r',
	"sierra":   's',
	"tango":    't',
	"uniform":  'u',
	"victor":   'v',
	"whiskey":  'w',
	"xray":     'x',
	"x-ray":    'x',
	"yankee":   'y',
	"zulu":     'z',
}

func spokenNameRepeatWord(token string) (int, bool) {
	switch cleanSpokenNameToken(token) {
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

func containsSpokenNameSymbol(parts []string) bool {
	for i, part := range parts {
		if cleanSpokenNameToken(part) == "hy" && i+1 < len(parts) && cleanSpokenNameToken(parts[i+1]) == "phen" {
			return true
		}
		if cleanSpokenNameToken(part) == "single" && i+1 < len(parts) && cleanSpokenNameToken(parts[i+1]) == "quote" {
			return true
		}
		if _, ok := spokenNameSymbols[cleanSpokenNameToken(part)]; ok {
			return true
		}
	}
	return false
}

func cleanSpokenNameToken(token string) string {
	return strings.Trim(strings.ToLower(token), ".,!?;:")
}

var spokenNameSymbols = map[string]string{
	"dash":       "-",
	"hyphen":     "-",
	"minus":      "-",
	"apostrophe": "'",
	"quote":      "'",
}

func isSpokenNameSymbolSuffix(token string) bool {
	switch cleanSpokenNameToken(token) {
	case "sign", "symbol", "key", "mark":
		return true
	default:
		return false
	}
}

func (t *GetNameTask) setConfirmNameTool(firstName string, middleName string, lastName string) {
	tools := make([]llm.Tool, 0, len(t.Agent.Tools)+1)
	for _, tool := range t.Agent.Tools {
		if tool.ID() == "confirm_name" {
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, &confirmNameTool{
		task:       t,
		firstName:  firstName,
		middleName: middleName,
		lastName:   lastName,
	})
	t.Agent.Tools = tools
}

type confirmNameTool struct {
	task       *GetNameTask
	firstName  string
	middleName string
	lastName   string
}

func (t *confirmNameTool) ID() string   { return "confirm_name" }
func (t *confirmNameTool) Name() string { return "confirm_name" }
func (t *confirmNameTool) Description() string {
	return "Call after the user confirms the name is correct."
}
func (t *confirmNameTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *confirmNameTool) Execute(ctx context.Context, args string) (string, error) {
	if t.task.firstName != t.firstName || t.task.middleName != t.middleName || t.task.lastName != t.lastName {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: nameStaleConfirmationPrompt(),
			})
		}
		return "", nil
	}
	t.task.completeName()
	return "", nil
}

func nameStaleConfirmationPrompt() string {
	return "The name has changed since confirmation was requested, ask the user to confirm the updated name."
}

func nameFailureTarget(ctx context.Context, fallback *GetNameTask) *GetNameTask {
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.Session == nil {
		return fallback
	}
	currentAgent, err := runCtx.Session.CurrentAgent()
	if err != nil {
		return fallback
	}
	if task, ok := currentAgent.(*GetNameTask); ok {
		return task
	}
	return fallback
}

type declineNameCaptureTool struct {
	task *GetNameTask
}

func (t *declineNameCaptureTool) ID() string   { return "decline_name_capture" }
func (t *declineNameCaptureTool) Name() string { return "decline_name_capture" }
func (t *declineNameCaptureTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
func (t *declineNameCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide their name."
}
func (t *declineNameCaptureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the user declined to provide their name"},
		},
		"required": []string{"reason"},
	}
}

func (t *declineNameCaptureTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	_ = nameFailureTarget(ctx, t.task).Fail(llm.NewToolError(fmt.Sprintf("couldn't get the name: %s", params.Reason)))
	return "", nil
}
