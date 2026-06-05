package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

var cardIssuersLookup = map[byte]string{
	'3': "American Express",
	'4': "Visa",
	'5': "Mastercard",
	'6': "Discover",
}

type GetCardNumberResult struct {
	Issuer     string
	CardNumber string
}

type GetCardNumberTask struct {
	agent.AgentTask[*GetCardNumberResult]
	RequireConfirmation bool
	currentCardNumber   string
}

const CardNumberInstructions = `You are a single step in a broader process of collecting credit card information.
You are solely responsible for collecting the card number.
Handle input as noisy voice transcription. Expect users to read the card number digit by digit.
Normalize spoken digits silently: 'four' to 4, 'zero' or 'oh' to 0.
Filter out filler words or hesitations.
If the user refuses to provide a number, call decline_card_capture.
If the user wishes to start over the card collection process, call restart_card_collection.
Avoid listing out questions with bullet points or numbers, use a natural conversational tone.
Never repeat sensitive information, such as the user's card number, back to the user.`

func NewGetCardNumberTask(requireConfirmation bool) *GetCardNumberTask {
	t := &GetCardNumberTask{
		AgentTask:           *agent.NewAgentTask[*GetCardNumberResult](CardNumberInstructions),
		RequireConfirmation: requireConfirmation,
	}

	t.Agent.Tools = []llm.Tool{
		&recordCardNumberTool{task: t},
		&declineCardCaptureTool{task: t},
		&restartCardCollectionTool{task: t},
	}

	return t
}

func (t *GetCardNumberTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), "Ask for the user's credit card number.")
		}
	}
}

func (t *GetCardNumberTask) completeCardNumber(cardNumber string) {
	issuer := "Other"
	if cardNumber != "" {
		if known, ok := cardIssuersLookup[cardNumber[0]]; ok {
			issuer = known
		}
	}
	t.Complete(&GetCardNumberResult{Issuer: issuer, CardNumber: cardNumber})
}

type recordCardNumberTool struct {
	task *GetCardNumberTask
}

func (t *recordCardNumberTool) ID() string   { return "record_card_number" }
func (t *recordCardNumberTool) Name() string { return "record_card_number" }
func (t *recordCardNumberTool) Description() string {
	return "Record the user's credit card number once the entire number has been given."
}
func (t *recordCardNumberTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"card_number": map[string]any{"type": "string", "description": "The credit card number with no dashes or spaces"},
		},
		"required": []string{"card_number"},
	}
}

func (t *recordCardNumberTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		CardNumber string `json:"card_number"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	cardNumber := normalizeCardDigits(params.CardNumber)
	if len(cardNumber) < 13 || len(cardNumber) > 19 {
		return "", llm.NewToolError("The length of the card number is invalid, ask the user to repeat their card number.")
	}
	if !validateCardNumberLuhn(cardNumber) {
		return "", llm.NewToolError("The card number is not valid, ask the user if they made a mistake or to provide another card.")
	}

	t.task.currentCardNumber = cardNumber
	if !t.task.RequireConfirmation {
		t.task.completeCardNumber(cardNumber)
		return "Card number captured and task completed.", nil
	}

	t.task.setConfirmCardNumberTool(cardNumber)
	return "The card number has been updated.\nAsk them to repeat the number, do not repeat the number back to them.", nil
}

func (t *GetCardNumberTask) setConfirmCardNumberTool(cardNumber string) {
	tools := make([]llm.Tool, 0, len(t.Agent.Tools)+1)
	for _, tool := range t.Agent.Tools {
		if tool.ID() == "confirm_card_number" {
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, &confirmCardNumberTool{task: t, cardNumber: cardNumber})
	t.Agent.Tools = tools
}

type confirmCardNumberTool struct {
	task       *GetCardNumberTask
	cardNumber string
}

func (t *confirmCardNumberTool) ID() string   { return "confirm_card_number" }
func (t *confirmCardNumberTool) Name() string { return "confirm_card_number" }
func (t *confirmCardNumberTool) Description() string {
	return "Confirm the card number after the user repeats it."
}
func (t *confirmCardNumberTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repeated_card_number": map[string]any{"type": "string", "description": "The card number repeated by the user"},
		},
		"required": []string{"repeated_card_number"},
	}
}

func (t *confirmCardNumberTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		RepeatedCardNumber string `json:"repeated_card_number"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	repeated := normalizeCardDigits(params.RepeatedCardNumber)
	if repeated != t.cardNumber {
		return "", llm.NewToolError("The repeated card number does not match, ask the user to try again.")
	}
	if !validateCardNumberLuhn(t.cardNumber) {
		return "", llm.NewToolError("The card number is not valid, ask the user if they made a mistake or to provide another card.")
	}
	t.task.completeCardNumber(t.cardNumber)
	return "Card number confirmed.", nil
}

type declineCardCaptureTool struct {
	task *GetCardNumberTask
}

func (t *declineCardCaptureTool) ID() string   { return "decline_card_capture" }
func (t *declineCardCaptureTool) Name() string { return "decline_card_capture" }
func (t *declineCardCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide card information."
}
func (t *declineCardCaptureTool) Parameters() map[string]any {
	return cardReasonSchema()
}
func (t *declineCardCaptureTool) Execute(ctx context.Context, args string) (string, error) {
	reason, err := decodeCardReason(args)
	if err != nil {
		return "", err
	}
	t.task.Fail(fmt.Errorf("couldn't get the card details: %s", reason))
	return "Task failed.", nil
}

type restartCardCollectionTool struct {
	task *GetCardNumberTask
}

func (t *restartCardCollectionTool) ID() string   { return "restart_card_collection" }
func (t *restartCardCollectionTool) Name() string { return "restart_card_collection" }
func (t *restartCardCollectionTool) Description() string {
	return "Handles the case when the user wants to restart card information collection."
}
func (t *restartCardCollectionTool) Parameters() map[string]any {
	return cardReasonSchema()
}
func (t *restartCardCollectionTool) Execute(ctx context.Context, args string) (string, error) {
	reason, err := decodeCardReason(args)
	if err != nil {
		return "", err
	}
	t.task.Fail(fmt.Errorf("starting over: %s", reason))
	return "Task failed.", nil
}

func cardReasonSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation"},
		},
		"required": []string{"reason"},
	}
}

func decodeCardReason(args string) (string, error) {
	var params struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	return params.Reason, nil
}

func normalizeCardDigits(cardNumber string) string {
	var b strings.Builder
	for _, r := range cardNumber {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func validateCardNumberLuhn(cardNumber string) bool {
	if cardNumber == "" {
		return false
	}
	sum := 0
	double := false
	for i := len(cardNumber) - 1; i >= 0; i-- {
		digit := cardNumber[i]
		if digit < '0' || digit > '9' {
			return false
		}
		n := int(digit - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return sum%10 == 0
}
