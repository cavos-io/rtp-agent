package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

var phoneRegex = regexp.MustCompile(`^\+?[1-9]\d{6,14}$`)

type GetPhoneNumberResult struct {
	PhoneNumber string
}

type GetPhoneNumberOptions struct {
	ExtraInstructions      string
	RequireConfirmation    bool
	RequireConfirmationSet bool
}

type GetPhoneNumberTask struct {
	agent.AgentTask[*GetPhoneNumberResult]
	ExtraInstructions   string
	RequireConfirmation bool
	currentPhoneNumber  string
}

const phoneNumberConfirmationInstruction = "Call `confirm_phone_number` after the user confirmed the phone number is correct."

const PhoneNumberInstructions = "You are only a single step in a broader system, responsible solely for capturing a phone number.\n" +
	"Handle input as noisy voice transcription. Expect that users will say phone numbers aloud in grouped digits or with an optional leading plus.\n" +
	"Normalize common spoken patterns silently: convert spoken digits to numeric form, remove filler words, strip dashes, spaces, parentheses, and dots, and recognize plus at the start as the international prefix.\n" +
	"Call `update_phone_number` at the first opportunity whenever you form a new hypothesis about the phone number. (before asking any questions or providing any answers.)\n" +
	"Don't invent phone numbers, stick strictly to what the user said.\n" +
	phoneNumberConfirmationInstruction + "\n" +
	"If the number is unclear or invalid, or it takes too much back-and-forth, prompt for it in parts: first the area code, then the remaining digits.\n" +
	"Never repeat the phone number back to the user as a single block of digits. Read it back in groups.\n" +
	"Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.\n" +
	"Avoid verbosity by not sharing example phone numbers or formats unless prompted to do so. Do not deviate from the goal of collecting the user's phone number.\n" +
	"Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called."

func NewGetPhoneNumberTask(opts GetPhoneNumberOptions) *GetPhoneNumberTask {
	requireConfirmation := true
	if opts.RequireConfirmationSet {
		requireConfirmation = opts.RequireConfirmation
	}
	instructions := phoneNumberInstructions(requireConfirmation, opts.ExtraInstructions)
	t := &GetPhoneNumberTask{
		AgentTask:           *agent.NewAgentTask[*GetPhoneNumberResult](instructions),
		ExtraInstructions:   opts.ExtraInstructions,
		RequireConfirmation: requireConfirmation,
	}

	t.Agent.Tools = []llm.Tool{
		&updatePhoneNumberTool{task: t},
		&declinePhoneNumberCaptureTool{task: t},
	}

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

func (t *GetPhoneNumberTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), "Ask the user to provide their phone number.")
		}
	}
}

type updatePhoneNumberTool struct {
	task *GetPhoneNumberTask
}

func (t *updatePhoneNumberTool) ID() string   { return "update_phone_number" }
func (t *updatePhoneNumberTool) Name() string { return "update_phone_number" }
func (t *updatePhoneNumberTool) Description() string {
	return "Update the phone number provided by the user."
}
func (t *updatePhoneNumberTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"phone_number": map[string]any{"type": "string", "description": "The phone number provided by the user, digits only with optional leading plus"},
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
	if !t.task.RequireConfirmation {
		_ = t.task.Complete(&GetPhoneNumberResult{PhoneNumber: cleaned})
		return "Phone number captured and task completed.", nil
	}

	t.task.setConfirmPhoneNumberTool(cleaned)
	return fmt.Sprintf("The phone number has been updated to %s\nRead the number back to the user in groups.\nPrompt the user for confirmation, do not call `confirm_phone_number` directly", cleaned), nil
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
	return "Phone number confirmed.", nil
}

func phoneNumberStaleConfirmationPrompt() string {
	return "The phone number has changed since confirmation was requested, ask the user to confirm the updated number."
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
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the user declined"},
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

	_ = t.task.Fail(llm.NewToolError(fmt.Sprintf("couldn't get the phone number: %s", params.Reason)))
	return "Task failed.", nil
}

func normalizePhoneNumber(phoneNumber string) string {
	cleaned := strings.TrimSpace(phoneNumber)
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "-", "", "(", "", ")", "", ".", "")
	return replacer.Replace(cleaned)
}
