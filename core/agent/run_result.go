package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cavos-io/conversation-worker/core/evals"
	"github.com/cavos-io/conversation-worker/core/llm"
)

var (
	ErrRunResultNotDone       = errors.New("run result is not done")
	ErrRunResultNoFinalOutput = errors.New("run result has no final output")
)

type RunResult struct {
	ChatCtx        *llm.ChatContext
	Expect         *RunAssert
	events         []RunEvent
	done           bool
	finalOutput    any
	finalOutputSet bool
}

func NewRunResult(chatCtx *llm.ChatContext) *RunResult {
	return &RunResult{
		ChatCtx: chatCtx,
		Expect:  &RunAssert{ChatCtx: chatCtx},
		events:  make([]RunEvent, 0),
	}
}

func (r *RunResult) Done() bool {
	return r.done
}

func (r *RunResult) MarkDone() {
	r.done = true
}

func (r *RunResult) SetFinalOutput(output any) {
	r.finalOutput = output
	r.finalOutputSet = true
}

func (r *RunResult) FinalOutput() (any, error) {
	if !r.done {
		return nil, ErrRunResultNotDone
	}
	if !r.finalOutputSet {
		return nil, ErrRunResultNoFinalOutput
	}
	return r.finalOutput, nil
}

type RunEvent interface {
	GetType() string
	GetCreatedAt() time.Time
}

type ChatMessageEvent struct {
	Item *llm.ChatMessage
}

func (e *ChatMessageEvent) GetType() string         { return "message" }
func (e *ChatMessageEvent) GetCreatedAt() time.Time { return e.Item.GetCreatedAt() }

type FunctionCallEvent struct {
	Item *llm.FunctionCall
}

func (e *FunctionCallEvent) GetType() string         { return "function_call" }
func (e *FunctionCallEvent) GetCreatedAt() time.Time { return e.Item.GetCreatedAt() }

type FunctionCallOutputEvent struct {
	Item *llm.FunctionCallOutput
}

func (e *FunctionCallOutputEvent) GetType() string         { return "function_call_output" }
func (e *FunctionCallOutputEvent) GetCreatedAt() time.Time { return e.Item.GetCreatedAt() }

type AgentHandoffEvent struct {
	Item     *llm.AgentHandoff
	OldAgent *Agent
	NewAgent *Agent
}

func (e *AgentHandoffEvent) GetType() string         { return "agent_handoff" }
func (e *AgentHandoffEvent) GetCreatedAt() time.Time { return e.Item.GetCreatedAt() }

func (r *RunResult) Events() []RunEvent {
	events := make([]RunEvent, len(r.events))
	copy(events, r.events)
	return events
}

func (r *RunResult) RecordItem(item llm.ChatItem) {
	var event RunEvent
	switch item := item.(type) {
	case *llm.ChatMessage:
		event = &ChatMessageEvent{Item: item}
	case *llm.FunctionCall:
		event = &FunctionCallEvent{Item: item}
	case *llm.FunctionCallOutput:
		event = &FunctionCallOutputEvent{Item: item}
	default:
		return
	}
	r.insertEvent(event)
}

func (r *RunResult) RecordAgentHandoff(item *llm.AgentHandoff, oldAgent *Agent, newAgent *Agent) {
	r.insertEvent(&AgentHandoffEvent{Item: item, OldAgent: oldAgent, NewAgent: newAgent})
}

func (r *RunResult) insertEvent(event RunEvent) {
	idx := r.findEventInsertionIndex(event.GetCreatedAt())
	r.events = append(r.events, nil)
	copy(r.events[idx+1:], r.events[idx:])
	r.events[idx] = event
}

func (r *RunResult) findEventInsertionIndex(createdAt time.Time) int {
	for i := len(r.events) - 1; i >= 0; i-- {
		if !r.events[i].GetCreatedAt().After(createdAt) {
			return i + 1
		}
	}
	return 0
}

type RunAssert struct {
	ChatCtx *llm.ChatContext
	errors  []error
}

func (a *RunAssert) IsFunctionCall(name string) *RunAssert {
	found := false
	for _, item := range a.ChatCtx.Items {
		if fc, ok := item.(*llm.FunctionCall); ok {
			if fc.Name == name {
				found = true
				break
			}
		}
	}
	if !found {
		a.errors = append(a.errors, fmt.Errorf("expected function call %q, but not found", name))
	}
	return a
}

func (a *RunAssert) ContainsMessage(role llm.ChatRole, content string) *RunAssert {
	found := false
	for _, item := range a.ChatCtx.Items {
		if msg, ok := item.(*llm.ChatMessage); ok && msg.Role == role {
			if strings.Contains(msg.TextContent(), content) {
				found = true
				break
			}
		}
	}
	if !found {
		a.errors = append(a.errors, fmt.Errorf("expected message from %s containing %q", role, content))
	}
	return a
}

func (a *RunAssert) HasError() error {
	if len(a.errors) == 0 {
		return nil
	}
	var msgs []string
	for _, err := range a.errors {
		msgs = append(msgs, err.Error())
	}
	return fmt.Errorf("assertions failed:\\n%s", strings.Join(msgs, "\\n"))
}

func (a *RunAssert) Judge(ctx context.Context, evaluator evals.Evaluator, llmInstance llm.LLM) (*RunAssert, error) {
	res, err := evaluator.Evaluate(ctx, a.ChatCtx, nil, llmInstance)
	if err != nil {
		a.errors = append(a.errors, fmt.Errorf("judge %q failed with error: %w", evaluator.Name(), err))
		return a, err
	}

	if res.Verdict == "fail" {
		a.errors = append(a.errors, fmt.Errorf("judge %q failed with verdict 'fail': %s", evaluator.Name(), res.Reasoning))
	} else if res.Verdict == "maybe" {
		a.errors = append(a.errors, fmt.Errorf("judge %q returned verdict 'maybe': %s", evaluator.Name(), res.Reasoning))
	}

	return a, nil
}
