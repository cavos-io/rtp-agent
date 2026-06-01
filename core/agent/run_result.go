package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/evals"
	"github.com/cavos-io/conversation-worker/core/llm"
)

var (
	ErrRunResultNotDone         = errors.New("run result is not done")
	ErrRunResultNoFinalOutput   = errors.New("run result has no final output")
	ErrRunResultFinalOutputType = errors.New("run result final output type mismatch")
)

type RunResult struct {
	ChatCtx         *llm.ChatContext
	Expect          *RunAssert
	events          []RunEvent
	done            bool
	finalOutput     any
	finalOutputErr  error
	finalOutputSet  bool
	finalOutputType reflect.Type
	watchedSpeech   map[*SpeechHandle]runResultSpeechWatch
	lastSpeech      *SpeechHandle
	mu              sync.Mutex
}

type runResultSpeechWatch struct {
	removeDoneCallback      func()
	removeItemAddedCallback func()
}

func NewRunResult(chatCtx *llm.ChatContext) *RunResult {
	return NewRunResultWithOutputType(chatCtx, nil)
}

func NewRunResultWithOutputType(chatCtx *llm.ChatContext, outputType reflect.Type) *RunResult {
	result := &RunResult{
		ChatCtx:         chatCtx,
		events:          make([]RunEvent, 0),
		finalOutputType: outputType,
		watchedSpeech:   make(map[*SpeechHandle]runResultSpeechWatch),
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
	if r.finalOutputErr != nil {
		return nil, r.finalOutputErr
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
	removeDoneCallback := speech.AddDoneCallback(func(doneSpeech *SpeechHandle) {
		r.markDoneIfNeeded(doneSpeech)
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

	r.markDoneIfNeeded(nil)
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

func (r *RunResult) markDoneIfNeeded(doneSpeech *SpeechHandle) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.done || len(r.watchedSpeech) == 0 {
		return
	}
	if doneSpeech != nil {
		r.lastSpeech = doneSpeech
	}
	for speech := range r.watchedSpeech {
		if !speech.IsDone() {
			return
		}
	}
	if r.lastSpeech != nil {
		if output, ok := r.lastSpeech.RunFinalOutput(); ok {
			if err, ok := output.(error); ok {
				r.finalOutputErr = err
			} else if r.finalOutputType != nil && reflect.TypeOf(output) != r.finalOutputType {
				r.finalOutputErr = fmt.Errorf("%w: expected %s, got %T", ErrRunResultFinalOutputType, r.finalOutputType, output)
			} else {
				r.finalOutput = output
			}
			r.finalOutputSet = true
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
	index   int
}

type RunEventCriteria struct {
	Role      llm.ChatRole
	Name      string
	Arguments map[string]any
	Output    string
	IsError   *bool
	NewAgent  *Agent
}

func (a *RunAssert) IsFunctionCall(name string) *RunAssert {
	return a.IsFunctionCallWithArguments(name, nil)
}

func (a *RunAssert) IsFunctionCallWithArguments(name string, arguments map[string]any) *RunAssert {
	found := false
	var argumentErr error
	for _, event := range a.events() {
		fcEvent, ok := event.(*FunctionCallEvent)
		if ok && fcEvent.Item.Name == name {
			if len(arguments) > 0 {
				if err := assertFunctionCallArguments(fcEvent.Item.Arguments, arguments); err != nil {
					argumentErr = err
					continue
				}
			}
			found = true
			break
		}
	}
	if !found {
		if argumentErr != nil {
			a.errors = append(a.errors, fmt.Errorf("expected function call %q with matching arguments: %w", name, argumentErr))
		} else {
			a.errors = append(a.errors, fmt.Errorf("expected function call %q, but not found", name))
		}
	}
	return a
}

func assertFunctionCallArguments(raw string, expected map[string]any) error {
	var actual map[string]any
	if err := json.Unmarshal([]byte(raw), &actual); err != nil {
		return fmt.Errorf("invalid JSON arguments: %w", err)
	}
	for key, expectedValue := range expected {
		actualValue, ok := actual[key]
		if !ok {
			return fmt.Errorf("missing key %q", key)
		}
		if !reflect.DeepEqual(actualValue, expectedValue) {
			return fmt.Errorf("for key %q, expected %#v, got %#v", key, expectedValue, actualValue)
		}
	}
	return nil
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

func (a *RunAssert) ContainsMessageRole(role llm.ChatRole) *RunAssert {
	found := false
	for _, event := range a.events() {
		msgEvent, ok := event.(*ChatMessageEvent)
		if ok && msgEvent.Item.Role == role {
			found = true
			break
		}
	}
	if !found {
		a.errors = append(a.errors, fmt.Errorf("expected message from %s, but not found", role))
	}
	return a
}

func (a *RunAssert) ContainsFunctionCallOutput(output string, isError bool) *RunAssert {
	found := false
	for _, event := range a.events() {
		outputEvent, ok := event.(*FunctionCallOutputEvent)
		if ok && outputEvent.Item.Output == output && outputEvent.Item.IsError == isError {
			found = true
			break
		}
	}
	if !found {
		a.errors = append(a.errors, fmt.Errorf("expected function call output %q with is_error=%t, but not found", output, isError))
	}
	return a
}

func (a *RunAssert) ContainsAgentHandoff(newAgent *Agent) *RunAssert {
	found := false
	for _, event := range a.events() {
		handoffEvent, ok := event.(*AgentHandoffEvent)
		if ok && handoffEvent.NewAgent == newAgent {
			found = true
			break
		}
	}
	if !found {
		a.errors = append(a.errors, errors.New("expected agent handoff, but not found"))
	}
	return a
}

func (a *RunAssert) NextEvent(eventType ...string) *RunAssert {
	expectedType := ""
	if len(eventType) > 0 {
		expectedType = eventType[0]
	}

	events := a.events()
	for a.index < len(events) {
		event := events[a.index]
		a.index++
		if expectedType == "" || event.GetType() == expectedType {
			return a
		}
	}

	if expectedType == "" {
		a.errors = append(a.errors, errors.New("expected another event, but none left"))
	} else {
		a.errors = append(a.errors, fmt.Errorf("expected another event of type %s, but none found", expectedType))
	}
	return a
}

func (a *RunAssert) SkipNextEventIf(eventType string, criteria ...RunEventCriteria) bool {
	events := a.events()
	if a.index >= len(events) {
		return false
	}
	event := events[a.index]
	if event.GetType() != eventType {
		return false
	}
	if len(criteria) > 0 && !eventMatchesCriteria(event, criteria[0]) {
		return false
	}

	a.index++
	return true
}

func eventMatchesCriteria(event RunEvent, criteria RunEventCriteria) bool {
	switch event := event.(type) {
	case *ChatMessageEvent:
		return criteria.Role == "" || event.Item.Role == criteria.Role
	case *FunctionCallEvent:
		if criteria.Name != "" && event.Item.Name != criteria.Name {
			return false
		}
		if len(criteria.Arguments) > 0 {
			return assertFunctionCallArguments(event.Item.Arguments, criteria.Arguments) == nil
		}
		return true
	case *FunctionCallOutputEvent:
		if criteria.Output != "" && event.Item.Output != criteria.Output {
			return false
		}
		if criteria.IsError != nil && event.Item.IsError != *criteria.IsError {
			return false
		}
		return true
	case *AgentHandoffEvent:
		return criteria.NewAgent == nil || event.NewAgent == criteria.NewAgent
	default:
		return true
	}
}

func (a *RunAssert) SkipNext(count int) *RunAssert {
	if count < 0 {
		a.errors = append(a.errors, fmt.Errorf("cannot skip negative event count %d", count))
		return a
	}

	events := a.events()
	if a.index+count > len(events) {
		a.errors = append(a.errors, fmt.Errorf("tried to skip %d event(s), but only %d available", count, len(events)-a.index))
		a.index = len(events)
		return a
	}

	a.index += count
	return a
}

func (a *RunAssert) NoMoreEvents() *RunAssert {
	events := a.events()
	if a.index < len(events) {
		a.errors = append(a.errors, fmt.Errorf("expected no more events, but found %s", events[a.index].GetType()))
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
