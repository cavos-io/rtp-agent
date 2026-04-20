package workflows

import (
	"context"
	"fmt"
	"regexp"

	"github.com/cavos-io/rtp-agent/core/agent"
)

var emailRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._%+\-]*@(?:[A-Za-z0-9](?:[A-Za-z0-9\-]*[A-Za-z0-9])?\.)+[A-Za-z]{2,}$`)

type GetEmailResult struct {
	Email string
}

type GetEmailTask struct {
	agent.AgentTask[*GetEmailResult]
	RequireConfirmation bool
	currentEmail        string
	emailConfirmed      bool
}

const EmailInstructions = `You are only a single step in a broader system, responsible solely for capturing an email address.
Handle input as noisy voice transcription. Expect that users will say emails aloud with formats like:
- 'john dot doe at gmail dot com'
- 'susan underscore smith at yahoo dot co dot uk'
- 'dave dash b at protonmail dot com'
- 'jane at example' (partial—prompt for the domain)
- 'theo t h e o at livekit dot io' (name followed by spelling)
Normalize common spoken patterns silently:
- Convert words like 'dot', 'underscore', 'dash', 'plus' into symbols: ., _, -, +.
- Convert 'at' to @.
- Recognize patterns where users speak their name or a word, followed by spelling: e.g., 'john j o h n'.
- Filter out filler words or hesitations.
- Assume some spelling if contextually obvious (e.g. 'mike b two two' → mikeb22).
Don't mention corrections. Treat inputs as possibly imperfect but fix them silently.
Call update_email_address at the first opportunity whenever you form a new hypothesis about the email. (before asking any questions or providing any answers.)
Don't invent new email addresses, stick strictly to what the user said.
Call confirm_email_address after the user confirmed the email address is correct.
If the email is unclear or invalid, or it takes too much back-and-forth, prompt for it in parts: first the part before the '@', then the domain—only if needed.
Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.
Always explicitly invoke a tool when applicable. Do not hallucinate tool usage, no real action is taken unless the tool is explicitly called.`

func NewGetEmailTask(requireConfirmation bool) *GetEmailTask {
	t := &GetEmailTask{
		AgentTask:           *agent.NewAgentTask[*GetEmailResult](EmailInstructions),
		RequireConfirmation: requireConfirmation,
	}
	t.Agent.Tools = []interface{}{
		&updateEmailTool{task: t},
		&confirmEmailTool{task: t},
		&declineEmailCaptureTool{task: t},
	}

	return t
}

func (t *GetEmailTask) OnEnter(ctx context.Context) error {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), "Please tell me your email address.", true)
		}
	}
	return nil
}

type updateEmailTool struct {
	task *GetEmailTask
}

func (t *updateEmailTool) ID() string   { return "update_email_address" }
func (t *updateEmailTool) Name() string { return "update_email_address" }
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

type updateEmailArgs struct {
	Email string `json:"email"`
}

func (t *updateEmailTool) Args() any {
	return &updateEmailArgs{}
}

func (t *updateEmailTool) Execute(ctx context.Context, args any) (any, error) {
	var email string
	if typed, ok := args.(*updateEmailArgs); ok {
		email = typed.Email
	} else {
		m, _ := args.(map[string]any)
		if v, ok := m["email"]; ok {
			email, _ = v.(string)
		}
	}

	if !emailRegex.MatchString(email) {
		return "", fmt.Errorf("invalid email address provided: %s", email)
	}

	t.task.currentEmail = email

	if !t.task.RequireConfirmation {
		t.task.Complete(&GetEmailResult{Email: email})
		return "Email captured and task completed.", nil
	}

	return fmt.Sprintf("The email has been updated to %s\nPrompt the user for confirmation, do not call `confirm_email_address` directly", email), nil
}

type confirmEmailTool struct {
	task *GetEmailTask
}

func (t *confirmEmailTool) ID() string   { return "confirm_email_address" }
func (t *confirmEmailTool) Name() string { return "confirm_email_address" }
func (t *confirmEmailTool) Description() string {
	return "Validates/confirms the email address provided by the user."
}
func (t *confirmEmailTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *confirmEmailTool) Execute(ctx context.Context, args any) (any, error) {
	if t.task.currentEmail == "" {
		return "", fmt.Errorf("error: no email address was provided, update_email_address must be called before")
	}

	t.task.emailConfirmed = true
	t.task.Complete(&GetEmailResult{Email: t.task.currentEmail})
	return "Email address confirmed.", nil
}

type declineEmailCaptureTool struct {
	task *GetEmailTask
}

func (t *declineEmailCaptureTool) ID() string   { return "decline_email_capture" }
func (t *declineEmailCaptureTool) Name() string { return "decline_email_capture" }
func (t *declineEmailCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide an email address."
}
func (t *declineEmailCaptureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the user declined"},
		},
		"required": []string{"reason"},
	}
}

func (t *declineEmailCaptureTool) Execute(ctx context.Context, args any) (any, error) {
	m, _ := args.(map[string]any)
	reason, _ := m["reason"].(string)

	t.task.Fail(fmt.Errorf("couldn't get the email address: %s", reason))
	return "Task failed.", nil
}

