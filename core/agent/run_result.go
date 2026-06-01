package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	watchedSpeech  map[*SpeechHandle]runResultSpeechWatch
	mu             sync.Mutex
}

type runResultSpeechWatch struct {
	removeDoneCallback      func()
	removeItemAddedCallback func()
}

func NewRunResult(chatCtx *llm.ChatContext) *RunResult {
	result := &RunResult{
		ChatCtx:       chatCtx,
		events:        make([]RunEvent, 0),
		watchedSpeech: make(map[*SpeechHandle]runResultSpeechWatch),
	}
	result.Expect = &RunAssert{ChatCtx: chatCtx, result: result}
	return result
}

func (r *RunResult) Done() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.done
}

func (r *RunResult) MarkDone() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.done = true
}

func (r *RunResult) SetFinalOutput(output any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.finalOutput = output
	r.finalOutputSet = true
}

func (r *RunResult) FinalOutput() (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

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
	r.mu.Lock()
	defer r.mu.Unlock()

	events := make([]RunEvent, len(r.events))
	copy(events, r.events)
	return events
}

func (r *RunResult) RecordItem(item llm.ChatItem) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordItemLocked(item)
}

func (r *RunResult) recordItemLocked(item llm.ChatItem) {
	if r.done {
		return
	}

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
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.done {
		return
	}
	r.insertEvent(&AgentHandoffEvent{Item: item, OldAgent: oldAgent, NewAgent: newAgent})
}

func (r *RunResult) WatchSpeechHandle(speech *SpeechHandle) bool {
	if speech == nil {
		return false
	}

	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return false
	}
	if _, ok := r.watchedSpeech[speech]; ok {
		r.mu.Unlock()
		return false
	}
	r.watchedSpeech[speech] = runResultSpeechWatch{}
	r.mu.Unlock()

	removeItemAddedCallback := speech.AddItemAddedCallback(func(item llm.ChatItem) {
		r.RecordItem(item)
	})
	removeDoneCallback := speech.AddDoneCallback(func(*SpeechHandle) {
		r.markDoneIfNeeded()
	})

	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		removeDoneCallback()
		removeItemAddedCallback()
		return false
	}
	if _, ok := r.watchedSpeech[speech]; !ok {
		r.mu.Unlock()
		removeDoneCallback()
		removeItemAddedCallback()
		return false
	}
	r.watchedSpeech[speech] = runResultSpeechWatch{
		removeDoneCallback:      removeDoneCallback,
		removeItemAddedCallback: removeItemAddedCallback,
	}
	r.mu.Unlock()

	r.markDoneIfNeeded()
	return true
}

func (r *RunResult) UnwatchSpeechHandle(speech *SpeechHandle) bool {
	r.mu.Lock()
	watch, ok := r.watchedSpeech[speech]
	if ok {
		delete(r.watchedSpeech, speech)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}

	if watch.removeDoneCallback != nil {
		watch.removeDoneCallback()
	}
	if watch.removeItemAddedCallback != nil {
		watch.removeItemAddedCallback()
	}
	return true
}

func (r *RunResult) markDoneIfNeeded() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.done || len(r.watchedSpeech) == 0 {
		return
	}
	for speech := range r.watchedSpeech {
		if !speech.IsDone() {
			return
		}
	}
	r.done = true
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
	result  *RunResult
	errors  []error
}

func (a *RunAssert) IsFunctionCall(name string) *RunAssert {
	found := false
	for _, event := range a.events() {
		fcEvent, ok := event.(*FunctionCallEvent)
		if ok && fcEvent.Item.Name == name {
			found = true
			break
		}
	}
	if !found {
		a.errors = append(a.errors, fmt.Errorf("expected function call %q, but not found", name))
	}
	return a
}

func (a *RunAssert) ContainsMessage(role llm.ChatRole, content string) *RunAssert {
	found := false
	for _, event := range a.events() {
		msgEvent, ok := event.(*ChatMessageEvent)
		if ok && msgEvent.Item.Role == role && strings.Contains(msgEvent.Item.TextContent(), content) {
			found = true
			break
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

func (a *RunAssert) events() []RunEvent {
	if a.result == nil {
		return nil
	}
	return a.result.Events()
}
