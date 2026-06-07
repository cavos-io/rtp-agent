package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GetDOBResult struct {
	DateOfBirth time.Time
	TimeOfBirth *time.Time
}

type GetDOBOptions struct {
	ExtraInstructions      string
	IncludeTime            bool
	RequireConfirmation    bool
	RequireConfirmationSet bool
}

type GetDOBTask struct {
	agent.AgentTask[*GetDOBResult]
	ExtraInstructions   string
	IncludeTime         bool
	RequireConfirmation bool
	currentDOB          *time.Time
	currentTime         *time.Time
}

const dobConfirmationInstruction = "Call `confirm_dob` after the user confirmed the date of birth is correct."

const dobInstructionsBeforeConfirmation = `You are only a single step in a broader system, responsible solely for capturing a date of birth.
Handle input as noisy voice transcription. Expect users to say dates aloud in formats like January 15th 1990, one fifteen ninety, Jan 15 90, or 15th January 1990.
Normalize common spoken patterns silently: convert spoken numbers and ordinals to numeric form, recognize month names, handle two-digit years appropriately, and filter filler words.
%sCall ` + "`update_dob`" + ` at the first opportunity whenever you form a new hypothesis about the date of birth. (before asking any questions or providing any answers.)
Don't invent dates, stick strictly to what the user said.
`

const dobInstructionsAfterConfirmation = `When reading back dates, use a natural spoken format like January fifteenth, nineteen ninety.
If the date is unclear or invalid, or it takes too much back-and-forth, prompt for it in parts: first the month, then the day, then the year.
Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.
Avoid verbosity by not sharing example dates or formats unless prompted to do so. Do not deviate from the goal of collecting the user's birthday.
Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.`

func NewGetDOBTask(opts GetDOBOptions) *GetDOBTask {
	requireConfirmation := true
	if opts.RequireConfirmationSet {
		requireConfirmation = opts.RequireConfirmation
	}
	timeInstructions := ""
	if opts.IncludeTime {
		timeInstructions = "Also ask for and capture the time of birth if the user knows it. The time is optional - if the user doesn't know it, proceed without it.\n"
	}
	instructions := dobInstructions(requireConfirmation, timeInstructions)
	if strings.TrimSpace(opts.ExtraInstructions) != "" {
		instructions += "\n" + strings.TrimSpace(opts.ExtraInstructions)
	}
	t := &GetDOBTask{
		AgentTask:           *agent.NewAgentTask[*GetDOBResult](instructions),
		ExtraInstructions:   opts.ExtraInstructions,
		IncludeTime:         opts.IncludeTime,
		RequireConfirmation: requireConfirmation,
	}

	t.Agent.Tools = []llm.Tool{
		&updateDOBTool{task: t},
		&declineDOBCaptureTool{task: t},
	}
	if opts.IncludeTime {
		t.Agent.Tools = append(t.Agent.Tools, &updateDOBTimeTool{task: t})
	}

	return t
}

func dobInstructions(requireConfirmation bool, timeInstructions string) string {
	beforeConfirmation := fmt.Sprintf(dobInstructionsBeforeConfirmation, timeInstructions)
	if !requireConfirmation {
		return beforeConfirmation + dobInstructionsAfterConfirmation
	}
	return beforeConfirmation + dobConfirmationInstruction + "\n" + dobInstructionsAfterConfirmation
}

func (t *GetDOBTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			prompt := "Ask the user to provide their date of birth."
			if t.IncludeTime {
				prompt = "Ask the user to provide their date of birth and, if they know it, their time of birth."
			}
			_, _ = session.GenerateReply(context.Background(), prompt)
		}
	}
}

type updateDOBTool struct {
	task *GetDOBTask
}

func (t *updateDOBTool) ID() string   { return "update_dob" }
func (t *updateDOBTool) Name() string { return "update_dob" }
func (t *updateDOBTool) Description() string {
	return "Update the date of birth provided by the user."
}
func (t *updateDOBTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"year":  map[string]any{"type": "integer", "description": "The birth year, for example 1990"},
			"month": map[string]any{"type": "integer", "description": "The birth month, 1-12"},
			"day":   map[string]any{"type": "integer", "description": "The birth day, 1-31"},
		},
		"required": []string{"year", "month", "day"},
	}
}

func (t *updateDOBTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Year  int `json:"year"`
		Month int `json:"month"`
		Day   int `json:"day"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	dob, err := buildDOB(params.Year, params.Month, params.Day)
	if err != nil {
		return "", err
	}
	t.task.currentDOB = &dob

	if !t.task.RequireConfirmation {
		_ = t.task.Complete(&GetDOBResult{DateOfBirth: dob, TimeOfBirth: t.task.currentTime})
		return "Date of birth captured and task completed.", nil
	}

	t.task.setConfirmDOBTool(&dob, t.task.currentTime)
	return fmt.Sprintf("The date of birth has been updated to %s\nRepeat the date back to the user in a natural spoken format.\nPrompt the user for confirmation, do not call `confirm_dob` directly", dob.Format("January 02, 2006")), nil
}

type updateDOBTimeTool struct {
	task *GetDOBTask
}

func (t *updateDOBTimeTool) ID() string   { return "update_time" }
func (t *updateDOBTimeTool) Name() string { return "update_time" }
func (t *updateDOBTimeTool) Description() string {
	return "Update the time of birth provided by the user."
}
func (t *updateDOBTimeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"hour":   map[string]any{"type": "integer", "description": "The birth hour, 0-23"},
			"minute": map[string]any{"type": "integer", "description": "The birth minute, 0-59"},
		},
		"required": []string{"hour", "minute"},
	}
}

func (t *updateDOBTimeTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Hour   int `json:"hour"`
		Minute int `json:"minute"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	birthTime, err := buildDOBTime(params.Hour, params.Minute)
	if err != nil {
		return "", err
	}
	t.task.currentTime = &birthTime

	if !t.task.RequireConfirmation && t.task.currentDOB != nil {
		_ = t.task.Complete(&GetDOBResult{DateOfBirth: *t.task.currentDOB, TimeOfBirth: t.task.currentTime})
		return "Time of birth captured and task completed.", nil
	}
	if t.task.RequireConfirmation {
		t.task.setConfirmDOBTool(t.task.currentDOB, t.task.currentTime)
	}
	formattedTime := birthTime.Format("03:04 PM")
	response := fmt.Sprintf("The time of birth has been updated to %s", formattedTime)
	if t.task.currentDOB != nil {
		response = fmt.Sprintf("The date and time of birth has been updated to %s at %s", t.task.currentDOB.Format("January 02, 2006"), formattedTime)
	}
	if t.task.RequireConfirmation {
		response += "\nRepeat the time back to the user in a natural spoken format.\nPrompt the user for confirmation, do not call `confirm_dob` directly"
	} else {
		response += "\nThe date of birth has not been provided yet, ask the user to provide it."
	}
	return response, nil
}

func (t *GetDOBTask) setConfirmDOBTool(dob *time.Time, birthTime *time.Time) {
	tools := make([]llm.Tool, 0, len(t.Agent.Tools)+1)
	for _, tool := range t.Agent.Tools {
		if tool.ID() == "confirm_dob" {
			continue
		}
		tools = append(tools, tool)
	}
	tools = append(tools, &confirmDOBTool{task: t, dateOfBirth: dob, timeOfBirth: birthTime})
	t.Agent.Tools = tools
}

type confirmDOBTool struct {
	task        *GetDOBTask
	dateOfBirth *time.Time
	timeOfBirth *time.Time
}

func (t *confirmDOBTool) ID() string   { return "confirm_dob" }
func (t *confirmDOBTool) Name() string { return "confirm_dob" }
func (t *confirmDOBTool) Description() string {
	return "Call after the user confirms the date of birth is correct."
}
func (t *confirmDOBTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *confirmDOBTool) Execute(ctx context.Context, args string) (string, error) {
	if !sameOptionalTime(t.dateOfBirth, t.task.currentDOB) || !sameOptionalTime(t.timeOfBirth, t.task.currentTime) {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: dobStaleConfirmationPrompt(),
			})
		}
		return "", nil
	}
	if t.task.currentDOB == nil {
		if activity := t.task.Agent.GetActivity(); activity != nil && activity.Session != nil {
			_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: dobMissingDatePrompt(),
			})
		}
		return "", nil
	}
	_ = t.task.Complete(&GetDOBResult{DateOfBirth: *t.task.currentDOB, TimeOfBirth: t.task.currentTime})
	return "Date of birth confirmed.", nil
}

func dobStaleConfirmationPrompt() string {
	return "The date of birth has changed since confirmation was requested, ask the user to confirm the updated date."
}

func dobMissingDatePrompt() string {
	return "No date of birth was provided yet, ask the user to provide it."
}

type declineDOBCaptureTool struct {
	task *GetDOBTask
}

func (t *declineDOBCaptureTool) ID() string   { return "decline_dob_capture" }
func (t *declineDOBCaptureTool) Name() string { return "decline_dob_capture" }
func (t *declineDOBCaptureTool) Description() string {
	return "Handles the case when the user explicitly declines to provide a date of birth."
}
func (t *declineDOBCaptureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string", "description": "A short explanation of why the user declined"},
		},
		"required": []string{"reason"},
	}
}

func (t *declineDOBCaptureTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	_ = t.task.Fail(llm.NewToolError(fmt.Sprintf("couldn't get the date of birth: %s", params.Reason)))
	return "Task failed.", nil
}

func buildDOB(year int, month int, day int) (time.Time, error) {
	dob := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if dob.Year() != year || int(dob.Month()) != month || dob.Day() != day {
		return time.Time{}, llm.NewToolError(fmt.Sprintf("Invalid date: year=%d month=%d day=%d", year, month, day))
	}
	today := time.Now()
	if dob.After(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)) {
		return time.Time{}, llm.NewToolError(fmt.Sprintf("Invalid date of birth: %s is in the future. Date of birth cannot be a future date.", dob.Format("January 02, 2006")))
	}
	return dob, nil
}

func buildDOBTime(hour int, minute int) (time.Time, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, llm.NewToolError(fmt.Sprintf("Invalid time: hour=%d minute=%d", hour, minute))
	}
	return time.Date(0, time.January, 1, hour, minute, 0, 0, time.UTC), nil
}

func sameOptionalTime(left *time.Time, right *time.Time) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return left.Equal(*right)
	}
}
