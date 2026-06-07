package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

type GetSecurityCodeResult struct {
	SecurityCode string
}

type GetExpirationDateResult struct {
	Date string
}

type GetCreditCardResult struct {
	CardholderName string
	Issuer         string
	CardNumber     string
	SecurityCode   string
	ExpirationDate string
}

type CardCaptureDeclinedError struct {
	Reason string
}

func (e *CardCaptureDeclinedError) Error() string {
	return fmt.Sprintf("couldn't get the card details: %s", e.Reason)
}

type CardCollectionRestartError struct {
	Reason string
}

func (e *CardCollectionRestartError) Error() string {
	return fmt.Sprintf("starting over: %s", e.Reason)
}

type GetCardNumberTask struct {
	agent.AgentTask[*GetCardNumberResult]
	RequireConfirmation bool
	currentCardNumber   string
}

type GetSecurityCodeTask struct {
	agent.AgentTask[*GetSecurityCodeResult]
	RequireConfirmation bool
	currentSecurityCode string
}

type GetExpirationDateTask struct {
	agent.AgentTask[*GetExpirationDateResult]
	RequireConfirmation   bool
	currentExpirationDate string
}

type GetCreditCardTask struct {
	agent.AgentTask[*GetCreditCardResult]
	RequireConfirmation bool
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

const cardNumberConfirmationInstructions = "Call `confirm_card_number` once the user has repeated their card number."

const SecurityCodeInstructions = `You are a single step in a broader process of collecting credit card information.
You are solely responsible for collecting the user's card security code.
Handle input as noisy voice transcription. Expect users to read the security code digit by digit.
Normalize spoken digits silently: 'four' to 4, 'zero' or 'oh' to 0.
Filter out filler words or hesitations.
If the user refuses to provide a code, call decline_card_capture.
If the user wishes to start over the card collection process, call restart_card_collection.
Avoid listing out questions with bullet points or numbers, use a natural conversational tone.
Never repeat sensitive information, such as the user's security code, back to the user.`

const securityCodeConfirmationInstructions = "Call `confirm_security_code` once the user has repeated their security code."

const ExpirationDateInstructions = `You are a single step in a broader process of collecting credit card information.
You are solely responsible for collecting the user's card expiration date.
Handle input as noisy voice transcription. Expect formats like April twenty five, oh four twenty five, four slash twenty five, or April 2025.
Normalize spoken months and digits silently.
If the user refuses to provide a date, call decline_card_capture.
If the user wishes to start over the card collection process, call restart_card_collection.
Avoid listing out questions with bullet points or numbers, use a natural conversational tone.
Never repeat sensitive information, such as the user's expiration date, back to the user.`

const expirationDateConfirmationInstructions = "Call `confirm_expiration_date` once the user has repeated their expiration date."

const CreditCardInstructions = `Collect the user's credit card information by running the cardholder name, card number, security code, and expiration date subtasks.
Never repeat sensitive card details back to the user.`

func NewGetCardNumberTask(requireConfirmation ...bool) *GetCardNumberTask {
	confirmationRequired := defaultCardConfirmation(requireConfirmation)
	t := &GetCardNumberTask{
		AgentTask:           *agent.NewAgentTask[*GetCardNumberResult](cardNumberInstructions(confirmationRequired)),
		RequireConfirmation: confirmationRequired,
	}

	t.Agent.Tools = []llm.Tool{
		&recordCardNumberTool{task: t},
		&declineCardCaptureTool{task: t},
		&restartCardCollectionTool{task: t},
	}

	return t
}

func NewGetSecurityCodeTask(requireConfirmation ...bool) *GetSecurityCodeTask {
	confirmationRequired := defaultCardConfirmation(requireConfirmation)
	t := &GetSecurityCodeTask{
		AgentTask:           *agent.NewAgentTask[*GetSecurityCodeResult](securityCodeInstructions(confirmationRequired)),
		RequireConfirmation: confirmationRequired,
	}

	t.Agent.Tools = []llm.Tool{
		&updateSecurityCodeTool{task: t},
		&declineCardCaptureTool{task: t},
		&restartCardCollectionTool{task: t},
	}

	return t
}

func NewGetExpirationDateTask(requireConfirmation ...bool) *GetExpirationDateTask {
	confirmationRequired := defaultCardConfirmation(requireConfirmation)
	t := &GetExpirationDateTask{
		AgentTask:           *agent.NewAgentTask[*GetExpirationDateResult](expirationDateInstructions(confirmationRequired)),
		RequireConfirmation: confirmationRequired,
	}

	t.Agent.Tools = []llm.Tool{
		&updateExpirationDateTool{task: t},
		&declineCardCaptureTool{task: t},
		&restartCardCollectionTool{task: t},
	}

	return t
}

func NewGetCreditCardTask(requireConfirmation ...bool) *GetCreditCardTask {
	return &GetCreditCardTask{
		AgentTask:           *agent.NewAgentTask[*GetCreditCardResult](CreditCardInstructions),
		RequireConfirmation: defaultCardConfirmation(requireConfirmation),
	}
}

func defaultCardConfirmation(requireConfirmation []bool) bool {
	if len(requireConfirmation) > 0 {
		return requireConfirmation[0]
	}
	return true
}

func cardNumberInstructions(requireConfirmation bool) string {
	if !requireConfirmation {
		return CardNumberInstructions
	}
	return CardNumberInstructions + "\n" + cardNumberConfirmationInstructions
}

func securityCodeInstructions(requireConfirmation bool) string {
	if !requireConfirmation {
		return SecurityCodeInstructions
	}
	return SecurityCodeInstructions + "\n" + securityCodeConfirmationInstructions
}

func expirationDateInstructions(requireConfirmation bool) string {
	if !requireConfirmation {
		return ExpirationDateInstructions
	}
	return ExpirationDateInstructions + "\n" + expirationDateConfirmationInstructions
}

func (t *GetCardNumberTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), "Ask for the user's credit card number.")
		}
	}
}

func (t *GetSecurityCodeTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), "Collect the user's card security code.")
		}
	}
}

func (t *GetExpirationDateTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), "Collect the user's card expiration date.")
		}
	}
}

func (t *GetCreditCardTask) OnEnter() {
	go func() {
		t.runCreditCardCollection(context.Background(), t.buildTaskGroup, t.startCreditCardTaskGroup)
	}()
}

func (t *GetCreditCardTask) startCreditCardTaskGroup(group *TaskGroup) {
	if activity := t.Agent.GetActivity(); activity != nil && activity.Session != nil {
		groupActivity := group.Agent.Start(activity.Session, group)
		defer groupActivity.Stop()
	} else {
		group.OnEnter()
	}
}

func (t *GetCreditCardTask) runCreditCardCollection(ctx context.Context, buildGroup func() *TaskGroup, startGroup func(*TaskGroup)) {
	for !t.Done() {
		group := buildGroup()
		startGroup(group)

		result, err := group.Wait(ctx)
		if err != nil {
			var restart *CardCollectionRestartError
			if errors.As(err, &restart) {
				continue
			}
			_ = t.Fail(err)
			return
		}
		if err := t.completeCreditCardFromTaskResults(result.TaskResults); err != nil {
			_ = t.Fail(err)
		}
		return
	}
}

func (t *GetCreditCardTask) buildTaskGroup() *TaskGroup {
	group := NewTaskGroup(true, false)
	group.Add("cardholder_name_task", "Collects the cardholder's full name", func() agent.AgentInterface {
		return NewGetNameTask(GetNameOptions{
			FirstName:              true,
			LastName:               true,
			ExtraInstructions:      "This is in the context of credit card information collection, ask specifically for the full name listed on it.",
			RequireConfirmation:    t.RequireConfirmation,
			RequireConfirmationSet: true,
		})
	})
	group.Add("card_number_task", "Collects the user's card number", func() agent.AgentInterface {
		return NewGetCardNumberTask(t.RequireConfirmation)
	})
	group.Add("security_code_task", "Collects the card's security code", func() agent.AgentInterface {
		return NewGetSecurityCodeTask(t.RequireConfirmation)
	})
	group.Add("expiration_date_task", "Collects the card's expiration date", func() agent.AgentInterface {
		return NewGetExpirationDateTask(t.RequireConfirmation)
	})
	return group
}

func (t *GetCreditCardTask) completeCreditCardFromTaskResults(results map[string]any) error {
	name, ok := results["cardholder_name_task"].(*GetNameResult)
	if !ok || name == nil {
		return fmt.Errorf("cardholder_name_task result = %T, want *GetNameResult", results["cardholder_name_task"])
	}
	cardNumber, ok := results["card_number_task"].(*GetCardNumberResult)
	if !ok || cardNumber == nil {
		return fmt.Errorf("card_number_task result = %T, want *GetCardNumberResult", results["card_number_task"])
	}
	securityCode, ok := results["security_code_task"].(*GetSecurityCodeResult)
	if !ok || securityCode == nil {
		return fmt.Errorf("security_code_task result = %T, want *GetSecurityCodeResult", results["security_code_task"])
	}
	expirationDate, ok := results["expiration_date_task"].(*GetExpirationDateResult)
	if !ok || expirationDate == nil {
		return fmt.Errorf("expiration_date_task result = %T, want *GetExpirationDateResult", results["expiration_date_task"])
	}

	cardholderName := strings.TrimSpace(strings.Join([]string{name.FirstName, name.MiddleName, name.LastName}, " "))
	for strings.Contains(cardholderName, "  ") {
		cardholderName = strings.ReplaceAll(cardholderName, "  ", " ")
	}
	return t.Complete(&GetCreditCardResult{
		CardholderName: cardholderName,
		Issuer:         cardNumber.Issuer,
		CardNumber:     cardNumber.CardNumber,
		SecurityCode:   securityCode.SecurityCode,
		ExpirationDate: expirationDate.Date,
	})
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

func (t *GetSecurityCodeTask) completeSecurityCode(securityCode string) {
	t.Complete(&GetSecurityCodeResult{SecurityCode: securityCode})
}

func (t *GetExpirationDateTask) completeExpirationDate(expirationDate string) {
	t.Complete(&GetExpirationDateResult{Date: expirationDate})
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

	t.task.currentCardNumber = cardNumber
	if !t.task.RequireConfirmation {
		if !validateCardNumberLuhn(cardNumber) {
			return "", llm.NewToolError("The card number is not valid, ask the user if they made a mistake or to provide another card.")
		}
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

type updateSecurityCodeTool struct {
	task *GetSecurityCodeTask
}

func (t *updateSecurityCodeTool) ID() string   { return "update_security_code" }
func (t *updateSecurityCodeTool) Name() string { return "update_security_code" }
func (t *updateSecurityCodeTool) Description() string {
	return "Update the card security code."
}
func (t *updateSecurityCodeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"security_code": map[string]any{"type": "string", "description": "The card security code, 3-4 digits and possibly with leading zeroes"},
		},
		"required": []string{"security_code"},
	}
}

func (t *updateSecurityCodeTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		SecurityCode string `json:"security_code"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	securityCode := strings.TrimSpace(params.SecurityCode)
	if !validSecurityCode(securityCode) {
		return "", llm.NewToolError("The security code's length is invalid, ask the user to repeat or to provide a new card and start over.")
	}

	t.task.currentSecurityCode = securityCode
	if !t.task.RequireConfirmation {
		t.task.completeSecurityCode(securityCode)
		return "Security code captured and task completed.", nil
	}

	t.task.setConfirmSecurityCodeTool(securityCode)
	return "The security code has been updated.\nDo not repeat the security code back to the user, ask them to repeat themselves.\nCall `confirm_security_code` once the user confirms, do not call it preemptively.", nil
}

func (t *GetSecurityCodeTask) setConfirmSecurityCodeTool(securityCode string) {
	tools := make([]llm.Tool, 0, len(t.Agent.Tools)+1)
	for _, tool := range t.Agent.Tools {
		if tool.ID() == "confirm_security_code" {
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, &confirmSecurityCodeTool{task: t, securityCode: securityCode})
	t.Agent.Tools = tools
}

type updateExpirationDateTool struct {
	task *GetExpirationDateTask
}

func (t *updateExpirationDateTool) ID() string   { return "update_expiration_date" }
func (t *updateExpirationDateTool) Name() string { return "update_expiration_date" }
func (t *updateExpirationDateTool) Description() string {
	return "Update the card expiration date."
}
func (t *updateExpirationDateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expiration_month": map[string]any{"type": "integer", "description": "The numerical expiration month, for example 4 for April"},
			"expiration_year":  map[string]any{"type": "integer", "description": "The two-digit expiration year, for example 35 for 2035"},
		},
		"required": []string{"expiration_month", "expiration_year"},
	}
}

func (t *updateExpirationDateTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		ExpirationMonth int `json:"expiration_month"`
		ExpirationYear  int `json:"expiration_year"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	if params.ExpirationMonth < 1 || params.ExpirationMonth > 12 {
		return "", llm.NewToolError("The expiration month is invalid, ask the user to repeat the expiration month.")
	}
	if params.ExpirationYear < 0 || params.ExpirationYear > 99 {
		return "", llm.NewToolError("The expiration year is invalid, ask the user to repeat the expiration year.")
	}
	if expirationDateExpired(params.ExpirationMonth, params.ExpirationYear, time.Now()) {
		return "", llm.NewToolError("The expiration date is in the past, the card is expired. Ask the user to provide another card.")
	}

	expirationDate := formatExpirationDate(params.ExpirationMonth, params.ExpirationYear)
	t.task.currentExpirationDate = expirationDate
	if !t.task.RequireConfirmation {
		t.task.completeExpirationDate(expirationDate)
		return "Expiration date captured and task completed.", nil
	}

	t.task.setConfirmExpirationDateTool(params.ExpirationMonth, params.ExpirationYear, expirationDate)
	return "The expiration date has been updated.\nDo not repeat the expiration date back to the user, ask them to repeat themselves.\nCall `confirm_expiration_date` once the user confirms, do not call it preemptively.", nil
}

func (t *GetExpirationDateTask) setConfirmExpirationDateTool(month int, year int, expirationDate string) {
	tools := make([]llm.Tool, 0, len(t.Agent.Tools)+1)
	for _, tool := range t.Agent.Tools {
		if tool.ID() == "confirm_expiration_date" {
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, &confirmExpirationDateTool{
		task:            t,
		expirationMonth: month,
		expirationYear:  year,
		expirationDate:  expirationDate,
	})
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

type confirmSecurityCodeTool struct {
	task         *GetSecurityCodeTask
	securityCode string
}

func (t *confirmSecurityCodeTool) ID() string   { return "confirm_security_code" }
func (t *confirmSecurityCodeTool) Name() string { return "confirm_security_code" }
func (t *confirmSecurityCodeTool) Description() string {
	return "Confirm the security code after the user repeats it."
}
func (t *confirmSecurityCodeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repeated_security_code": map[string]any{"type": "string", "description": "The security code repeated by the user"},
		},
		"required": []string{"repeated_security_code"},
	}
}

func (t *confirmSecurityCodeTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		RepeatedSecurityCode string `json:"repeated_security_code"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	if strings.TrimSpace(params.RepeatedSecurityCode) != t.securityCode {
		return "", llm.NewToolError("The repeated security code does not match, ask the user to try again.")
	}
	t.task.completeSecurityCode(t.securityCode)
	return "Security code confirmed.", nil
}

type confirmExpirationDateTool struct {
	task            *GetExpirationDateTask
	expirationMonth int
	expirationYear  int
	expirationDate  string
}

func (t *confirmExpirationDateTool) ID() string   { return "confirm_expiration_date" }
func (t *confirmExpirationDateTool) Name() string { return "confirm_expiration_date" }
func (t *confirmExpirationDateTool) Description() string {
	return "Confirm the expiration date after the user repeats it."
}
func (t *confirmExpirationDateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repeated_expiration_month": map[string]any{"type": "integer", "description": "The expiration month repeated by the user"},
			"repeated_expiration_year":  map[string]any{"type": "integer", "description": "The expiration year repeated by the user"},
		},
		"required": []string{"repeated_expiration_month", "repeated_expiration_year"},
	}
}

func (t *confirmExpirationDateTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		RepeatedExpirationMonth int `json:"repeated_expiration_month"`
		RepeatedExpirationYear  int `json:"repeated_expiration_year"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	if params.RepeatedExpirationMonth != t.expirationMonth || params.RepeatedExpirationYear != t.expirationYear {
		return "", llm.NewToolError("The repeated expiration date does not match, ask the user to try again.")
	}
	t.task.completeExpirationDate(t.expirationDate)
	return "Expiration date confirmed.", nil
}

type cardFailureTask interface {
	Fail(error) error
}

type declineCardCaptureTool struct {
	task cardFailureTask
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
	_ = t.task.Fail(&CardCaptureDeclinedError{Reason: reason})
	return "Task failed.", nil
}

type restartCardCollectionTool struct {
	task cardFailureTask
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
	_ = t.task.Fail(&CardCollectionRestartError{Reason: reason})
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

func validSecurityCode(securityCode string) bool {
	if len(securityCode) < 3 || len(securityCode) > 4 {
		return false
	}
	for _, r := range securityCode {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func formatExpirationDate(month int, year int) string {
	return fmt.Sprintf("%02d/%02d", month, year)
}

func expirationDateExpired(month int, year int, today time.Time) bool {
	fullYear := 2000 + year
	return fullYear < today.Year() || (fullYear == today.Year() && month < int(today.Month()))
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
