package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GetNameResult struct {
	FirstName  string
	MiddleName string
	LastName   string
}

type GetNameOptions struct {
	FirstName              bool
	MiddleName             bool
	LastName               bool
	VerifySpelling         bool
	ExtraInstructions      string
	RequireConfirmation    bool
	RequireConfirmationSet bool
}

type GetNameTask struct {
	agent.AgentTask[*GetNameResult]
	RequireConfirmation bool
	CollectFirstName    bool
	CollectMiddleName   bool
	CollectLastName     bool
	VerifySpelling      bool
	nameFormat          string
	firstName           string
	middleName          string
	lastName            string
}

const nameConfirmationInstruction = "Call `confirm_name` after the user confirmed the name is correct."

const nameInstructionsBeforeConfirmation = `You are only a single step in a broader system, responsible solely for capturing the user's name.
Handle input as noisy voice transcription. Expect users to say names aloud, possibly followed by spelling.
Normalize common spoken patterns silently, preserve special characters such as hyphens and apostrophes, and capitalize name parts appropriately.
Call ` + "`update_name`" + ` at the first opportunity whenever you form a new hypothesis about the name. (before asking any questions or providing any answers.)
Don't invent names, stick strictly to what the user said.
`

const nameInstructionsAfterConfirmation = `If the user explicitly declines to provide their name, call decline_name_capture.
Ignore unrelated input and avoid going off-topic.`

const NameInstructions = nameInstructionsBeforeConfirmation + nameConfirmationInstruction + "\n" + nameInstructionsAfterConfirmation

const nameInstructionsWithoutConfirmation = nameInstructionsBeforeConfirmation + nameInstructionsAfterConfirmation

func NewGetNameTask(opts GetNameOptions) *GetNameTask {
	if !opts.FirstName && !opts.MiddleName && !opts.LastName {
		opts.FirstName = true
	}
	requireConfirmation := true
	if opts.RequireConfirmationSet {
		requireConfirmation = opts.RequireConfirmation
	}
	instructions := nameInstructions(requireConfirmation)
	if opts.VerifySpelling {
		instructions += "\nAfter receiving the name, always verify the spelling by asking the user to confirm or spell out the name letter by letter."
	}
	if strings.TrimSpace(opts.ExtraInstructions) != "" {
		instructions += "\n" + strings.TrimSpace(opts.ExtraInstructions)
	}
	t := &GetNameTask{
		AgentTask:           *agent.NewAgentTask[*GetNameResult](instructions),
		RequireConfirmation: requireConfirmation,
		CollectFirstName:    opts.FirstName,
		CollectMiddleName:   opts.MiddleName,
		CollectLastName:     opts.LastName,
		VerifySpelling:      opts.VerifySpelling,
	}
	t.nameFormat = t.buildNameFormat()
	t.Agent.Tools = []llm.Tool{
		&updateNameTool{task: t},
		&declineNameCaptureTool{task: t},
	}
	return t
}

func nameInstructions(requireConfirmation bool) string {
	if !requireConfirmation {
		return nameInstructionsWithoutConfirmation
	}
	return NameInstructions
}

func (t *GetNameTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), fmt.Sprintf("Ask the user for their name, follow this order %q but do not mention the format.", t.nameFormat))
		}
	}
}

func (t *GetNameTask) buildNameFormat() string {
	parts := make([]string, 0, 3)
	if t.CollectFirstName {
		parts = append(parts, "{first_name}")
	}
	if t.CollectMiddleName {
		parts = append(parts, "{middle_name}")
	}
	if t.CollectLastName {
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
	parts := make([]string, 0, 3)
	if t.firstName != "" {
		parts = append(parts, t.firstName)
	}
	if t.middleName != "" {
		parts = append(parts, t.middleName)
	}
	if t.lastName != "" {
		parts = append(parts, t.lastName)
	}
	return strings.Join(parts, " ")
}

type updateNameTool struct {
	task *GetNameTask
}

func (t *updateNameTool) ID() string   { return "update_name" }
func (t *updateNameTool) Name() string { return "update_name" }
func (t *updateNameTool) Description() string {
	return "Update the name provided by the user."
}
func (t *updateNameTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"first_name":  map[string]any{"type": "string", "description": "The user's first name"},
			"middle_name": map[string]any{"type": "string", "description": "The user's middle name, if collected"},
			"last_name":   map[string]any{"type": "string", "description": "The user's last name, if collected"},
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

	firstName := strings.TrimSpace(params.FirstName)
	middleName := strings.TrimSpace(params.MiddleName)
	lastName := strings.TrimSpace(params.LastName)
	if err := t.task.validateNameParts(firstName, middleName, lastName); err != nil {
		return "", err
	}

	t.task.firstName = firstName
	t.task.middleName = middleName
	t.task.lastName = lastName
	if !t.task.RequireConfirmation {
		t.task.completeName()
		return "Name captured and task completed.", nil
	}

	t.task.setConfirmNameTool(firstName, middleName, lastName)
	fullName := t.task.fullName()
	if t.task.VerifySpelling {
		return fmt.Sprintf("The name has been updated to %s\nSpell out the name letter by letter for verification: %s\nPrompt the user for confirmation, do not call `confirm_name` directly", fullName, fullName), nil
	}
	return fmt.Sprintf("The name has been updated to %s\nRepeat the name back to the user and prompt for confirmation, do not call `confirm_name` directly", fullName), nil
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
	if len(errors) > 0 {
		return llm.NewToolError("Incomplete name: " + strings.Join(errors, "; "))
	}
	return nil
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
	return "Confirm the name after the user says it is correct."
}
func (t *confirmNameTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *confirmNameTool) Execute(ctx context.Context, args string) (string, error) {
	if t.task.firstName != t.firstName || t.task.middleName != t.middleName || t.task.lastName != t.lastName {
		return "", llm.NewToolError("The name has changed since confirmation was requested, ask the user to confirm the updated name.")
	}
	t.task.completeName()
	return "Name confirmed.", nil
}

type declineNameCaptureTool struct {
	task *GetNameTask
}

func (t *declineNameCaptureTool) ID() string   { return "decline_name_capture" }
func (t *declineNameCaptureTool) Name() string { return "decline_name_capture" }
func (t *declineNameCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide their name."
}
func (t *declineNameCaptureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the user declined"},
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
	_ = t.task.Fail(fmt.Errorf("couldn't get the name: %s", params.Reason))
	return "Task failed.", nil
}
