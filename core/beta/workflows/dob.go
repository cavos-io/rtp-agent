package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	AgentOptions
	ExtraInstructions      string
	IncludeTime            bool
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	ChatContext            *llm.ChatContext
	Tools                  []llm.Tool
}

type GetDOBTask struct {
	agent.AgentTask[*GetDOBResult]
	ExtraInstructions      string
	IncludeTime            bool
	RequireConfirmation    bool
	RequireConfirmationSet bool
	RequireExplicitAsk     bool
	currentDOB             *time.Time
	currentTime            *time.Time
}

const dobConfirmationInstruction = "Call `confirm_dob` after the user confirmed the date of birth is correct."

const dobInstructionsBeforeConfirmation = `You are only a single step in a broader system, responsible solely for capturing a date of birth.
Handle input as noisy voice transcription. Expect that users will say dates aloud with formats like:
- 'January 15th 1990'
- 'the fifteenth of January nineteen ninety'
- '01 15 1990' or 'one fifteen ninety'
- 'Jan 15 90'
- '15th January 1990'
Normalize common spoken patterns silently:
- Convert spoken numbers and ordinals to their numeric form: 'fifteenth' → 15, 'ninety' → 1990.
- Recognize month names in various forms: 'Jan', 'January', etc.
- Handle two-digit years appropriately: '90' likely means 1990, '05' likely means 2005.
- Filter out filler words or hesitations.
Don't mention corrections. Treat inputs as possibly imperfect but fix them silently.
%sCall ` + "`update_dob`" + ` at the first opportunity whenever you form a new hypothesis about the date of birth. (before asking any questions or providing any answers.)
Don't invent dates, stick strictly to what the user said.
`

const dobInstructionsAfterConfirmation = `When reading back dates, use a natural spoken format like 'January fifteenth, nineteen ninety'.
If the date is unclear or invalid, or it takes too much back-and-forth, prompt for it in parts: first the month, then the day, then the year.
Ignore unrelated input and avoid going off-topic. Do not generate markdown, greetings, or unnecessary commentary.
Avoid verbosity by not sharing example dates or formats unless prompted to do so. Do not deviate from the goal of collecting the user's birthday.
Always explicitly invoke a tool when applicable. Do not simulate tool usage, no real action is taken unless the tool is explicitly called.`

const dobTextInstructionsBeforeConfirmation = `You are only a single step in a broader system, responsible solely for capturing a date of birth.
Handle input as typed text. Expect users to type their date of birth directly.
Accept common date formats like 'MM/DD/YYYY', 'January 15, 1990', or '1990-01-15'.
Handle two-digit years appropriately: '90' likely means 1990, '05' likely means 2005.
%sCall ` + "`update_dob`" + ` at the first opportunity whenever you form a new hypothesis about the date of birth. (before asking any questions or providing any answers.)
Don't invent dates, stick strictly to what the user said.
`

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
	textInstructions := dobTextInstructions(opts.RequireConfirmationSet && opts.RequireConfirmation, timeInstructions)
	if strings.TrimSpace(opts.ExtraInstructions) != "" {
		extra := "\n" + strings.TrimSpace(opts.ExtraInstructions)
		instructions += extra
		textInstructions += extra
	}
	t := &GetDOBTask{
		AgentTask:              *agent.NewAgentTask[*GetDOBResult](instructions),
		ExtraInstructions:      opts.ExtraInstructions,
		IncludeTime:            opts.IncludeTime,
		RequireConfirmation:    requireConfirmation,
		RequireConfirmationSet: opts.RequireConfirmationSet,
		RequireExplicitAsk:     opts.RequireExplicitAsk,
	}
	if opts.ChatContext != nil {
		t.ChatCtx = opts.ChatContext.Copy()
	}
	t.InstructionVariants = llm.NewInstructions(instructions, textInstructions)
	applyAgentOptions(&t.Agent, opts.AgentOptions)

	t.Agent.Tools = append(append([]llm.Tool{}, opts.Tools...),
		&updateDOBTool{task: t},
		&declineDOBCaptureTool{task: t},
	)
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

func dobTextInstructions(requireConfirmation bool, timeInstructions string) string {
	beforeConfirmation := fmt.Sprintf(dobTextInstructionsBeforeConfirmation, timeInstructions)
	if !requireConfirmation {
		return beforeConfirmation + dobInstructionsAfterConfirmation
	}
	return beforeConfirmation + dobConfirmationInstruction + "\n" + dobInstructionsAfterConfirmation
}

func dobConfirmationRequired(ctx context.Context, requireConfirmation bool, set bool) bool {
	if !requireConfirmation {
		return false
	}
	if set {
		return requireConfirmation
	}
	runCtx := agent.GetRunContext(ctx)
	if runCtx == nil || runCtx.SpeechHandle == nil {
		return true
	}
	return runCtx.SpeechHandle.InputDetails.Modality == "audio"
}

func (t *GetDOBTask) OnEnter() {
	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			prompt := "Ask the user to provide their date of birth."
			if t.IncludeTime {
				prompt = "Ask the user to provide their date of birth and, if they know it, their time of birth."
			}
			_, _ = session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
				Instructions: prompt,
			})
		}
	}
}

type updateDOBTool struct {
	task *GetDOBTask
}

func (t *updateDOBTool) ID() string   { return "update_dob" }
func (t *updateDOBTool) Name() string { return "update_dob" }
func (t *updateDOBTool) ToolFlags() llm.ToolFlag {
	if t.task.RequireExplicitAsk {
		return llm.ToolFlagIgnoreOnEnter
	}
	return llm.ToolFlagNone
}
func (t *updateDOBTool) Description() string {
	return "Update the date of birth provided by the user. Given a spoken month and year (e.g., 'July 2030'), return its numerical representation (7/2030)."
}
func (t *updateDOBTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"year":  map[string]any{"type": "integer", "description": "The birth year (e.g., 1990)"},
			"month": map[string]any{"type": "integer", "description": "The birth month (1-12)"},
			"day":   map[string]any{"type": "integer", "description": "The birth day (1-31)"},
		},
		"required": []string{"year", "month", "day"},
	}
}

func (t *updateDOBTool) Execute(ctx context.Context, args string) (string, error) {
	year, month, day, err := parseDOBArgs([]byte(args))
	if err != nil {
		return "", err
	}

	dob, err := buildDOB(year, month, day)
	if err != nil {
		return "", err
	}
	t.task.currentDOB = &dob

	requireConfirmation := dobConfirmationRequired(ctx, t.task.RequireConfirmation, t.task.RequireConfirmationSet)
	if !requireConfirmation {
		_ = t.task.Complete(&GetDOBResult{DateOfBirth: dob, TimeOfBirth: t.task.currentTime})
		return "", nil
	}

	t.task.setConfirmDOBTool(&dob, t.task.currentTime)
	response := "The date of birth has been updated."
	confirmationTarget := "date"
	if t.task.currentTime != nil {
		confirmationTarget = "date and time"
	}
	response += dobConfirmationPrompt(confirmationTarget)
	return response, nil
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
			"hour":   map[string]any{"type": "integer", "description": "The birth hour (0-23)"},
			"minute": map[string]any{"type": "integer", "description": "The birth minute (0-59)"},
		},
		"required": []string{"hour", "minute"},
	}
}

func (t *updateDOBTimeTool) Execute(ctx context.Context, args string) (string, error) {
	hour, minute, err := parseDOBTimeArgs([]byte(args))
	if err != nil {
		return "", err
	}
	birthTime, err := buildDOBTime(hour, minute)
	if err != nil {
		return "", err
	}
	t.task.currentTime = &birthTime

	requireConfirmation := dobConfirmationRequired(ctx, t.task.RequireConfirmation, t.task.RequireConfirmationSet)
	if !requireConfirmation && t.task.currentDOB != nil {
		_ = t.task.Complete(&GetDOBResult{DateOfBirth: *t.task.currentDOB, TimeOfBirth: t.task.currentTime})
		return "", nil
	}
	if requireConfirmation {
		t.task.setConfirmDOBTool(t.task.currentDOB, t.task.currentTime)
	}
	response := "The time of birth has been updated."
	readbackTarget := "time"
	if t.task.currentDOB != nil {
		response = "The date and time of birth has been updated."
		readbackTarget = "date and time"
	}
	if requireConfirmation {
		response += dobConfirmationPrompt(readbackTarget)
	} else {
		response += "\nThe date of birth has not been provided yet, ask the user to provide it."
	}
	return response, nil
}

func dobConfirmationPrompt(target string) string {
	return fmt.Sprintf("\nAsk the user to confirm the updated %s of birth without repeating it back.\nPrompt the user for confirmation, do not call `confirm_dob` directly", target)
}

func parseDOBTimeArgs(args []byte) (int, int, error) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(args, &params); err != nil {
		return 0, 0, err
	}
	meridiem := parseDOBMeridiem(params["hour"])
	if meridiem == "" {
		meridiem = parseDOBMeridiem(params["minute"])
	}
	if hour, minute, ok := parseNaturalDOBClockPhrase(params["hour"]); ok {
		return applyDOBMeridiem(hour, meridiem), minute, nil
	}
	hour, err := parseDOBClockNumber(params["hour"])
	if err != nil {
		return 0, 0, fmt.Errorf("hour: %w", err)
	}
	minute, err := parseDOBClockNumber(params["minute"])
	if err != nil {
		return 0, 0, fmt.Errorf("minute: %w", err)
	}
	hour = applyDOBMeridiem(hour, meridiem)
	return hour, minute, nil
}

func parseNaturalDOBClockPhrase(raw json.RawMessage) (int, int, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, 0, false
	}
	text = stripDOBClockSuffix(stripDOBMeridiem(cleanSpokenDOBText(text)))
	tokens := strings.Fields(text)
	if len(tokens) < 3 {
		return 0, 0, false
	}
	parseHour := func(parts []string) (int, bool) {
		if len(parts) == 0 {
			return 0, false
		}
		if len(parts) == 1 {
			switch parts[0] {
			case "midnight":
				return 0, true
			case "noon":
				return 12, true
			}
			if value, err := strconv.Atoi(parts[0]); err == nil {
				return value, true
			}
		}
		return parseSpokenExpirationNumber(strings.Join(parts, " "))
	}
	if tokens[0] == "quarter" && tokens[1] == "past" {
		hour, ok := parseHour(tokens[2:])
		return hour, 15, ok
	}
	if tokens[0] == "half" && tokens[1] == "past" {
		hour, ok := parseHour(tokens[2:])
		return hour, 30, ok
	}
	if tokens[0] == "quarter" && tokens[1] == "to" {
		hour, ok := parseHour(tokens[2:])
		if !ok {
			return 0, 0, false
		}
		if hour == 0 {
			hour = 12
		}
		return hour - 1, 45, true
	}
	return 0, 0, false
}

func parseDOBClockNumber(raw json.RawMessage) (int, error) {
	var number int
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	text = cleanSpokenDOBText(text)
	text = stripDOBMeridiem(text)
	text = stripDOBClockSuffix(text)
	switch text {
	case "midnight":
		return 0, nil
	case "noon":
		return 12, nil
	}
	if value, err := strconv.Atoi(text); err == nil {
		return value, nil
	}
	if value, ok := parseSpokenExpirationNumber(text); ok {
		return value, nil
	}
	return 0, fmt.Errorf("invalid time number %q", text)
}

func parseDOBMeridiem(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	text = cleanSpokenDOBText(text)
	tokens := strings.Fields(text)
	for i, token := range tokens {
		switch token {
		case "am", "a.m", "morning":
			return "am"
		case "pm", "p.m", "afternoon", "evening", "night":
			return "pm"
		case "a":
			if i+1 < len(tokens) && tokens[i+1] == "m" {
				return "am"
			}
		case "p":
			if i+1 < len(tokens) && tokens[i+1] == "m" {
				return "pm"
			}
		}
	}
	return ""
}

func stripDOBMeridiem(text string) string {
	tokens := strings.Fields(text)
	out := tokens[:0]
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch token {
		case "am", "pm", "a.m", "p.m", "morning", "afternoon", "evening", "night":
			continue
		case "a", "p":
			if i+1 < len(tokens) && tokens[i+1] == "m" {
				i++
				continue
			}
		}
		out = append(out, token)
	}
	return strings.Join(out, " ")
}

func stripDOBClockSuffix(text string) string {
	text = strings.ReplaceAll(text, "o'clock", "o clock")
	tokens := strings.Fields(text)
	if len(tokens) >= 2 && tokens[len(tokens)-2] == "o" && tokens[len(tokens)-1] == "clock" {
		return strings.Join(tokens[:len(tokens)-2], " ")
	}
	if len(tokens) >= 1 && tokens[len(tokens)-1] == "oclock" {
		return strings.Join(tokens[:len(tokens)-1], " ")
	}
	return text
}

func applyDOBMeridiem(hour int, meridiem string) int {
	switch meridiem {
	case "am":
		if hour == 12 {
			return 0
		}
	case "pm":
		if hour >= 1 && hour < 12 {
			return hour + 12
		}
	}
	return hour
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
	return "", nil
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
func (t *declineDOBCaptureTool) ToolFlags() llm.ToolFlag {
	return llm.ToolFlagIgnoreOnEnter
}
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
