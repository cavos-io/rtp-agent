package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

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

type GetCardNumberOptions struct {
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ExtraInstructions      string
	ChatContext            *llm.ChatContext
}

type GetSecurityCodeOptions struct {
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ExtraInstructions      string
	ChatContext            *llm.ChatContext
}

type GetExpirationDateOptions struct {
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ExtraInstructions      string
	ChatContext            *llm.ChatContext
}

type GetCreditCardOptions struct {
	AgentOptions
	RequireConfirmation    bool
	RequireConfirmationSet bool
	ExtraInstructions      string
	ChatContext            *llm.ChatContext
	Tools                  []llm.Tool
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
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ExtraInstructions      string
	currentCardNumber      string
}

type GetSecurityCodeTask struct {
	agent.AgentTask[*GetSecurityCodeResult]
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ExtraInstructions      string
	currentSecurityCode    string
}

type GetExpirationDateTask struct {
	agent.AgentTask[*GetExpirationDateResult]
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ExtraInstructions      string
	currentExpirationDate  string
}

type GetCreditCardTask struct {
	agent.AgentTask[*GetCreditCardResult]
	RequireConfirmation    bool
	RequireConfirmationSet bool
	ExtraInstructions      string
}

const CardNumberInstructions = `You are a single step in a broader process of collecting credit card information.
You are solely responsible for collecting the credit card number.
Handle input as noisy voice transcription. Expect users to read the card number digit by digit.
Normalize spoken digits silently: 'four' → 4, 'zero' / 'oh' → 0.
Filter out filler words or hesitations.
If the user refuses to provide a credit card number, call decline_card_capture().
If the user wishes to start over the credit card collection process, call restart_card_collection().
Avoid listing out questions with bullet points or numbers, use a natural conversational tone.
Never repeat any sensitive information, such as the user's credit card number, back to the user.`

const cardNumberConfirmationInstructions = "Call `confirm_card_number` once the user has repeated their card number."

const cardNumberTextInstructions = `You are a single step in a broader process of collecting credit card information.
You are solely responsible for collecting the credit card number.
Handle input as typed text. Users may type the number with or without spaces or dashes (e.g. '4152 6374 8901 2345').
If the user refuses to provide a credit card number, call decline_card_capture().
If the user wishes to start over the credit card collection process, call restart_card_collection().
Avoid listing out questions with bullet points or numbers, use a natural conversational tone.
Never repeat any sensitive information, such as the user's credit card number, back to the user.`

const SecurityCodeInstructions = `You are a single step in a broader process of collecting credit card information.
You are solely responsible for collecting the user's card's security code.
Handle input as noisy voice transcription. Expect users to read the security code digit by digit.
Normalize spoken digits silently: 'four' → 4, 'zero' / 'oh' → 0.
Filter out filler words or hesitations.
If the user refuses to provide a code, call decline_card_capture().
If the user wishes to start over the card collection process, call restart_card_collection().
Avoid listing out questions with bullet points or numbers, use a natural conversational tone.
Never repeat any sensitive information, such as the user's security code, back to the user.`

const securityCodeConfirmationInstructions = "Call `confirm_security_code` once the user has repeated their security code."

const securityCodeTextInstructions = `You are a single step in a broader process of collecting credit card information.
You are solely responsible for collecting the user's card's security code.
Handle input as typed text. Users will type the security code directly.
If the user refuses to provide a code, call decline_card_capture().
If the user wishes to start over the card collection process, call restart_card_collection().
Avoid listing out questions with bullet points or numbers, use a natural conversational tone.
Never repeat any sensitive information, such as the user's security code, back to the user.`

const ExpirationDateInstructions = `You are a single step in a broader process of collecting credit card information.
You are solely responsible for collecting the user's card's expiration date.
Handle input as noisy voice transcription. Expect users to say the expiration date in formats like 'April twenty five', 'oh four twenty five', 'four slash twenty five', or 'April 2025'.
Normalize spoken months and digits silently.
Filter out filler words or hesitations.
If the user refuses to provide a date, call decline_card_capture().
If the user wishes to start over the card collection process, call restart_card_collection().
Avoid listing out questions with bullet points or numbers, use a natural conversational tone.
Never repeat any sensitive information, such as the user's expiration date, back to the user.`

const expirationDateConfirmationInstructions = "Call `confirm_expiration_date` once the user has repeated their expiration date."

const expirationDateTextInstructions = `You are a single step in a broader process of collecting credit card information.
You are solely responsible for collecting the user's card's expiration date.
Handle input as typed text. Expect users to type the expiration date in formats like '04/25', '04/2025', or 'April 2025'.
If the user refuses to provide a date, call decline_card_capture().
If the user wishes to start over the card collection process, call restart_card_collection().
Avoid listing out questions with bullet points or numbers, use a natural conversational tone.
Never repeat any sensitive information, such as the user's expiration date, back to the user.`

const CreditCardInstructions = "*none*"

const cardholderNameExtraInstructions = "You are collecting the name on the credit card (the cardholder). " +
	"When you ask the user to confirm a candidate name from earlier in the conversation, " +
	"anchor the question to the card or cardholder so the user knows which name you mean - " +
	"not just 'is it [name]?' in the abstract."

func NewGetCardNumberTask(requireConfirmation ...bool) *GetCardNumberTask {
	opts := GetCardNumberOptions{}
	if len(requireConfirmation) > 0 {
		opts.RequireConfirmation = requireConfirmation[0]
		opts.RequireConfirmationSet = true
	}
	return NewGetCardNumberTaskWithOptions(opts)
}

func NewGetCardNumberTaskWithOptions(opts GetCardNumberOptions) *GetCardNumberTask {
	confirmationRequired := defaultCardConfirmationOption(opts.RequireConfirmation, opts.RequireConfirmationSet)
	instructions := cardNumberInstructions(confirmationRequired, opts.ExtraInstructions)
	t := &GetCardNumberTask{
		AgentTask:              *agent.NewAgentTask[*GetCardNumberResult](instructions),
		RequireConfirmation:    confirmationRequired,
		RequireConfirmationSet: opts.RequireConfirmationSet,
		RequireExplicitAsk:     opts.RequireExplicitAsk,
		ExtraInstructions:      opts.ExtraInstructions,
	}
	if opts.ChatContext != nil {
		t.ChatCtx = opts.ChatContext.Copy()
	}
	t.InstructionVariants = llm.NewInstructions(
		instructions,
		cardNumberTextVariantInstructions(opts.RequireConfirmationSet && opts.RequireConfirmation, opts.ExtraInstructions),
	)

	t.Agent.Tools = []llm.Tool{
		&recordCardNumberTool{task: t},
		&declineCardCaptureTool{task: t},
		&restartCardCollectionTool{task: t},
	}

	return t
}

func NewGetSecurityCodeTask(requireConfirmation ...bool) *GetSecurityCodeTask {
	opts := GetSecurityCodeOptions{}
	if len(requireConfirmation) > 0 {
		opts.RequireConfirmation = requireConfirmation[0]
		opts.RequireConfirmationSet = true
	}
	return NewGetSecurityCodeTaskWithOptions(opts)
}

func NewGetSecurityCodeTaskWithOptions(opts GetSecurityCodeOptions) *GetSecurityCodeTask {
	confirmationRequired := defaultCardConfirmationOption(opts.RequireConfirmation, opts.RequireConfirmationSet)
	instructions := securityCodeInstructions(confirmationRequired, opts.ExtraInstructions)
	t := &GetSecurityCodeTask{
		AgentTask:              *agent.NewAgentTask[*GetSecurityCodeResult](instructions),
		RequireConfirmation:    confirmationRequired,
		RequireConfirmationSet: opts.RequireConfirmationSet,
		RequireExplicitAsk:     opts.RequireExplicitAsk,
		ExtraInstructions:      opts.ExtraInstructions,
	}
	if opts.ChatContext != nil {
		t.ChatCtx = opts.ChatContext.Copy()
	}
	t.InstructionVariants = llm.NewInstructions(
		instructions,
		securityCodeTextVariantInstructions(opts.RequireConfirmationSet && opts.RequireConfirmation, opts.ExtraInstructions),
	)

	t.Agent.Tools = []llm.Tool{
		&updateSecurityCodeTool{task: t},
		&declineCardCaptureTool{task: t},
		&restartCardCollectionTool{task: t},
	}

	return t
}

func NewGetExpirationDateTask(requireConfirmation ...bool) *GetExpirationDateTask {
	opts := GetExpirationDateOptions{}
	if len(requireConfirmation) > 0 {
		opts.RequireConfirmation = requireConfirmation[0]
		opts.RequireConfirmationSet = true
	}
	return NewGetExpirationDateTaskWithOptions(opts)
}

func NewGetExpirationDateTaskWithOptions(opts GetExpirationDateOptions) *GetExpirationDateTask {
	confirmationRequired := defaultCardConfirmationOption(opts.RequireConfirmation, opts.RequireConfirmationSet)
	instructions := expirationDateInstructions(confirmationRequired, opts.ExtraInstructions)
	t := &GetExpirationDateTask{
		AgentTask:              *agent.NewAgentTask[*GetExpirationDateResult](instructions),
		RequireConfirmation:    confirmationRequired,
		RequireConfirmationSet: opts.RequireConfirmationSet,
		RequireExplicitAsk:     opts.RequireExplicitAsk,
		ExtraInstructions:      opts.ExtraInstructions,
	}
	if opts.ChatContext != nil {
		t.ChatCtx = opts.ChatContext.Copy()
	}
	t.InstructionVariants = llm.NewInstructions(
		instructions,
		expirationDateTextVariantInstructions(opts.RequireConfirmationSet && opts.RequireConfirmation, opts.ExtraInstructions),
	)

	t.Agent.Tools = []llm.Tool{
		&updateExpirationDateTool{task: t},
		&declineCardCaptureTool{task: t},
		&restartCardCollectionTool{task: t},
	}

	return t
}

func NewGetCreditCardTask(requireConfirmation ...bool) *GetCreditCardTask {
	opts := GetCreditCardOptions{}
	if len(requireConfirmation) > 0 {
		opts.RequireConfirmation = requireConfirmation[0]
		opts.RequireConfirmationSet = true
	}
	return NewGetCreditCardTaskWithOptions(opts)
}

func NewGetCreditCardTaskWithOptions(opts GetCreditCardOptions) *GetCreditCardTask {
	t := &GetCreditCardTask{
		AgentTask:              *agent.NewAgentTask[*GetCreditCardResult](CreditCardInstructions),
		RequireConfirmation:    defaultCardConfirmationOption(opts.RequireConfirmation, opts.RequireConfirmationSet),
		RequireConfirmationSet: opts.RequireConfirmationSet,
		ExtraInstructions:      opts.ExtraInstructions,
	}
	if opts.ChatContext != nil {
		t.ChatCtx = opts.ChatContext.Copy()
	}
	applyAgentOptions(&t.Agent, opts.AgentOptions)
	t.Agent.Tools = append([]llm.Tool{}, opts.Tools...)
	return t
}

func defaultCardConfirmationOption(requireConfirmation bool, set bool) bool {
	if set {
		return requireConfirmation
	}
	return true
}

func cardConfirmationRequired(ctx context.Context, requireConfirmation bool, set bool) bool {
	if set {
		return requireConfirmation
	}
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.SpeechHandle == nil {
		return true
	}
	return runCtx.SpeechHandle.InputDetails.Modality == "audio"
}

func cardNumberInstructions(requireConfirmation bool, extraInstructions string) string {
	instructions := CardNumberInstructions
	if !requireConfirmation {
		return appendCardExtraInstructions(instructions, extraInstructions)
	}
	instructions += "\n" + cardNumberConfirmationInstructions
	return appendCardExtraInstructions(instructions, extraInstructions)
}

func cardNumberTextVariantInstructions(requireConfirmation bool, extraInstructions string) string {
	instructions := cardNumberTextInstructions
	if requireConfirmation {
		instructions += "\n" + cardNumberConfirmationInstructions
	}
	return appendCardExtraInstructions(instructions, extraInstructions)
}

func securityCodeInstructions(requireConfirmation bool, extraInstructions string) string {
	instructions := SecurityCodeInstructions
	if !requireConfirmation {
		return appendCardExtraInstructions(instructions, extraInstructions)
	}
	instructions += "\n" + securityCodeConfirmationInstructions
	return appendCardExtraInstructions(instructions, extraInstructions)
}

func securityCodeTextVariantInstructions(requireConfirmation bool, extraInstructions string) string {
	instructions := securityCodeTextInstructions
	if requireConfirmation {
		instructions += "\n" + securityCodeConfirmationInstructions
	}
	return appendCardExtraInstructions(instructions, extraInstructions)
}

func expirationDateInstructions(requireConfirmation bool, extraInstructions string) string {
	instructions := ExpirationDateInstructions
	if !requireConfirmation {
		return appendCardExtraInstructions(instructions, extraInstructions)
	}
	instructions += "\n" + expirationDateConfirmationInstructions
	return appendCardExtraInstructions(instructions, extraInstructions)
}

func expirationDateTextVariantInstructions(requireConfirmation bool, extraInstructions string) string {
	instructions := expirationDateTextInstructions
	if requireConfirmation {
		instructions += "\n" + expirationDateConfirmationInstructions
	}
	return appendCardExtraInstructions(instructions, extraInstructions)
}

func appendCardExtraInstructions(instructions string, extraInstructions string) string {
	if extra := strings.TrimSpace(extraInstructions); extra != "" {
		return instructions + "\n" + extra
	}
	return instructions
}

func (t *GetCardNumberTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: cardNumberOnEnterPrompt(),
			})
		}
	}
}

func cardNumberOnEnterPrompt() string {
	return "Get the user's credit card number. First scan the conversation - if a credit card number was already given (e.g. the user volunteered it before the task started), use it via update_card_number rather than re-asking. Only ask fresh when no credit card number is in the conversation yet."
}

func (t *GetSecurityCodeTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: securityCodeOnEnterPrompt(),
			})
		}
	}
}

func securityCodeOnEnterPrompt() string {
	return "Get the user's card security code. First scan the conversation - if a code was already given, use it via update_security_code rather than re-asking. Only ask fresh when no code is in the conversation yet."
}

func (t *GetExpirationDateTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: expirationDateOnEnterPrompt(),
			})
		}
	}
}

func expirationDateOnEnterPrompt() string {
	return "Get the user's card expiration date. First scan the conversation - if an expiration date was already given, use it via update_expiration_date rather than re-asking. Only ask fresh when no date is in the conversation yet."
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
	if t.ChatCtx != nil {
		group.ChatCtx = t.ChatCtx.Copy()
	}
	group.Add("card_number_task", "Collects the user's card number", func() agent.AgentInterface {
		return NewGetCardNumberTaskWithOptions(GetCardNumberOptions{
			RequireConfirmation:    t.RequireConfirmation,
			RequireConfirmationSet: t.RequireConfirmationSet,
			ExtraInstructions:      t.ExtraInstructions,
			ChatContext:            t.ChatCtx,
		})
	})
	group.Add("expiration_date_task", "Collects the card's expiration date", func() agent.AgentInterface {
		return NewGetExpirationDateTaskWithOptions(GetExpirationDateOptions{
			RequireConfirmation:    t.RequireConfirmation,
			RequireConfirmationSet: t.RequireConfirmationSet,
			ExtraInstructions:      t.ExtraInstructions,
			ChatContext:            t.ChatCtx,
		})
	})
	group.Add("security_code_task", "Collects the card's security code", func() agent.AgentInterface {
		return NewGetSecurityCodeTaskWithOptions(GetSecurityCodeOptions{
			RequireConfirmation:    t.RequireConfirmation,
			RequireConfirmationSet: t.RequireConfirmationSet,
			ExtraInstructions:      t.ExtraInstructions,
			ChatContext:            t.ChatCtx,
		})
	})
	group.Add("cardholder_name_task", "Collects the cardholder's full name", func() agent.AgentInterface {
		return NewGetNameTask(GetNameOptions{
			FirstName:              true,
			LastName:               true,
			ExtraInstructions:      cardholderNameExtraInstructionsWithExtra(t.ExtraInstructions),
			RequireConfirmation:    t.RequireConfirmation,
			RequireConfirmationSet: t.RequireConfirmationSet,
			RequireExplicitAsk:     true,
			ChatContext:            t.ChatCtx,
		})
	})
	return group
}

func cardholderNameExtraInstructionsWithExtra(extraInstructions string) string {
	if extra := strings.TrimSpace(extraInstructions); extra != "" {
		return extra + "\n\n" + cardholderNameExtraInstructions
	}
	return cardholderNameExtraInstructions
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

	cardholderName := strings.TrimSpace(strings.Join([]string{name.FirstName, name.LastName}, " "))
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

func (t *recordCardNumberTool) ID() string   { return "update_card_number" }
func (t *recordCardNumberTool) Name() string { return "update_card_number" }
func (t *recordCardNumberTool) ToolFlags() llm.ToolFlag {
	if t.task.RequireExplicitAsk {
		return llm.ToolFlagIgnoreOnEnter
	}
	return llm.ToolFlagNone
}
func (t *recordCardNumberTool) Description() string {
	return "Call to record the user's card number. Only call once the entire number has been given, do not call in increments."
}
func (t *recordCardNumberTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"card_number": map[string]any{"type": "string", "description": "The credit card number as a string with no dashes or spaces"},
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

	cardNumber := normalizeCardDigits(stripSpokenCardNumberLengthLabel(params.CardNumber))
	if len(cardNumber) < 13 || len(cardNumber) > 19 {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: invalidCardNumberLengthPrompt(),
			})
		}
		return "", nil
	}

	t.task.currentCardNumber = cardNumber
	if !cardConfirmationRequired(ctx, t.task.RequireConfirmation, t.task.RequireConfirmationSet) {
		if !validateCardNumberLuhn(cardNumber) {
			if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
				_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
					Instructions: invalidCardNumberPrompt(),
				})
			}
			return "", nil
		}
		t.task.completeCardNumber(cardNumber)
		return "", nil
	}

	t.task.setConfirmCardNumberTool(cardNumber)
	return "The card number has been updated.\nAsk them to repeat the number, do not repeat the number back to them.\n", nil
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
func (t *updateSecurityCodeTool) ToolFlags() llm.ToolFlag {
	if t.task.RequireExplicitAsk {
		return llm.ToolFlagIgnoreOnEnter
	}
	return llm.ToolFlagNone
}
func (t *updateSecurityCodeTool) Description() string {
	return "Call to update the card's security code."
}
func (t *updateSecurityCodeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"security_code": map[string]any{"type": "string", "description": "The card's security code (3-4 digits, may have leading zeros)."},
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

	securityCode := normalizeCardDigits(stripSpokenSecurityCodeLengthLabel(params.SecurityCode))
	if !validSecurityCode(securityCode) {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: invalidSecurityCodePrompt(),
			})
		}
		return "", nil
	}

	t.task.currentSecurityCode = securityCode
	if !cardConfirmationRequired(ctx, t.task.RequireConfirmation, t.task.RequireConfirmationSet) {
		t.task.completeSecurityCode(securityCode)
		return "", nil
	}

	t.task.setConfirmSecurityCodeTool(securityCode)
	return "The security code has been updated.\nDo not repeat the security code back to the user, ask them to repeat the code.\nCall `confirm_security_code` once the user confirms, do not call it preemptively.\n", nil
}

func invalidSecurityCodePrompt() string {
	return "The security code's length is invalid, ask the user to repeat or to provide a new card and start over."
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
func (t *updateExpirationDateTool) ToolFlags() llm.ToolFlag {
	if t.task.RequireExplicitAsk {
		return llm.ToolFlagIgnoreOnEnter
	}
	return llm.ToolFlagNone
}
func (t *updateExpirationDateTool) Description() string {
	return "Call to update the card's expiration date. Collect both the numerical month and year."
}
func (t *updateExpirationDateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expiration_month": map[string]any{"type": "integer", "description": "The numerical expiration month of the card, example: '04' for April"},
			"expiration_year":  map[string]any{"type": "integer", "description": "The numerical expiration year of the card shortened to the last two digits, for example, '35' for 2035"},
		},
		"required": []string{"expiration_month", "expiration_year"},
	}
}

func (t *updateExpirationDateTool) Execute(ctx context.Context, args string) (string, error) {
	month, year, err := parseExpirationDateArgs([]byte(args), "expiration_month", "expiration_year")
	if err != nil {
		return "", err
	}
	if month < 1 || month > 12 {
		t.task.promptInvalidExpirationDate(invalidExpirationMonthPrompt())
		return "", nil
	}
	if year < 0 || year > 99 {
		t.task.promptInvalidExpirationDate(invalidExpirationYearPrompt())
		return "", nil
	}
	if expirationDateExpired(month, year, time.Now()) {
		t.task.promptInvalidExpirationDate(expiredExpirationDatePrompt())
		return "", nil
	}

	expirationDate := formatExpirationDate(month, year)
	t.task.currentExpirationDate = expirationDate
	if !cardConfirmationRequired(ctx, t.task.RequireConfirmation, t.task.RequireConfirmationSet) {
		t.task.completeExpirationDate(expirationDate)
		return "", nil
	}

	t.task.setConfirmExpirationDateTool(month, year, expirationDate)
	return "The expiration date has been updated.\nDo not repeat the expiration date back to the user, ask them to repeat the expiration date.\nCall `confirm_expiration_date` once the user confirms, do not call it preemptively.\n", nil
}

func (t *GetExpirationDateTask) promptInvalidExpirationDate(prompt string) {
	if activity := t.Agent.GetActivity(); activity != nil && activity.Session != nil {
		_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
			Instructions: prompt,
		})
	}
}

func invalidExpirationMonthPrompt() string {
	return "The expiration month is invalid, ask the user to repeat the expiration month."
}

func invalidExpirationYearPrompt() string {
	return "The expiration year is invalid, ask the user to repeat the expiration year."
}

func expiredExpirationDatePrompt() string {
	return "The expiration date is in the past, the card is expired. Ask the user to provide another card."
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
	return "Call after the user repeats their card number for confirmation."
}
func (t *confirmCardNumberTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repeated_card_number": map[string]any{"type": "string", "description": "The card number repeated by the user as a string"},
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
	if t.cardNumber != t.task.currentCardNumber {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: cardNumberStaleConfirmationPrompt(),
			})
		}
		return "", nil
	}
	repeated := normalizeCardDigits(stripSpokenCardNumberLengthLabel(params.RepeatedCardNumber))
	if repeated != t.cardNumber {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: cardNumberMismatchPrompt(),
			})
		}
		return "", nil
	}
	if !validateCardNumberLuhn(t.cardNumber) {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: invalidCardNumberPrompt(),
			})
		}
		return "", nil
	}
	t.task.completeCardNumber(t.cardNumber)
	return "", nil
}

func cardNumberMismatchPrompt() string {
	return "The repeated card number does not match, ask the user to try again."
}

func cardNumberStaleConfirmationPrompt() string {
	return "The card number has changed since confirmation was requested, ask the user to confirm the updated number."
}

func invalidCardNumberPrompt() string {
	return "The card number is not valid, ask the user if they made a mistake or to provide another card."
}

func invalidCardNumberLengthPrompt() string {
	return "The length of the card number is invalid, ask the user to repeat their card number."
}

type confirmSecurityCodeTool struct {
	task         *GetSecurityCodeTask
	securityCode string
}

func (t *confirmSecurityCodeTool) ID() string   { return "confirm_security_code" }
func (t *confirmSecurityCodeTool) Name() string { return "confirm_security_code" }
func (t *confirmSecurityCodeTool) Description() string {
	return "Call after the user repeats their security code for confirmation."
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
	if t.securityCode != t.task.currentSecurityCode {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: securityCodeStaleConfirmationPrompt(),
			})
		}
		return "", nil
	}
	if normalizeCardDigits(stripSpokenSecurityCodeLengthLabel(params.RepeatedSecurityCode)) != t.securityCode {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: securityCodeMismatchPrompt(),
			})
		}
		return "", nil
	}
	t.task.completeSecurityCode(t.securityCode)
	return "", nil
}

func securityCodeMismatchPrompt() string {
	return "The repeated security code does not match, ask the user to try again."
}

func securityCodeStaleConfirmationPrompt() string {
	return "The security code has changed since confirmation was requested, ask the user to confirm the updated code."
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
	return "Call after the user repeats their expiration date for confirmation."
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
	month, year, err := parseExpirationDateArgs([]byte(args), "repeated_expiration_month", "repeated_expiration_year")
	if err != nil {
		return "", err
	}
	if t.expirationDate != t.task.currentExpirationDate {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: expirationDateStaleConfirmationPrompt(),
			})
		}
		return "", nil
	}
	if month != t.expirationMonth || year != t.expirationYear {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: expirationDateMismatchPrompt(),
			})
		}
		return "", nil
	}
	t.task.completeExpirationDate(t.expirationDate)
	return "", nil
}

func expirationDateMismatchPrompt() string {
	return "The repeated expiration date does not match, ask the user to try again."
}

func expirationDateStaleConfirmationPrompt() string {
	return "The expiration date has changed since confirmation was requested, ask the user to confirm the updated date."
}

func parseExpirationDateArgs(args []byte, monthKey string, yearKey string) (int, int, error) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(args, &params); err != nil {
		return 0, 0, err
	}
	month, err := parseExpirationNumber(params[monthKey], true)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", monthKey, err)
	}
	year, err := parseExpirationNumber(params[yearKey], false)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", yearKey, err)
	}
	return month, year, nil
}

func parseExpirationNumber(raw json.RawMessage, allowMonthName bool) (int, error) {
	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	text = normalizeSpokenExpirationText(text)
	if value, err := strconv.Atoi(text); err == nil {
		return value, nil
	}
	if allowMonthName {
		if value, ok := expirationMonthNames[text]; ok {
			return value, nil
		}
	}
	if !allowMonthName {
		if value, ok := parseSpokenExpirationYearDigits(text); ok {
			return value, nil
		}
	}
	if value, ok := parseSpokenExpirationNumber(text); ok {
		return value, nil
	}
	return 0, fmt.Errorf("invalid expiration number %q", text)
}

func parseSpokenExpirationYearDigits(text string) (int, bool) {
	tokens := strings.Fields(strings.ReplaceAll(text, "-", " "))
	if len(tokens) != 4 {
		return 0, false
	}
	value := 0
	for _, token := range tokens {
		digit, ok := spokenDOBDigit(token)
		if !ok {
			return 0, false
		}
		value = value*10 + digit
	}
	if value >= 2000 && value < 2100 {
		return value % 100, true
	}
	return 0, false
}

func normalizeSpokenExpirationText(text string) string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
	if len(parts) == 0 {
		return ""
	}
	parts = trimTrailingSpokenExpirationFiller(parts)
	filtered := parts[:0]
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		part = strings.Trim(part, ".,!?;:")
		if part == "" {
			continue
		}
		if isSpokenExpirationSplitContractedLabel(part) && i+1 < len(parts) && strings.Trim(parts[i+1], ".,!?;:") == "s" {
			i++
			continue
		}
		if _, ok := spokenExpirationFillers[part]; ok {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, " ")
}

func isSpokenExpirationSplitContractedLabel(part string) bool {
	switch part {
	case "card", "date", "expiration", "month", "year":
		return true
	default:
		return false
	}
}

func trimTrailingSpokenExpirationFiller(parts []string) []string {
	clean := func(part string) string {
		return strings.Trim(part, ".,!?;:")
	}
	if trimmed := trimTrailingSpokenExpirationSignoffParts(parts, clean); len(trimmed) != len(parts) {
		return trimmed
	}
	trailing := map[string]struct{}{
		"done": {}, "ok": {}, "okay": {}, "please": {}, "thanks": {}, "thank": {}, "you": {},
	}
	if len(parts) >= 2 &&
		clean(parts[len(parts)-1]) == "done" &&
		clean(parts[len(parts)-2]) == "all" {
		parts = parts[:len(parts)-2]
	}
	for len(parts) > 0 {
		last := clean(parts[len(parts)-1])
		if last == "you" && len(parts) >= 2 && clean(parts[len(parts)-2]) == "for" {
			break
		}
		if _, ok := trailing[last]; !ok {
			break
		}
		parts = parts[:len(parts)-1]
	}
	if len(parts) >= 5 &&
		(clean(parts[len(parts)-5]) == "that's" || clean(parts[len(parts)-5]) == "thats") &&
		(clean(parts[len(parts)-4]) == "it" || clean(parts[len(parts)-4]) == "all") &&
		clean(parts[len(parts)-3]) == "for" &&
		clean(parts[len(parts)-2]) == "the" &&
		clean(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 4 &&
		(clean(parts[len(parts)-4]) == "that's" || clean(parts[len(parts)-4]) == "thats") &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		clean(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-4]
	}
	if len(parts) >= 6 &&
		clean(parts[len(parts)-6]) == "that" &&
		clean(parts[len(parts)-5]) == "is" &&
		(clean(parts[len(parts)-4]) == "it" || clean(parts[len(parts)-4]) == "all") &&
		clean(parts[len(parts)-3]) == "for" &&
		clean(parts[len(parts)-2]) == "the" &&
		clean(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 5 &&
		clean(parts[len(parts)-5]) == "that" &&
		clean(parts[len(parts)-4]) == "is" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		clean(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 7 &&
		clean(parts[len(parts)-7]) == "that" &&
		clean(parts[len(parts)-6]) == "will" &&
		clean(parts[len(parts)-5]) == "be" &&
		(clean(parts[len(parts)-4]) == "it" || clean(parts[len(parts)-4]) == "all") &&
		clean(parts[len(parts)-3]) == "for" &&
		clean(parts[len(parts)-2]) == "the" &&
		clean(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-7]
	}
	if len(parts) >= 6 &&
		clean(parts[len(parts)-6]) == "that" &&
		clean(parts[len(parts)-5]) == "will" &&
		clean(parts[len(parts)-4]) == "be" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 6 &&
		(clean(parts[len(parts)-6]) == "that'll" || clean(parts[len(parts)-6]) == "thatll") &&
		clean(parts[len(parts)-5]) == "be" &&
		(clean(parts[len(parts)-4]) == "it" || clean(parts[len(parts)-4]) == "all") &&
		clean(parts[len(parts)-3]) == "for" &&
		clean(parts[len(parts)-2]) == "the" &&
		clean(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 7 &&
		clean(parts[len(parts)-7]) == "that" &&
		clean(parts[len(parts)-6]) == "ll" &&
		clean(parts[len(parts)-5]) == "be" &&
		(clean(parts[len(parts)-4]) == "it" || clean(parts[len(parts)-4]) == "all") &&
		clean(parts[len(parts)-3]) == "for" &&
		clean(parts[len(parts)-2]) == "the" &&
		clean(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-7]
	}
	if len(parts) >= 3 &&
		clean(parts[len(parts)-3]) == "for" &&
		clean(parts[len(parts)-2]) == "the" &&
		clean(parts[len(parts)-1]) == "day" {
		return parts[:len(parts)-3]
	}
	if len(parts) >= 5 &&
		(clean(parts[len(parts)-5]) == "that'll" || clean(parts[len(parts)-5]) == "thatll") &&
		clean(parts[len(parts)-4]) == "be" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 6 &&
		clean(parts[len(parts)-6]) == "that" &&
		clean(parts[len(parts)-5]) == "ll" &&
		clean(parts[len(parts)-4]) == "be" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 4 &&
		(clean(parts[len(parts)-4]) == "that's" || clean(parts[len(parts)-4]) == "thats") &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		(clean(parts[len(parts)-1]) == "now" || clean(parts[len(parts)-1]) == "me" || clean(parts[len(parts)-1]) == "today") {
		return parts[:len(parts)-4]
	}
	if len(parts) >= 5 &&
		(clean(parts[len(parts)-5]) == "that'll" || clean(parts[len(parts)-5]) == "thatll") &&
		clean(parts[len(parts)-4]) == "be" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		(clean(parts[len(parts)-1]) == "now" || clean(parts[len(parts)-1]) == "me" || clean(parts[len(parts)-1]) == "today") {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 6 &&
		clean(parts[len(parts)-6]) == "that" &&
		clean(parts[len(parts)-5]) == "will" &&
		clean(parts[len(parts)-4]) == "be" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		(clean(parts[len(parts)-1]) == "now" || clean(parts[len(parts)-1]) == "me" || clean(parts[len(parts)-1]) == "today") {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 5 &&
		clean(parts[len(parts)-5]) == "that" &&
		clean(parts[len(parts)-4]) == "is" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		(clean(parts[len(parts)-1]) == "now" || clean(parts[len(parts)-1]) == "me" || clean(parts[len(parts)-1]) == "today") {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 2 &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-2]
	}
	if len(parts) >= 2 &&
		clean(parts[len(parts)-1]) == "it" &&
		(clean(parts[len(parts)-2]) == "that's" || clean(parts[len(parts)-2]) == "thats") {
		return parts[:len(parts)-2]
	}
	if len(parts) >= 2 &&
		clean(parts[len(parts)-1]) == "all" &&
		(clean(parts[len(parts)-2]) == "that's" || clean(parts[len(parts)-2]) == "thats") {
		return parts[:len(parts)-2]
	}
	if len(parts) >= 3 &&
		(clean(parts[len(parts)-1]) == "it" || clean(parts[len(parts)-1]) == "all") &&
		clean(parts[len(parts)-2]) == "is" &&
		clean(parts[len(parts)-3]) == "that" {
		return parts[:len(parts)-3]
	}
	if len(parts) >= 3 &&
		(clean(parts[len(parts)-3]) == "that'll" || clean(parts[len(parts)-3]) == "thatll") &&
		clean(parts[len(parts)-2]) == "be" &&
		(clean(parts[len(parts)-1]) == "it" || clean(parts[len(parts)-1]) == "all") {
		return parts[:len(parts)-3]
	}
	if len(parts) >= 4 &&
		clean(parts[len(parts)-4]) == "that" &&
		clean(parts[len(parts)-3]) == "will" &&
		clean(parts[len(parts)-2]) == "be" &&
		(clean(parts[len(parts)-1]) == "it" || clean(parts[len(parts)-1]) == "all") {
		return parts[:len(parts)-4]
	}
	if len(parts) >= 4 &&
		clean(parts[len(parts)-4]) == "that" &&
		clean(parts[len(parts)-3]) == "ll" &&
		clean(parts[len(parts)-2]) == "be" &&
		(clean(parts[len(parts)-1]) == "it" || clean(parts[len(parts)-1]) == "all") {
		return parts[:len(parts)-4]
	}
	return parts
}

func trimTrailingSpokenExpirationSignoffParts(parts []string, clean func(string) string) []string {
	if len(parts) >= 5 &&
		clean(parts[len(parts)-5]) == "that" &&
		clean(parts[len(parts)-4]) == "is" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 5 &&
		clean(parts[len(parts)-5]) == "that" &&
		clean(parts[len(parts)-4]) == "s" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 4 &&
		(clean(parts[len(parts)-4]) == "that's" || clean(parts[len(parts)-4]) == "thats") &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-4]
	}
	if len(parts) >= 6 &&
		clean(parts[len(parts)-6]) == "that" &&
		clean(parts[len(parts)-5]) == "will" &&
		clean(parts[len(parts)-4]) == "be" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 5 &&
		(clean(parts[len(parts)-5]) == "that'll" || clean(parts[len(parts)-5]) == "thatll") &&
		clean(parts[len(parts)-4]) == "be" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-5]
	}
	if len(parts) >= 6 &&
		clean(parts[len(parts)-6]) == "that" &&
		clean(parts[len(parts)-5]) == "ll" &&
		clean(parts[len(parts)-4]) == "be" &&
		(clean(parts[len(parts)-3]) == "it" || clean(parts[len(parts)-3]) == "all") &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-6]
	}
	if len(parts) >= 2 &&
		clean(parts[len(parts)-2]) == "for" &&
		isCardDigitSignoffObject(clean(parts[len(parts)-1])) {
		return parts[:len(parts)-2]
	}
	return parts
}

var expirationMonthNames = map[string]int{
	"january": 1, "jan": 1,
	"february": 2, "feb": 2,
	"march": 3, "mar": 3,
	"april": 4, "apr": 4,
	"may":  5,
	"june": 6, "jun": 6,
	"july": 7, "jul": 7,
	"august": 8, "aug": 8,
	"september": 9, "sep": 9, "sept": 9,
	"october": 10, "oct": 10,
	"november": 11, "nov": 11,
	"december": 12, "dec": 12,
}

var spokenExpirationFillers = map[string]struct{}{
	"um": {}, "uh": {}, "ah": {}, "er": {}, "erm": {}, "like": {}, "actually": {}, "sorry": {},
	"slash": {},
	"card":  {},
	"date":  {}, "expiration": {}, "expire": {}, "expires": {}, "expiry": {},
	"month": {}, "year": {},
	"card's": {}, "date's": {}, "expiration's": {}, "month's": {}, "year's": {},
	"my": {}, "is": {}, "will": {}, "be": {},
	"the":     {},
	"through": {}, "thru": {}, "until": {}, "valid": {},
	"end": {}, "good": {}, "of": {}, "in": {}, "on": {},
}

func parseSpokenExpirationNumber(text string) (int, bool) {
	ones := map[string]int{
		"zero": 0, "oh": 0, "o": 0, "owe": 0, "aught": 0, "ought": 0, "naught": 0, "nought": 0,
		"one": 1, "won": 1, "two": 2, "to": 2, "too": 2, "three": 3, "tree": 3, "free": 3, "four": 4, "for": 4, "fore": 4, "five": 5,
		"six": 6, "sex": 6, "seven": 7, "eight": 8, "nine": 9, "niner": 9,
		"ate": 8, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
		"fourteen": 14, "fifteen": 15, "sixteen": 16,
		"seventeen": 17, "eighteen": 18, "nineteen": 19,
	}
	tens := map[string]int{"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90}
	rawTokens := strings.Fields(strings.ReplaceAll(text, "-", " "))
	tokens := rawTokens[:0]
	for _, token := range rawTokens {
		if token == "and" {
			continue
		}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		return 0, false
	}
	if len(tokens) == 1 {
		if value, ok := ones[tokens[0]]; ok {
			return value, true
		}
		if value, ok := tens[tokens[0]]; ok {
			return value, true
		}
	}
	if len(tokens) == 2 {
		if tokens[0] == "single" {
			if value, ok := ones[tokens[1]]; ok && value < 10 {
				return value, true
			}
		}
		if tokens[0] == "zero" || tokens[0] == "oh" || tokens[0] == "o" || tokens[0] == "owe" || tokens[0] == "aught" || tokens[0] == "ought" || tokens[0] == "naught" || tokens[0] == "nought" {
			if value, ok := ones[tokens[1]]; ok && value < 10 {
				return value, true
			}
		}
		if left, ok := ones[tokens[0]]; ok && left < 10 {
			if right, ok := ones[tokens[1]]; ok && right < 10 {
				return left*10 + right, true
			}
		}
		if value, ok := tens[tokens[0]]; ok {
			if onesValue, ok := ones[tokens[1]]; ok && onesValue < 10 {
				return value + onesValue, true
			}
		}
		if tokens[0] == "twenty" {
			if value, ok := tens[tokens[1]]; ok && value < 100 {
				return value, true
			}
		}
	}
	if len(tokens) == 3 && tokens[0] == "twenty" && tokens[1] == "twenty" {
		if onesValue, ok := ones[tokens[2]]; ok && onesValue < 10 {
			return 20 + onesValue, true
		}
	}
	if len(tokens) == 3 && tokens[0] == "twenty" {
		if tensValue, ok := tens[tokens[1]]; ok && tensValue < 100 {
			if onesValue, ok := ones[tokens[2]]; ok && onesValue < 10 {
				return tensValue + onesValue, true
			}
		}
	}
	if len(tokens) >= 3 && tokens[0] == "two" && tokens[1] == "thousand" {
		if value, ok := parseSpokenExpirationNumber(strings.Join(tokens[2:], " ")); ok && value < 100 {
			return value, true
		}
	}
	return 0, false
}

type cardFailureTask interface {
	Fail(error) error
}

func cardFailureTarget(ctx context.Context, fallback cardFailureTask) cardFailureTask {
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.Session == nil {
		return fallback
	}
	currentAgent, err := runCtx.Session.CurrentAgent()
	if err != nil {
		return fallback
	}
	switch task := currentAgent.(type) {
	case *GetCardNumberTask:
		return task
	case *GetSecurityCodeTask:
		return task
	case *GetExpirationDateTask:
		return task
	}
	return fallback
}

type declineCardCaptureTool struct {
	task cardFailureTask
}

func (t *declineCardCaptureTool) ID() string   { return "decline_card_capture" }
func (t *declineCardCaptureTool) Name() string { return "decline_card_capture" }
func (t *declineCardCaptureTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
func (t *declineCardCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide a detail for their card information."
}
func (t *declineCardCaptureTool) Parameters() map[string]any {
	return cardReasonSchema("A short explanation of why the user declined to provide card information")
}
func (t *declineCardCaptureTool) Execute(ctx context.Context, args string) (string, error) {
	reason, err := decodeCardReason(args)
	if err != nil {
		return "", err
	}
	_ = cardFailureTarget(ctx, t.task).Fail(&CardCaptureDeclinedError{Reason: reason})
	return "", nil
}

type restartCardCollectionTool struct {
	task cardFailureTask
}

func (t *restartCardCollectionTool) ID() string   { return "restart_card_collection" }
func (t *restartCardCollectionTool) Name() string { return "restart_card_collection" }
func (t *restartCardCollectionTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
func (t *restartCardCollectionTool) Description() string {
	return "Handles the case when the user wishes to start over the card information collection process and validate a new card."
}
func (t *restartCardCollectionTool) Parameters() map[string]any {
	return cardReasonSchema("A short explanation of why the user wishes to start over")
}
func (t *restartCardCollectionTool) Execute(ctx context.Context, args string) (string, error) {
	reason, err := decodeCardReason(args)
	if err != nil {
		return "", err
	}
	_ = cardFailureTarget(ctx, t.task).Fail(&CardCollectionRestartError{Reason: reason})
	return "", nil
}

func cardReasonSchema(reasonDescription string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": reasonDescription},
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
