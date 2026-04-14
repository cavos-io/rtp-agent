package workflows

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/core/beta"
	"github.com/cavos-io/conversation-worker/library/logger"
)

type GetDtmfResult struct {
	UserInput string
}

type GetDtmfTask struct {
	agent.AgentTask[*GetDtmfResult]
	NumDigits          int
	AskForConfirmation bool
	DtmfInputTimeout   time.Duration
	DtmfStopEvent      beta.DtmfEvent
	currDtmfInputs     []beta.DtmfEvent
	dtmfReplyRunning   bool
	mu                 sync.Mutex
	timer              *time.Timer
}

func NewGetDtmfTask(numDigits int, askConfirmation bool) *GetDtmfTask {
	instructions := `You are a single step in a broader system, responsible solely for gathering digits input from the user. 
You will either receive a sequence of digits through dtmf events tagged by <dtmf_inputs>, or 
user will directly say the digits to you. You should be able to handle both cases. `

	if askConfirmation {
		instructions += "Once user has confirmed the digits (by verbally spoken or entered manually), call `confirm_inputs` with the inputs."
	} else {
		instructions += "If user provides the digits through voice and it is valid, call `record_inputs` with the inputs."
	}

	t := &GetDtmfTask{
		AgentTask:          *agent.NewAgentTask[*GetDtmfResult](instructions),
		NumDigits:          numDigits,
		AskForConfirmation: askConfirmation,
		DtmfInputTimeout:   4 * time.Second,
		DtmfStopEvent:      beta.DtmfEventPound,
		currDtmfInputs:     make([]beta.DtmfEvent, 0),
	}

	if askConfirmation {
		t.Agent.Tools = []interface{}{&confirmInputsTool{task: t}}
	} else {
		t.Agent.Tools = []interface{}{&recordInputsTool{task: t}}
	}

	return t
}

func (t *GetDtmfTask) OnEnter(ctx context.Context) error {
	agentObj := t.Agent.GetAgent()
	if agentObj == nil {
		return nil
	}

	// Assuming session is available via some mechanism in real Start
	// For parity, we should register for SIP DTMF
	return nil
}

func (t *GetDtmfTask) OnExit(ctx context.Context) error {
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
	}
	t.mu.Unlock()
	return nil
}

func (t *GetDtmfTask) onSipDTMFReceived(digit string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.dtmfReplyRunning {
		return
	}

	if digit == string(t.DtmfStopEvent) {
		go t.generateDtmfReply()
		return
	}

	t.currDtmfInputs = append(t.currDtmfInputs, beta.DtmfEvent(digit))
	logger.Logger.Infow("DTMF inputs", "inputs", beta.FormatDtmf(t.currDtmfInputs))

	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(t.DtmfInputTimeout, t.generateDtmfReply)
}

func (t *GetDtmfTask) generateDtmfReply() {
	t.mu.Lock()
	t.dtmfReplyRunning = true
	inputs := make([]beta.DtmfEvent, len(t.currDtmfInputs))
	copy(inputs, t.currDtmfInputs)
	t.currDtmfInputs = make([]beta.DtmfEvent, 0)
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.dtmfReplyRunning = false
		t.mu.Unlock()
	}()

	dtmfStr := beta.FormatDtmf(inputs)
	logger.Logger.Debugw("Generating DTMF reply", "currentInputs", dtmfStr)

	if len(inputs) != t.NumDigits {
		t.Fail(fmt.Errorf("digits input not fully received. Expect %d digits, got %d", t.NumDigits, len(inputs)))
		return
	}

	if !t.AskForConfirmation {
		t.Complete(&GetDtmfResult{UserInput: dtmfStr})
		return
	}

	if activity := t.Agent.GetActivity(); activity != nil {
		if session := activity.Session; session != nil {
			_, _ = session.GenerateReply(context.Background(), fmt.Sprintf("You entered %s. Please confirm if this is correct.", dtmfStr), true)
		}
	}
}

type confirmInputsTool struct {
	task *GetDtmfTask
}

func (t *confirmInputsTool) ID() string   { return "confirm_inputs" }
func (t *confirmInputsTool) Name() string { return "confirm_inputs" }
func (t *confirmInputsTool) Description() string {
	return "Finalize the collected digit inputs after explicit user confirmation."
}
func (t *confirmInputsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"inputs": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"required": []string{"inputs"},
	}
}

func (t *confirmInputsTool) Execute(ctx context.Context, args any) (any, error) {
	m, _ := args.(map[string]any)
	inputsRaw, ok := m["inputs"].([]interface{})
	if !ok {
		return "", fmt.Errorf("invalid or missing inputs array")
	}
	var inputs []string
	for _, raw := range inputsRaw {
		if s, isStr := raw.(string); isStr {
			inputs = append(inputs, s)
		}
	}

	dtmfEvents := make([]beta.DtmfEvent, len(inputs))
	for i, v := range inputs {
		dtmfEvents[i] = beta.DtmfEvent(v)
	}

	t.task.Complete(&GetDtmfResult{UserInput: beta.FormatDtmf(dtmfEvents)})
	return "Inputs confirmed.", nil
}

type recordInputsTool struct {
	task *GetDtmfTask
}

func (t *recordInputsTool) ID() string   { return "record_inputs" }
func (t *recordInputsTool) Name() string { return "record_inputs" }
func (t *recordInputsTool) Description() string {
	return "Record the collected digit inputs without additional confirmation."
}
func (t *recordInputsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"inputs": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"required": []string{"inputs"},
	}
}

func (t *recordInputsTool) Execute(ctx context.Context, args any) (any, error) {
	m, _ := args.(map[string]any)
	inputsRaw, ok := m["inputs"].([]interface{})
	if !ok {
		return "", fmt.Errorf("invalid or missing inputs array")
	}
	var inputs []string
	for _, raw := range inputsRaw {
		if s, isStr := raw.(string); isStr {
			inputs = append(inputs, s)
		}
	}

	dtmfEvents := make([]beta.DtmfEvent, len(inputs))
	for i, v := range inputs {
		dtmfEvents[i] = beta.DtmfEvent(v)
	}

	t.task.Complete(&GetDtmfResult{UserInput: beta.FormatDtmf(dtmfEvents)})
	return "Inputs recorded.", nil
}
