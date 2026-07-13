package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cavos-io/rtp-agent/core/agent"
	beta "github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GetAddressResult struct {
	Address string
}

type GetAddressOptions struct {
	AgentOptions
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ExtraInstructions      string
	Instructions           *beta.InstructionParts
	ChatContext            *llm.ChatContext
	Tools                  []llm.Tool
}

type GetAddressTask struct {
	agent.AgentTask[*GetAddressResult]
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	currentAddress         string
	addressConfirmed       bool
}

const addressConfirmationInstruction = "Call `confirm_address` after the user confirmed the address is correct."

const addressPersona = "You are only a single step in a broader system, responsible solely for capturing an address."

const addressInstructionsBeforeConfirmation = addressPersona + `
You will be handling addresses from any country. Expect that users will say address in different formats with fields filled like:
- 'street_address': '450 SOUTH MAIN ST', 'unit_number': 'FLOOR 2', 'locality': 'SALT LAKE CITY UT 84101', 'country': 'UNITED STATES',
- 'street_address': '123 MAPLE STREET', 'unit_number': 'APARTMENT 10', 'locality': 'OTTAWA ON K1A 0B1', 'country': 'CANADA',
- 'street_address': 'GUOMAO JIE 3 HAO, CHAOYANG QU', 'unit_number': 'GUOMAO DA SHA 18 LOU 101 SHI', 'locality': 'BEIJING SHI 100000', 'country': 'CHINA',
- 'street_address': '5 RUE DE L'ANCIENNE COMÉDIE', 'unit_number': 'APP C4', 'locality': '75006 PARIS', 'country': 'FRANCE',
- 'street_address': 'PLOT 10, NEHRU ROAD', 'unit_number': 'OFFICE 403, 4TH FLOOR', 'locality': 'VILE PARLE (E), MUMBAI MAHARASHTRA 400099', 'country': 'INDIA',
Normalize common spoken patterns silently:
- Convert words like 'dash' and 'apostrophe' into symbols: ` + "`-`, `'`." + `
- Convert spelled out numbers like 'six' and 'seven' into numerals: ` + "`6`, `7`." + `
- Recognize patterns where users speak their address field followed by spelling: e.g., 'guomao g u o m a o'.
- Filter out filler words or hesitations.
- Recognize when there may be accents on certain letters if explicitly said or common in the location specified. Be sure to verify the correct accents if existent.
Don't mention corrections. Treat inputs as possibly imperfect but fix them silently.
Avoid using bullet points and parenthese in any responses.
Ask the user to confirm postal codes and spelled address parts without reading long values back.
Call ` + "`update_address`" + ` at the first opportunity whenever you form a new hypothesis about the address. (before asking any questions or providing any answers.)
Don't invent new addresses, stick strictly to what the user said.
`

const addressTextInstructionsBeforeConfirmation = addressPersona + `
You will be handling addresses from any country.
Expect users to type their address directly.
If the address looks almost correct but has minor issues (e.g. missing country or postal code), prompt for clarification.
Call ` + "`update_address`" + ` at the first opportunity whenever you form a new hypothesis about the address. (before asking any questions or providing any answers.)
Don't invent new addresses, stick strictly to what the user said.
`

const addressInstructionsAfterConfirmation = `If the address is unclear or invalid, or it takes too much back-and-forth, prompt for it in parts in this order: street address, unit number if applicable, locality, and country.
Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.
Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.`

const AddressInstructions = addressInstructionsBeforeConfirmation + addressConfirmationInstruction + "\n" + addressInstructionsAfterConfirmation

const addressInstructionsWithoutConfirmation = addressInstructionsBeforeConfirmation + addressInstructionsAfterConfirmation

const addressTextInstructions = addressTextInstructionsBeforeConfirmation + addressConfirmationInstruction + "\n" + addressInstructionsAfterConfirmation

const addressTextInstructionsWithoutConfirmation = addressTextInstructionsBeforeConfirmation + addressInstructionsAfterConfirmation

func NewGetAddressTask(opts GetAddressOptions) *GetAddressTask {
	requireConfirmation := true
	if opts.RequireConfirmationSet {
		requireConfirmation = opts.RequireConfirmation
	}
	instructions := AddressInstructions
	if !requireConfirmation {
		instructions = addressInstructionsWithoutConfirmation
	}
	instructions = applyInstructionParts(instructions, addressPersona, opts.Instructions)
	extraInstructions := opts.ExtraInstructions
	if opts.Instructions != nil {
		extraInstructions = ""
	}
	instructions = appendAddressExtraInstructions(instructions, extraInstructions)
	textInstructions := addressTextVariantInstructions(opts.RequireConfirmationSet && opts.RequireConfirmation, opts.Instructions)
	textInstructions = appendAddressExtraInstructions(textInstructions, extraInstructions)
	t := &GetAddressTask{
		AgentTask:              *agent.NewAgentTask[*GetAddressResult](instructions),
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
		&updateAddressTool{task: t},
		&declineAddressCaptureTool{task: t},
	)

	return t
}

func appendAddressExtraInstructions(instructions string, extraInstructions string) string {
	if extra := strings.TrimSpace(extraInstructions); extra != "" {
		return strings.TrimRight(instructions, "\n") + "\n" + extra
	}
	return instructions
}

func addressTextVariantInstructions(requireConfirmation bool, parts *beta.InstructionParts) string {
	instructions := addressTextInstructions
	if !requireConfirmation {
		instructions = addressTextInstructionsWithoutConfirmation
	}
	return applyInstructionParts(instructions, addressPersona, parts)
}

func addressConfirmationRequired(ctx context.Context, requireConfirmation bool, set bool) bool {
	if set {
		return requireConfirmation
	}
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.SpeechHandle == nil {
		return true
	}
	return runCtx.SpeechHandle.InputDetails.Modality == "audio"
}

func (t *GetAddressTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: addressOnEnterPrompt(),
			})
		}
	}
}

func addressOnEnterPrompt() string {
	return "Ask the user to provide their address."
}

type updateAddressTool struct {
	task *GetAddressTask
}

func (t *updateAddressTool) ID() string   { return "update_address" }
func (t *updateAddressTool) Name() string { return "update_address" }
func (t *updateAddressTool) ToolFlags() llm.ToolFlag {
	if t.task.RequireExplicitAsk {
		return llm.ToolFlagIgnoreOnEnter
	}
	return llm.ToolFlagNone
}
func (t *updateAddressTool) Description() string {
	return "Update the address provided by the user."
}
func (t *updateAddressTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"street_address": map[string]any{
				"type":        "string",
				"description": "Dependent on country, may include fields like house number, street name, block, or district",
			},
			"unit_number": map[string]any{
				"type":        "string",
				"description": "The unit number, for example Floor 1 or Apartment 12. If there is no unit number, return ''",
			},
			"locality": map[string]any{
				"type":        "string",
				"description": "Dependent on country, may include fields like city, zip code, or province",
			},
			"country": map[string]any{
				"type":        "string",
				"description": "The country the user lives in spelled out fully",
			},
		},
		"required": []string{"street_address", "unit_number", "locality", "country"},
	}
}

func (t *updateAddressTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		StreetAddress string `json:"street_address"`
		UnitNumber    string `json:"unit_number"`
		Locality      string `json:"locality"`
		Country       string `json:"country"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	streetAddress := normalizeAddressField(params.StreetAddress)
	unitNumber := normalizeAddressField(params.UnitNumber)
	locality := normalizeAddressField(params.Locality)
	country := normalizeAddressField(params.Country)

	addressFields := []string{streetAddress}
	if strings.TrimSpace(unitNumber) != "" {
		addressFields = append(addressFields, unitNumber)
	}
	addressFields = append(addressFields, locality, country)
	address := strings.Join(addressFields, " ")

	t.task.currentAddress = address

	if !addressConfirmationRequired(ctx, t.task.RequireConfirmation, t.task.RequireConfirmationSet) {
		_ = t.task.Complete(&GetAddressResult{Address: address})
		return "", nil
	}

	t.task.setConfirmAddressTool(address)
	return "The address has been updated.\nAsk the user to confirm the updated address without repeating it back.\nPrompt the user for confirmation, do not call `confirm_address` directly", nil
}

func normalizeAddressField(field string) string {
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

	var out []string
	var digitRun strings.Builder
	attachNext := false
	repeat := 1
	flushDigits := func() {
		if digitRun.Len() == 0 {
			return
		}
		if attachNext && len(out) > 0 {
			out[len(out)-1] += digitRun.String()
			attachNext = false
		} else if len(out) > 0 && (isSingleAddressLetter(out[len(out)-1]) || addressTokenAllDigits(out[len(out)-1]) || addressTokenStartsWithDigit(out[len(out)-1]) && addressTokenEndsWithLetter(out[len(out)-1]) || strings.Contains(out[len(out)-1], "/") && addressTokenContainsDigit(out[len(out)-1]) && addressTokenEndsWithLetter(out[len(out)-1])) {
			out[len(out)-1] += digitRun.String()
		} else {
			out = append(out, digitRun.String())
		}
		digitRun.Reset()
	}
	appendToken := func(token string) {
		if attachNext && len(out) > 0 {
			out[len(out)-1] += token
			attachNext = false
			return
		}
		if isSingleAddressLetter(token) && len(out) > 0 && addressTokenContainsDigit(out[len(out)-1]) {
			out[len(out)-1] += token
			return
		}
		out = append(out, token)
	}
	tokens := stripSpokenAddressFieldLabels(trimSpokenAddressPreamble(trimTrailingSpokenAddressFiller(strings.Fields(field))))
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		lower := cleanSpokenAddressToken(token)
		if _, ok := spokenAddressFillers[lower]; ok {
			flushDigits()
			continue
		}
		if lower == "single" && i+1 < len(tokens) && isSpokenAddressNumberLikeToken(cleanSpokenAddressToken(tokens[i+1])) {
			flushDigits()
			repeat = 1
			continue
		}
		if lower == "double" {
			if next, ok := nextSpokenAddressNonFiller(tokens, i+1); ok {
				if next.token == "u" || next.token == "ewe" || next.token == "you" {
					flushDigits()
					appendToken("w")
					i = next.index
					repeat = 1
					continue
				}
			}
		}
		if count, ok := spokenAddressRepeatWord(lower); ok {
			flushDigits()
			repeat = count
			continue
		}
		if len(lower) > 1 {
			spelled, j, ok := consumeSpokenAddressSpelling(tokens, i+1)
			if ok && spelled == lower {
				flushDigits()
				appendToken(token)
				i = j - 1
				continue
			}
		}
		if lower == "hy" && i+1 < len(tokens) && cleanSpokenAddressToken(tokens[i+1]) == "phen" {
			flushDigits()
			if len(out) == 0 {
				out = append(out, "-")
			} else {
				out[len(out)-1] += "-"
			}
			attachNext = true
			i++
			if i+1 < len(tokens) && isSpokenAddressSymbolSuffix(cleanSpokenAddressToken(tokens[i+1])) {
				i++
			}
			continue
		}
		if symbol, ok := spokenAddressSymbols[lower]; ok {
			flushDigits()
			if len(out) == 0 {
				out = append(out, symbol)
			} else {
				out[len(out)-1] += symbol
			}
			attachNext = true
			if i+1 < len(tokens) && isSpokenAddressSymbolSuffix(cleanSpokenAddressToken(tokens[i+1])) {
				i++
			}
			continue
		}
		if lower == "single" && i+1 < len(tokens) && cleanSpokenAddressToken(tokens[i+1]) == "quote" {
			flushDigits()
			if len(out) == 0 {
				out = append(out, "'")
			} else {
				out[len(out)-1] += "'"
			}
			attachNext = true
			i++
			if i+1 < len(tokens) && isSpokenAddressSymbolSuffix(cleanSpokenAddressToken(tokens[i+1])) {
				i++
			}
			continue
		}
		if isSpokenAddressNumberSignPrefix(lower) && i+1 < len(tokens) && isSpokenAddressNumberSignSuffix(cleanSpokenAddressToken(tokens[i+1])) {
			flushDigits()
			appendToken("#")
			attachNext = true
			i++
			continue
		}
		if isSpokenAddressBareNumberSignPrefix(lower) && i+1 < len(tokens) && isSpokenAddressNumberLikeToken(cleanSpokenAddressToken(tokens[i+1])) {
			flushDigits()
			appendToken("#")
			attachNext = true
			continue
		}
		next := nextAddressNumberToken(tokens, i+1)
		zeroBeforeNumber := isSpokenAddressZeroWord(lower) && next < len(tokens) && isSpokenAddressNumberLikeToken(cleanSpokenAddressToken(tokens[next]))
		if letter, ok := spokenAddressLetterAlias(lower); ok && !zeroBeforeNumber && isSpokenAddressAlphanumericContext(out, digitRun.Len() > 0, tokens, i) {
			flushDigits()
			appendToken(letter)
			repeat = 1
			continue
		}
		if ordinal, consumed := parseSpokenAddressOrdinal(tokens, i); consumed > 0 {
			flushDigits()
			appendToken(ordinal)
			i += consumed - 1
			repeat = 1
			continue
		}
		if compound, consumed := parseSpokenAddressNumber(tokens, i); consumed > 0 {
			for range repeat {
				digitRun.WriteString(compound)
			}
			repeat = 1
			i += consumed - 1
			continue
		}
		if digit, ok := digits[lower]; ok {
			for range repeat {
				digitRun.WriteString(digit)
			}
			repeat = 1
			continue
		}
		flushDigits()
		repeat = 1
		appendToken(token)
	}
	flushDigits()
	return strings.Join(out, " ")
}

func trimTrailingSpokenAddressFiller(tokens []string) []string {
	if trimmed := trimTrailingSpokenAddressSignoffParts(tokens); len(trimmed) != len(tokens) {
		return trimmed
	}
	trailing := map[string]struct{}{
		"done": {}, "ok": {}, "okay": {}, "please": {}, "thanks": {}, "thank": {}, "you": {},
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[len(tokens)-1]) == "done" &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "all" {
		tokens = tokens[:len(tokens)-2]
	}
	for len(tokens) > 0 {
		last := cleanSpokenAddressToken(tokens[len(tokens)-1])
		if last == "you" && len(tokens) >= 2 && cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" {
			break
		}
		if _, ok := trailing[last]; !ok {
			break
		}
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) >= 5 &&
		(cleanSpokenAddressToken(tokens[len(tokens)-5]) == "that's" || cleanSpokenAddressToken(tokens[len(tokens)-5]) == "thats") &&
		(cleanSpokenAddressToken(tokens[len(tokens)-4]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-4]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-3]) == "for" &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "the" &&
		cleanSpokenAddressToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-5]
	}
	if len(tokens) >= 6 &&
		cleanSpokenAddressToken(tokens[len(tokens)-6]) == "that" &&
		cleanSpokenAddressToken(tokens[len(tokens)-5]) == "is" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-4]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-4]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-3]) == "for" &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "the" &&
		cleanSpokenAddressToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-6]
	}
	if len(tokens) >= 7 &&
		cleanSpokenAddressToken(tokens[len(tokens)-7]) == "that" &&
		cleanSpokenAddressToken(tokens[len(tokens)-6]) == "will" &&
		cleanSpokenAddressToken(tokens[len(tokens)-5]) == "be" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-4]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-4]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-3]) == "for" &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "the" &&
		cleanSpokenAddressToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-7]
	}
	if len(tokens) >= 6 &&
		(cleanSpokenAddressToken(tokens[len(tokens)-6]) == "that'll" || cleanSpokenAddressToken(tokens[len(tokens)-6]) == "thatll") &&
		cleanSpokenAddressToken(tokens[len(tokens)-5]) == "be" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-4]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-4]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-3]) == "for" &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "the" &&
		cleanSpokenAddressToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-6]
	}
	if len(tokens) >= 7 &&
		cleanSpokenAddressToken(tokens[len(tokens)-7]) == "that" &&
		cleanSpokenAddressToken(tokens[len(tokens)-6]) == "ll" &&
		cleanSpokenAddressToken(tokens[len(tokens)-5]) == "be" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-4]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-4]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-3]) == "for" &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "the" &&
		cleanSpokenAddressToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-7]
	}
	if len(tokens) >= 5 &&
		(cleanSpokenAddressToken(tokens[len(tokens)-5]) == "that'll" || cleanSpokenAddressToken(tokens[len(tokens)-5]) == "thatll") &&
		cleanSpokenAddressToken(tokens[len(tokens)-4]) == "be" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-3]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-3]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" &&
		isSpokenAddressSignoffObject(cleanSpokenAddressToken(tokens[len(tokens)-1])) {
		return tokens[:len(tokens)-5]
	}
	if len(tokens) >= 6 &&
		cleanSpokenAddressToken(tokens[len(tokens)-6]) == "that" &&
		cleanSpokenAddressToken(tokens[len(tokens)-5]) == "ll" &&
		cleanSpokenAddressToken(tokens[len(tokens)-4]) == "be" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-3]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-3]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" &&
		isSpokenAddressSignoffObject(cleanSpokenAddressToken(tokens[len(tokens)-1])) {
		return tokens[:len(tokens)-6]
	}
	if len(tokens) >= 4 &&
		(cleanSpokenAddressToken(tokens[len(tokens)-4]) == "that's" || cleanSpokenAddressToken(tokens[len(tokens)-4]) == "thats") &&
		(cleanSpokenAddressToken(tokens[len(tokens)-3]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-3]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-1]) == "now" || cleanSpokenAddressToken(tokens[len(tokens)-1]) == "me" || cleanSpokenAddressToken(tokens[len(tokens)-1]) == "today") {
		return tokens[:len(tokens)-4]
	}
	if len(tokens) >= 5 &&
		cleanSpokenAddressToken(tokens[len(tokens)-5]) == "that" &&
		cleanSpokenAddressToken(tokens[len(tokens)-4]) == "is" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-3]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-3]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-1]) == "now" || cleanSpokenAddressToken(tokens[len(tokens)-1]) == "me" || cleanSpokenAddressToken(tokens[len(tokens)-1]) == "today") {
		return tokens[:len(tokens)-5]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" &&
		isSpokenAddressSignoffObject(cleanSpokenAddressToken(tokens[len(tokens)-1])) {
		return tokens[:len(tokens)-2]
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[len(tokens)-3]) == "for" &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "the" &&
		cleanSpokenAddressToken(tokens[len(tokens)-1]) == "day" {
		return tokens[:len(tokens)-3]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[len(tokens)-1]) == "it" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-2]) == "that's" || cleanSpokenAddressToken(tokens[len(tokens)-2]) == "thats") {
		return tokens[:len(tokens)-2]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[len(tokens)-1]) == "all" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-2]) == "that's" || cleanSpokenAddressToken(tokens[len(tokens)-2]) == "thats") {
		return tokens[:len(tokens)-2]
	}
	if len(tokens) >= 3 &&
		(cleanSpokenAddressToken(tokens[len(tokens)-1]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-1]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "is" &&
		cleanSpokenAddressToken(tokens[len(tokens)-3]) == "that" {
		return tokens[:len(tokens)-3]
	}
	if len(tokens) >= 3 &&
		(cleanSpokenAddressToken(tokens[len(tokens)-3]) == "that'll" || cleanSpokenAddressToken(tokens[len(tokens)-3]) == "thatll") &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "be" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-1]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-1]) == "all") {
		return tokens[:len(tokens)-3]
	}
	if len(tokens) >= 4 &&
		cleanSpokenAddressToken(tokens[len(tokens)-4]) == "that" &&
		cleanSpokenAddressToken(tokens[len(tokens)-3]) == "will" &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "be" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-1]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-1]) == "all") {
		return tokens[:len(tokens)-4]
	}
	if len(tokens) >= 4 &&
		cleanSpokenAddressToken(tokens[len(tokens)-4]) == "that" &&
		cleanSpokenAddressToken(tokens[len(tokens)-3]) == "ll" &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "be" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-1]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-1]) == "all") {
		return tokens[:len(tokens)-4]
	}
	return tokens
}

func trimTrailingSpokenAddressSignoffParts(tokens []string) []string {
	if len(tokens) >= 5 &&
		cleanSpokenAddressToken(tokens[len(tokens)-5]) == "that" &&
		cleanSpokenAddressToken(tokens[len(tokens)-4]) == "is" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-3]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-3]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" &&
		isSpokenAddressSignoffObject(cleanSpokenAddressToken(tokens[len(tokens)-1])) {
		return tokens[:len(tokens)-5]
	}
	if len(tokens) >= 5 &&
		cleanSpokenAddressToken(tokens[len(tokens)-5]) == "that" &&
		cleanSpokenAddressToken(tokens[len(tokens)-4]) == "s" &&
		(cleanSpokenAddressToken(tokens[len(tokens)-3]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-3]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" &&
		isSpokenAddressSignoffObject(cleanSpokenAddressToken(tokens[len(tokens)-1])) {
		return tokens[:len(tokens)-5]
	}
	if len(tokens) >= 4 &&
		(cleanSpokenAddressToken(tokens[len(tokens)-4]) == "that's" || cleanSpokenAddressToken(tokens[len(tokens)-4]) == "thats") &&
		(cleanSpokenAddressToken(tokens[len(tokens)-3]) == "it" || cleanSpokenAddressToken(tokens[len(tokens)-3]) == "all") &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" &&
		isSpokenAddressSignoffObject(cleanSpokenAddressToken(tokens[len(tokens)-1])) {
		return tokens[:len(tokens)-4]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[len(tokens)-2]) == "for" &&
		isSpokenAddressSignoffObject(cleanSpokenAddressToken(tokens[len(tokens)-1])) {
		return tokens[:len(tokens)-2]
	}
	return tokens
}

func isSpokenAddressSignoffObject(token string) bool {
	switch token {
	case "day", "me", "now", "today", "you":
		return true
	default:
		return false
	}
}

func stripSpokenAddressFieldLabels(tokens []string) []string {
	out := tokens[:0]
	for i := 0; i < len(tokens); i++ {
		if i+2 < len(tokens) &&
			isSpokenAddressPostalLabel(cleanSpokenAddressToken(tokens[i])) &&
			cleanSpokenAddressToken(tokens[i+1]) == "code" &&
			cleanSpokenAddressToken(tokens[i+2]) == "s" {
			i += 2
			continue
		}
		if i+1 < len(tokens) &&
			isSpokenAddressPostalLabel(cleanSpokenAddressToken(tokens[i])) &&
			cleanSpokenAddressToken(tokens[i+1]) == "code's" {
			i++
			continue
		}
		if i+2 < len(tokens) &&
			isSpokenAddressPostalLabel(cleanSpokenAddressToken(tokens[i])) &&
			cleanSpokenAddressToken(tokens[i+1]) == "code" &&
			cleanSpokenAddressToken(tokens[i+2]) == "is" {
			i += 2
			continue
		}
		if i+1 < len(tokens) &&
			isSpokenAddressPostalLabel(cleanSpokenAddressToken(tokens[i])) &&
			(cleanSpokenAddressToken(tokens[i+1]) == "is" || cleanSpokenAddressToken(tokens[i+1]) == "s") {
			i++
			continue
		}
		if cleanSpokenAddressToken(tokens[i]) == "state's" || cleanSpokenAddressToken(tokens[i]) == "province's" {
			continue
		}
		if i+1 < len(tokens) &&
			(cleanSpokenAddressToken(tokens[i]) == "state" || cleanSpokenAddressToken(tokens[i]) == "province") &&
			cleanSpokenAddressToken(tokens[i+1]) == "s" {
			i++
			continue
		}
		if i+1 < len(tokens) &&
			(cleanSpokenAddressToken(tokens[i]) == "state" || cleanSpokenAddressToken(tokens[i]) == "province") &&
			cleanSpokenAddressToken(tokens[i+1]) == "is" {
			i++
			continue
		}
		if i+2 < len(tokens) &&
			isSpokenAddressUnitType(cleanSpokenAddressToken(tokens[i])) &&
			cleanSpokenAddressToken(tokens[i+1]) == "number" &&
			cleanSpokenAddressToken(tokens[i+2]) == "is" {
			out = append(out, tokens[i])
			i += 2
			continue
		}
		if i+1 < len(tokens) &&
			isSpokenAddressUnitType(cleanSpokenAddressToken(tokens[i])) &&
			cleanSpokenAddressToken(tokens[i+1]) == "s" {
			out = append(out, tokens[i])
			i++
			continue
		}
		if i+1 < len(tokens) &&
			isSpokenAddressUnitType(cleanSpokenAddressToken(tokens[i])) &&
			cleanSpokenAddressToken(tokens[i+1]) == "is" {
			out = append(out, tokens[i])
			i++
			continue
		}
		if strings.HasSuffix(cleanSpokenAddressToken(tokens[i]), "'s") &&
			isSpokenAddressUnitType(strings.TrimSuffix(cleanSpokenAddressToken(tokens[i]), "'s")) {
			out = append(out, strings.TrimSuffix(tokens[i], "'s"))
			continue
		}
		out = append(out, tokens[i])
	}
	return out
}

func isSpokenAddressPostalLabel(token string) bool {
	switch token {
	case "post", "postal", "postcode", "zip":
		return true
	default:
		return false
	}
}

func isSpokenAddressUnitType(token string) bool {
	switch token {
	case "apartment", "apt", "floor", "office", "suite", "unit":
		return true
	default:
		return false
	}
}

func trimSpokenAddressPreamble(tokens []string) []string {
	if trimmed, ok := trimSpokenAddressWillBePreamble(tokens); ok {
		return trimmed
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[0]) == "my" &&
		cleanSpokenAddressToken(tokens[1]) == "address" &&
		cleanSpokenAddressToken(tokens[2]) == "is" {
		return tokens[3:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "my" &&
		cleanSpokenAddressToken(tokens[1]) == "address's" {
		return tokens[2:]
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[0]) == "my" &&
		cleanSpokenAddressToken(tokens[1]) == "address" &&
		cleanSpokenAddressToken(tokens[2]) == "s" {
		return tokens[3:]
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[0]) == "the" &&
		cleanSpokenAddressToken(tokens[1]) == "address" &&
		cleanSpokenAddressToken(tokens[2]) == "is" {
		return tokens[3:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "the" &&
		cleanSpokenAddressToken(tokens[1]) == "address's" {
		return tokens[2:]
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[0]) == "the" &&
		cleanSpokenAddressToken(tokens[1]) == "address" &&
		cleanSpokenAddressToken(tokens[2]) == "s" {
		return tokens[3:]
	}
	if len(tokens) >= 4 &&
		cleanSpokenAddressToken(tokens[0]) == "i" &&
		cleanSpokenAddressToken(tokens[1]) == "live" &&
		cleanSpokenAddressToken(tokens[2]) == "at" {
		return tokens[3:]
	}
	if len(tokens) >= 4 &&
		cleanSpokenAddressToken(tokens[0]) == "i" &&
		cleanSpokenAddressToken(tokens[1]) == "am" &&
		cleanSpokenAddressToken(tokens[2]) == "at" {
		return tokens[3:]
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[0]) == "i'm" &&
		cleanSpokenAddressToken(tokens[1]) == "at" {
		return tokens[2:]
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[0]) == "street" &&
		cleanSpokenAddressToken(tokens[1]) == "address" &&
		cleanSpokenAddressToken(tokens[2]) == "is" {
		return tokens[3:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "street" &&
		cleanSpokenAddressToken(tokens[1]) == "address's" {
		return tokens[2:]
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[0]) == "street" &&
		cleanSpokenAddressToken(tokens[1]) == "address" &&
		cleanSpokenAddressToken(tokens[2]) == "s" {
		return tokens[3:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "street" &&
		cleanSpokenAddressToken(tokens[1]) == "is" {
		return tokens[2:]
	}
	if len(tokens) >= 1 && cleanSpokenAddressToken(tokens[0]) == "street's" {
		return tokens[1:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "street" &&
		cleanSpokenAddressToken(tokens[1]) == "s" {
		return tokens[2:]
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[0]) == "unit" &&
		cleanSpokenAddressToken(tokens[1]) == "number" &&
		cleanSpokenAddressToken(tokens[2]) == "is" {
		return tokens[3:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "unit" &&
		cleanSpokenAddressToken(tokens[1]) == "number's" {
		return tokens[2:]
	}
	if len(tokens) >= 3 &&
		cleanSpokenAddressToken(tokens[0]) == "unit" &&
		cleanSpokenAddressToken(tokens[1]) == "number" &&
		cleanSpokenAddressToken(tokens[2]) == "s" {
		return tokens[3:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "city" &&
		cleanSpokenAddressToken(tokens[1]) == "is" {
		return tokens[2:]
	}
	if len(tokens) >= 1 && cleanSpokenAddressToken(tokens[0]) == "city's" {
		return tokens[1:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "city" &&
		cleanSpokenAddressToken(tokens[1]) == "s" {
		return tokens[2:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "country" &&
		cleanSpokenAddressToken(tokens[1]) == "is" {
		return tokens[2:]
	}
	if len(tokens) >= 1 && cleanSpokenAddressToken(tokens[0]) == "country's" {
		return tokens[1:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "country" &&
		cleanSpokenAddressToken(tokens[1]) == "s" {
		return tokens[2:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "address" &&
		cleanSpokenAddressToken(tokens[1]) == "is" {
		return tokens[2:]
	}
	if len(tokens) >= 1 && cleanSpokenAddressToken(tokens[0]) == "address's" {
		return tokens[1:]
	}
	if len(tokens) >= 2 &&
		cleanSpokenAddressToken(tokens[0]) == "address" &&
		cleanSpokenAddressToken(tokens[1]) == "s" {
		return tokens[2:]
	}
	return tokens
}

func trimSpokenAddressWillBePreamble(tokens []string) ([]string, bool) {
	prefixes := [][]string{
		{"my", "address", "will", "be"},
		{"the", "address", "will", "be"},
		{"address", "will", "be"},
		{"street", "address", "will", "be"},
		{"street", "will", "be"},
		{"unit", "number", "will", "be"},
		{"city", "will", "be"},
		{"country", "will", "be"},
	}
	for _, prefix := range prefixes {
		if len(tokens) < len(prefix) {
			continue
		}
		matched := true
		for i, want := range prefix {
			if cleanSpokenAddressToken(tokens[i]) != want {
				matched = false
				break
			}
		}
		if matched {
			return tokens[len(prefix):], true
		}
	}
	return tokens, false
}

func consumeSpokenAddressSpelling(tokens []string, start int) (string, int, bool) {
	var spelled strings.Builder
	repeat := 1
	for j := start; j < len(tokens); j++ {
		token := cleanSpokenAddressToken(tokens[j])
		if _, ok := spokenAddressFillers[token]; ok {
			continue
		}
		switch token {
		case "single":
			repeat = 1
			continue
		case "double":
			if next, ok := nextSpokenAddressNonFiller(tokens, j+1); ok {
				if next.token == "u" || next.token == "ewe" || next.token == "you" {
					spelled.WriteString("w")
					j = next.index
					repeat = 1
					continue
				}
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
		if letter, ok := spokenAddressLetterAlias(token); ok {
			token = letter
		}
		if !isSingleEmailLetter(token) {
			return spelled.String(), j, spelled.Len() > 0
		}
		for range repeat {
			spelled.WriteString(token)
		}
		repeat = 1
	}
	return spelled.String(), len(tokens), spelled.Len() > 0
}

func nextSpokenAddressNonFiller(tokens []string, start int) (struct {
	token string
	index int
}, bool) {
	for i := start; i < len(tokens); i++ {
		token := cleanSpokenAddressToken(tokens[i])
		if _, ok := spokenAddressFillers[token]; ok {
			continue
		}
		return struct {
			token string
			index int
		}{token: token, index: i}, true
	}
	return struct {
		token string
		index int
	}{}, false
}

func isSingleAddressLetter(token string) bool {
	return len(token) == 1 && ((token[0] >= 'a' && token[0] <= 'z') || (token[0] >= 'A' && token[0] <= 'Z'))
}

func addressTokenContainsDigit(token string) bool {
	for _, r := range token {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func addressTokenAllDigits(token string) bool {
	if token == "" {
		return false
	}
	for _, r := range token {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func addressTokenEndsWithLetter(token string) bool {
	if token == "" {
		return false
	}
	last := token[len(token)-1]
	return (last >= 'a' && last <= 'z') || (last >= 'A' && last <= 'Z')
}

func addressTokenStartsWithDigit(token string) bool {
	if token == "" {
		return false
	}
	return token[0] >= '0' && token[0] <= '9'
}

func spokenAddressLetterAlias(word string) (string, bool) {
	aliases := map[string]string{
		"ay":     "a",
		"aye":    "a",
		"bee":    "b",
		"be":     "b",
		"cee":    "c",
		"sea":    "c",
		"see":    "c",
		"dee":    "d",
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

func isSpokenAddressAlphanumericContext(out []string, pendingDigits bool, tokens []string, index int) bool {
	if pendingDigits {
		return true
	}
	if len(out) > 0 && addressTokenContainsDigit(out[len(out)-1]) {
		return true
	}
	if len(out) > 0 && isSpokenAddressUnitType(strings.ToLower(out[len(out)-1])) {
		return true
	}
	next := nextAddressNumberToken(tokens, index+1)
	if next < len(tokens) && isSpokenAddressNumberLikeToken(cleanSpokenAddressToken(tokens[next])) {
		return true
	}
	return false
}

func parseSpokenAddressNumber(tokens []string, start int) (string, int) {
	if start >= len(tokens) {
		return "", 0
	}
	first := cleanSpokenAddressToken(tokens[start])
	secondIndex := nextAddressNumberToken(tokens, start+1)
	if tensDigit, ok := spokenAddressTensDigit(first); ok && secondIndex < len(tokens) {
		second := cleanSpokenAddressToken(tokens[secondIndex])
		thirdIndex := nextAddressNumberToken(tokens, secondIndex+1)
		if thirdIndex < len(tokens) && isSpokenAddressZeroWord(second) && spokenAddressDigit(cleanSpokenAddressToken(tokens[thirdIndex])) != "" {
			return tensDigit + "00" + spokenAddressDigit(cleanSpokenAddressToken(tokens[thirdIndex])), thirdIndex - start + 1
		}
	}
	if secondIndex < len(tokens) {
		second := cleanSpokenAddressToken(tokens[secondIndex])
		if second == "hundred" {
			prefix := spokenAddressDigit(first)
			if prefix == "" {
				return "", 0
			}
			tailIndex := nextAddressNumberToken(tokens, secondIndex+1)
			if tail, consumed := parseSpokenAddressNumber(tokens, tailIndex); consumed > 0 {
				if len(tail) == 1 {
					tail = "0" + tail
				} else if tailIndex < len(tokens) {
					tailToken := cleanSpokenAddressToken(tokens[tailIndex])
					if spokenAddressDigit(tailToken) != "" && !isSpokenAddressZeroWord(tailToken) {
						tail = "0" + tail
					}
				}
				return prefix + tail, tailIndex - start + consumed
			}
			if tailIndex < len(tokens) {
				tailToken := cleanSpokenAddressToken(tokens[tailIndex])
				nextTailIndex := nextAddressNumberToken(tokens, tailIndex+1)
				if isSpokenAddressZeroWord(tailToken) && nextTailIndex < len(tokens) {
					if tail := spokenAddressDigit(cleanSpokenAddressToken(tokens[nextTailIndex])); tail != "" {
						return prefix + "0" + tail, nextTailIndex - start + 1
					}
				}
				if tail := spokenAddressDigit(cleanSpokenAddressToken(tokens[tailIndex])); tail != "" {
					tailDigits := tail
					consumedTail := tailIndex - start + 1
					for next := nextAddressNumberToken(tokens, tailIndex+1); next < len(tokens); next = nextAddressNumberToken(tokens, next+1) {
						digit := spokenAddressDigit(cleanSpokenAddressToken(tokens[next]))
						if digit == "" {
							break
						}
						tailDigits += digit
						consumedTail = next - start + 1
					}
					return prefix + "0" + tailDigits, consumedTail
				}
			}
			return prefix + "00", secondIndex - start + 1
		}
		if value, ok := parseSpokenExpirationNumber(first + " " + second); ok && value >= 10 && value < 100 {
			return fmt.Sprintf("%d", value), secondIndex - start + 1
		}
	}
	if value, ok := parseSpokenExpirationNumber(first); ok && value >= 10 && value < 100 {
		return fmt.Sprintf("%d", value), 1
	}
	if spokenAddressDigit(first) == "" {
		return "", 0
	}
	if tail, consumed := parseSpokenAddressNumber(tokens, secondIndex); consumed > 0 {
		if len(tail) == 1 {
			tail = "0" + tail
		}
		return spokenAddressDigit(first) + tail, secondIndex - start + consumed
	}
	return "", 0
}

func isSpokenAddressZeroWord(word string) bool {
	return word == "zero" || word == "oh" || word == "o" || word == "owe" || word == "aught" || word == "ought" || word == "naught" || word == "nought"
}

func nextAddressNumberToken(tokens []string, start int) int {
	for start < len(tokens) {
		token := cleanSpokenAddressToken(tokens[start])
		if _, ok := spokenAddressFillers[token]; !ok {
			break
		}
		start++
	}
	return start
}

func parseSpokenAddressOrdinal(tokens []string, start int) (string, int) {
	if start >= len(tokens) {
		return "", 0
	}
	first := cleanSpokenAddressToken(tokens[start])
	if ordinal, ok := spokenAddressOrdinals[first]; ok {
		return ordinal, 1
	}
	if start+1 >= len(tokens) {
		return "", 0
	}
	secondIndex := start + 1
	for secondIndex < len(tokens) {
		token := cleanSpokenAddressToken(tokens[secondIndex])
		if _, ok := spokenAddressFillers[token]; !ok {
			break
		}
		secondIndex++
	}
	if secondIndex >= len(tokens) {
		return "", 0
	}
	second := cleanSpokenAddressToken(tokens[secondIndex])
	if second == "hundred" {
		prefix := spokenAddressDigit(first)
		tailIndex := nextAddressNumberToken(tokens, secondIndex+1)
		if prefix != "" && tailIndex < len(tokens) {
			if tailOrdinal, consumed := parseSpokenAddressOrdinal(tokens, tailIndex); consumed > 0 {
				if tailValue, ok := parseAddressOrdinalValue(tailOrdinal); ok {
					prefixValue, _ := strconv.Atoi(prefix)
					return formatAddressOrdinal(prefixValue*100 + tailValue), tailIndex - start + consumed
				}
			}
		}
	}
	if ordinal, ok := spokenAddressOrdinals[first+" "+second]; ok {
		return ordinal, secondIndex - start + 1
	}
	return "", 0
}

func parseAddressOrdinalValue(ordinal string) (int, bool) {
	for _, suffix := range []string{"st", "nd", "rd", "th"} {
		if strings.HasSuffix(ordinal, suffix) {
			value, err := strconv.Atoi(strings.TrimSuffix(ordinal, suffix))
			return value, err == nil
		}
	}
	return 0, false
}

func formatAddressOrdinal(value int) string {
	suffix := "th"
	if value%100 < 11 || value%100 > 13 {
		switch value % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", value, suffix)
}

func spokenAddressDigit(word string) string {
	digits := map[string]string{
		"zero": "0", "oh": "0", "o": "0", "owe": "0", "aught": "0", "ought": "0", "naught": "0", "nought": "0", "one": "1", "won": "1", "two": "2", "to": "2", "too": "2", "three": "3", "tree": "3", "free": "3", "four": "4", "for": "4", "fore": "4",
		"five": "5", "six": "6", "seven": "7", "eight": "8", "ate": "8", "nine": "9", "niner": "9",
	}
	return digits[word]
}

func spokenAddressTensDigit(word string) (string, bool) {
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

func spokenAddressRepeatWord(word string) (int, bool) {
	switch word {
	case "double":
		return 2, true
	case "triple":
		return 3, true
	case "quadruple":
		return 4, true
	default:
		return 0, false
	}
}

func isSpokenAddressSymbolSuffix(word string) bool {
	return word == "sign" || word == "symbol" || word == "key" || word == "mark"
}

func isSpokenAddressNumberSignSuffix(word string) bool {
	return isSpokenAddressSymbolSuffix(word) || word == "key"
}

func isSpokenAddressNumberSignPrefix(word string) bool {
	return word == "number" || word == "hash" || word == "pound" || word == "octothorpe"
}

func isSpokenAddressBareNumberSignPrefix(word string) bool {
	return word == "hash" || word == "hashtag" || word == "pound" || word == "octothorpe"
}

func isSpokenAddressNumberLikeToken(word string) bool {
	if word == "" {
		return false
	}
	if addressTokenAllDigits(word) {
		return true
	}
	if spokenAddressDigit(word) != "" {
		return true
	}
	if _, ok := spokenAddressTensDigit(word); ok {
		return true
	}
	if _, ok := spokenAddressOrdinals[word]; ok {
		return true
	}
	if value, ok := parseSpokenExpirationNumber(word); ok && value >= 10 && value < 100 {
		return true
	}
	return false
}

var spokenAddressSymbols = map[string]string{
	"dash":       "-",
	"hyphen":     "-",
	"minus":      "-",
	"apostrophe": "'",
	"quote":      "'",
	"slash":      "/",
}

var spokenAddressOrdinals = map[string]string{
	"first": "1st", "second": "2nd", "third": "3rd", "fourth": "4th", "fifth": "5th",
	"sixth": "6th", "seventh": "7th", "eighth": "8th", "ninth": "9th", "tenth": "10th",
	"eleventh": "11th", "twelfth": "12th", "thirteenth": "13th", "fourteenth": "14th", "fifteenth": "15th",
	"sixteenth": "16th", "seventeenth": "17th", "eighteenth": "18th", "nineteenth": "19th",
	"twentieth": "20th", "twenty first": "21st", "twenty second": "22nd", "twenty third": "23rd",
	"twenty fourth": "24th", "twenty fifth": "25th", "twenty sixth": "26th", "twenty seventh": "27th",
	"twenty eighth": "28th", "twenty ninth": "29th", "thirtieth": "30th", "thirty first": "31st",
}

func cleanSpokenAddressToken(token string) string {
	return strings.Trim(strings.ToLower(token), ".,!?;:")
}

var spokenAddressFillers = map[string]struct{}{
	"actually": {},
	"and":      {},
	"like":     {},
	"spell":    {},
	"spelled":  {},
	"sorry":    {},
	"um":       {}, "uh": {}, "er": {}, "ah": {}, "hmm": {},
}

func (t *GetAddressTask) setConfirmAddressTool(address string) {
	tools := make([]llm.Tool, 0, len(t.Agent.Tools)+1)
	for _, tool := range t.Agent.Tools {
		if tool.ID() == "confirm_address" {
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, &confirmAddressTool{task: t, address: address})
	t.Agent.Tools = tools
}

type confirmAddressTool struct {
	task    *GetAddressTask
	address string
}

func (t *confirmAddressTool) ID() string   { return "confirm_address" }
func (t *confirmAddressTool) Name() string { return "confirm_address" }
func (t *confirmAddressTool) Description() string {
	return "Call after the user confirms the address is correct."
}
func (t *confirmAddressTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *confirmAddressTool) Execute(ctx context.Context, args string) (string, error) {
	if t.task.currentAddress == "" {
		return "", fmt.Errorf("error: no address was provided, update_address must be called before")
	}
	if t.address != t.task.currentAddress {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: addressStaleConfirmationPrompt(),
			})
		}
		return "", nil
	}

	t.task.addressConfirmed = true
	_ = t.task.Complete(&GetAddressResult{Address: t.address})
	return "", nil
}

func addressStaleConfirmationPrompt() string {
	return "The address has changed since confirmation was requested, ask the user to confirm the updated address."
}

func addressFailureTarget(ctx context.Context, fallback *GetAddressTask) *GetAddressTask {
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.Session == nil {
		return fallback
	}
	currentAgent, err := runCtx.Session.CurrentAgent()
	if err != nil {
		return fallback
	}
	if task, ok := currentAgent.(*GetAddressTask); ok {
		return task
	}
	return fallback
}

type declineAddressCaptureTool struct {
	task *GetAddressTask
}

func (t *declineAddressCaptureTool) ID() string   { return "decline_address_capture" }
func (t *declineAddressCaptureTool) Name() string { return "decline_address_capture" }
func (t *declineAddressCaptureTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
func (t *declineAddressCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide an address."
}
func (t *declineAddressCaptureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the user declined to provide the address"},
		},
		"required": []string{"reason"},
	}
}

func (t *declineAddressCaptureTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	_ = addressFailureTarget(ctx, t.task).Fail(llm.NewToolError(fmt.Sprintf("couldn't get the address: %s", params.Reason)))
	return "", nil
}
