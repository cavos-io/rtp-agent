package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/beta"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
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
	dtmfStopCh         chan struct{}
	userState          agent.UserState
	agentState         agent.AgentState
}

func ValidateDtmfNumDigits(numDigits int) error {
	if numDigits <= 0 {
		return fmt.Errorf("num_digits must be greater than 0")
	}
	return nil
}

func NewGetDtmfTask(numDigits int, askConfirmation bool) (*GetDtmfTask, error) {
	if err := ValidateDtmfNumDigits(numDigits); err != nil {
		return nil, err
	}

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
		userState:          agent.UserStateListening,
		agentState:         agent.AgentStateInitializing,
	}

	if askConfirmation {
		t.Agent.Tools = []llm.Tool{&confirmInputsTool{task: t}}
	} else {
		t.Agent.Tools = []llm.Tool{&recordInputsTool{task: t}}
	}

	return t, nil
}

func (t *GetDtmfTask) OnEnter() {
	activity := t.Agent.GetActivity()
	if activity == nil || activity.Session == nil {
		return
	}

	stopCh := make(chan struct{})
	t.mu.Lock()
	t.dtmfStopCh = stopCh
	t.userState = activity.Session.UserState()
	t.agentState = activity.Session.AgentState()
	t.mu.Unlock()

	dtmfEvents := activity.Session.SipDTMFEvents()
	userStateEvents := activity.Session.UserStateChangedEvents()
	agentStateEvents := activity.Session.AgentStateChangedEvents()
	go func() {
		for {
			select {
			case <-stopCh:
				return
			case ev := <-dtmfEvents:
				t.onSipDTMFReceived(ev.Digit)
			case ev := <-userStateEvents:
				t.onUserStateChanged(ev.NewState)
			case ev := <-agentStateEvents:
				t.onAgentStateChanged(ev.NewState)
			}
		}
	}()

	_, _ = activity.Session.GenerateReplyWithOptions(context.Background(), agent.GenerateReplyOptions{
		ToolChoice: "none",
	})
}

func (t *GetDtmfTask) OnExit() {
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
	}
	if t.dtmfStopCh != nil {
		close(t.dtmfStopCh)
		t.dtmfStopCh = nil
	}
	shouldGenerateReply := !t.dtmfReplyRunning && len(t.currDtmfInputs) > 0
	t.mu.Unlock()

	if shouldGenerateReply {
		go t.generateDtmfReply()
	}
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

	t.updateDtmfReplyTimerLocked()
}

func (t *GetDtmfTask) onUserStateChanged(state agent.UserState) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.userState = state
	t.updateDtmfReplyTimerLocked()
}

func (t *GetDtmfTask) onAgentStateChanged(state agent.AgentState) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.agentState = state
	t.updateDtmfReplyTimerLocked()
}

func (t *GetDtmfTask) updateDtmfReplyTimerLocked() {
	if t.dtmfReplyRunning || len(t.currDtmfInputs) == 0 {
		return
	}

	if t.userState == agent.UserStateSpeaking ||
		t.agentState == agent.AgentStateSpeaking ||
		t.agentState == agent.AgentStateThinking {
		t.cancelDtmfReplyTimerLocked()
		return
	}

	t.scheduleDtmfReplyLocked()
}

func (t *GetDtmfTask) scheduleDtmfReplyLocked() {
	t.cancelDtmfReplyTimerLocked()
	t.timer = time.AfterFunc(t.DtmfInputTimeout, t.generateDtmfReply)
}

func (t *GetDtmfTask) cancelDtmfReplyTimerLocked() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
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
			_, _ = session.GenerateReply(context.Background(), buildDtmfConfirmationInstructions(dtmfStr))
		}
	}
}

func buildDtmfConfirmationInstructions(dtmfStr string) string {
	return "User has entered the following valid digits on the telephone keypad:\n" +
		fmt.Sprintf("<dtmf_inputs>%s</dtmf_inputs>\n", dtmfStr) +
		"Please confirm it with the user by saying the digits one by one with space in between " +
		"(.e.g. 'one two three four five six seven eight nine ten'). " +
		"Once you are sure, call `confirm_inputs` with the inputs."
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

func (t *confirmInputsTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Inputs []string `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	dtmfEvents, err := parseDtmfInputs(params.Inputs)
	if err != nil {
		return "", err
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

func (t *recordInputsTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Inputs []string `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}

	dtmfEvents, err := parseDtmfInputs(params.Inputs)
	if err != nil {
		return "", err
	}

	t.task.Complete(&GetDtmfResult{UserInput: beta.FormatDtmf(dtmfEvents)})
	return "Inputs recorded.", nil
}

func parseDtmfInputs(inputs []string) ([]beta.DtmfEvent, error) {
	dtmfEvents := make([]beta.DtmfEvent, len(inputs))
	for i, v := range inputs {
		event := beta.DtmfEvent(v)
		if _, err := beta.DtmfEventToCode(event); err != nil {
			return nil, err
		}
		dtmfEvents[i] = event
	}
	return dtmfEvents, nil
}
