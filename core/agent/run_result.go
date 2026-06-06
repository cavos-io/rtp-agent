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

	"github.com/cavos-io/rtp-agent/core/evals"
	"github.com/cavos-io/rtp-agent/core/llm"
)

var (
	ErrRunResultNotDone         = errors.New("run result is not done")
	ErrRunResultNoFinalOutput   = errors.New("run result has no final output")
	ErrRunResultFinalOutputType = errors.New("run result final output type mismatch")
)

type RunResult struct {
	ChatCtx         *llm.ChatContext
	Expect          *RunAssert
	userInput       string
	events          []RunEvent
	done            bool
	doneCh          chan struct{}
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
	return newRunResult(chatCtx, "", nil)
}

func NewRunResultWithOutputType(chatCtx *llm.ChatContext, outputType reflect.Type) *RunResult {
	result := NewRunResult(chatCtx)
	result.finalOutputType = outputType
	return result
}

func NewRunResultWithUserInput(chatCtx *llm.ChatContext, userInput string) *RunResult {
	return newRunResult(chatCtx, userInput, nil)
}

func newRunResultFromOptions(chatCtx *llm.ChatContext, userInput string, outputType reflect.Type) *RunResult {
	if userInput != "" && outputType == nil {
		return NewRunResultWithUserInput(chatCtx, userInput)
	}
	result := NewRunResultWithOutputType(chatCtx, outputType)
	result.userInput = userInput
	return result
}

func newRunResult(chatCtx *llm.ChatContext, userInput string, outputType reflect.Type) *RunResult {
	result := &RunResult{
		ChatCtx:         chatCtx,
		userInput:       userInput,
		events:          make([]RunEvent, 0),
		doneCh:          make(chan struct{}),
		finalOutputType: outputType,
		watchedSpeech:   make(map[*SpeechHandle]runResultSpeechWatch),
	}
	result.Expect = &RunAssert{ChatCtx: chatCtx, result: result}
	return result
}

func (r *RunResult) UserInput() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.userInput
}

func (r *RunResult) Done() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.done
}

func (r *RunResult) MarkDone() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.markDoneLocked()
}

func (r *RunResult) markDoneLocked() {
	if r.done {
		return
	}
	r.done = true
	close(r.doneCh)
}

func (r *RunResult) Wait(ctx context.Context) error {
	r.mu.Lock()
	doneCh := r.doneCh
	r.mu.Unlock()

	select {
	case <-doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *RunResult) SetFinalOutput(output any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.setFinalOutputLocked(output)
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
			r.setFinalOutputLocked(output)
		}
	}
	r.markDoneLocked()
}

func (r *RunResult) setFinalOutputLocked(output any) {
	r.finalOutput = nil
	r.finalOutputErr = nil
	if err, ok := output.(error); ok {
		r.finalOutputErr = err
	} else if r.finalOutputType != nil && reflect.TypeOf(output) != r.finalOutputType {
		r.finalOutputErr = fmt.Errorf("%w: expected %s, got %T", ErrRunResultFinalOutputType, r.finalOutputType, output)
	} else {
		r.finalOutput = output
	}
	r.finalOutputSet = true
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
	return a.ContainsFunctionCallWithArguments(name, arguments)
}

func (a *RunAssert) ContainsFunctionCall(name string) *RunAssert {
	return a.ContainsFunctionCallWithArguments(name, nil)
}

func (a *RunAssert) ContainsFunctionCallWithArguments(name string, arguments map[string]any) *RunAssert {
	return a.ContainsFunctionCallMatching(RunEventCriteria{
		Name:      name,
		Arguments: arguments,
	})
}

func (a *RunAssert) ContainsFunctionCallMatching(criteria RunEventCriteria) *RunAssert {
	found := false
	var argumentErr error
	for _, event := range a.events() {
		fcEvent, ok := event.(*FunctionCallEvent)
		if ok {
			if criteria.Name != "" && fcEvent.Item.Name != criteria.Name {
				continue
			}
			if len(criteria.Arguments) > 0 {
				if err := assertFunctionCallArguments(fcEvent.Item.Arguments, criteria.Arguments); err != nil {
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
			a.errors = append(a.errors, fmt.Errorf("expected function call matching criteria: %w", argumentErr))
		} else {
			a.errors = append(a.errors, errors.New("expected function call matching criteria, but not found"))
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
	return a.ContainsMessageMatching(RunEventCriteria{Role: role})
}

func (a *RunAssert) ContainsMessageMatching(criteria RunEventCriteria) *RunAssert {
	found := false
	for _, event := range a.events() {
		msgEvent, ok := event.(*ChatMessageEvent)
		if ok && eventMatchesCriteria(msgEvent, criteria) {
			found = true
			break
		}
	}
	if !found {
		a.errors = append(a.errors, errors.New("expected message matching criteria, but not found"))
	}
	return a
}

func (a *RunAssert) ContainsFunctionCallOutput(output string, isError bool) *RunAssert {
	return a.ContainsFunctionCallOutputMatching(RunEventCriteria{
		Output:  output,
		IsError: &isError,
	})
}

func (a *RunAssert) ContainsFunctionCallOutputMatching(criteria RunEventCriteria) *RunAssert {
	found := false
	for _, event := range a.events() {
		outputEvent, ok := event.(*FunctionCallOutputEvent)
		if ok && eventMatchesCriteria(outputEvent, criteria) {
			found = true
			break
		}
	}
	if !found {
		a.errors = append(a.errors, errors.New("expected function call output matching criteria, but not found"))
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

	if _, ok := a.nextEvent(expectedType); ok {
		return a
	}

	if expectedType == "" {
		a.errors = append(a.errors, errors.New("expected another event, but none left"))
	} else {
		a.errors = append(a.errors, fmt.Errorf("expected another event of type %s, but none found", expectedType))
	}
	return a
}

func (a *RunAssert) EventAt(index int, eventType string, criteria ...RunEventCriteria) *RunAssert {
	events := a.events()
	resolvedIndex := index
	if resolvedIndex < 0 {
		resolvedIndex += len(events)
	}
	if resolvedIndex < 0 || resolvedIndex >= len(events) {
		a.errors = append(a.errors, fmt.Errorf("event index %d out of range (total events: %d)", index, len(events)))
		return a
	}

	event := events[resolvedIndex]
	if eventType != "" && event.GetType() != eventType {
		a.errors = append(a.errors, fmt.Errorf("expected event at index %d to be %s, got %s", index, eventType, event.GetType()))
		return a
	}
	if len(criteria) > 0 && !eventMatchesCriteria(event, criteria[0]) {
		a.errors = append(a.errors, fmt.Errorf("expected event at index %d matching criteria", index))
	}
	return a
}

func (a *RunAssert) ContainsEventInRange(start int, end int, eventType string, criteria ...RunEventCriteria) *RunAssert {
	events := a.events()
	resolvedStart := resolveRunEventIndex(start, len(events))
	resolvedEnd := resolveRunEventIndex(end, len(events))
	if resolvedStart < 0 || resolvedEnd < 0 || resolvedStart > resolvedEnd || resolvedEnd > len(events) {
		a.errors = append(a.errors, fmt.Errorf("invalid event range [%d:%d] for %d event(s)", start, end, len(events)))
		return a
	}

	for _, event := range events[resolvedStart:resolvedEnd] {
		if eventType != "" && event.GetType() != eventType {
			continue
		}
		if len(criteria) > 0 && !eventMatchesCriteria(event, criteria[0]) {
			continue
		}
		return a
	}

	a.errors = append(a.errors, fmt.Errorf("expected event of type %s matching criteria in range [%d:%d]", eventType, start, end))
	return a
}

func resolveRunEventIndex(index int, length int) int {
	if index < 0 {
		return index + length
	}
	return index
}

func (a *RunAssert) NextMessage(role llm.ChatRole) *RunAssert {
	event, ok := a.nextEvent("message")
	if !ok {
		a.errors = append(a.errors, errors.New("expected message event, but none found"))
		return a
	}
	msgEvent, ok := event.(*ChatMessageEvent)
	if !ok {
		a.errors = append(a.errors, fmt.Errorf("expected message event, got %s", event.GetType()))
		return a
	}
	if role != "" && msgEvent.Item.Role != role {
		a.errors = append(a.errors, fmt.Errorf("expected message from %s, got %s", role, msgEvent.Item.Role))
	}
	return a
}

func (a *RunAssert) NextFunctionCall(name string) *RunAssert {
	return a.NextFunctionCallWithArguments(name, nil)
}

func (a *RunAssert) NextFunctionCallWithArguments(name string, arguments map[string]any) *RunAssert {
	event, ok := a.nextEvent("function_call")
	if !ok {
		a.errors = append(a.errors, errors.New("expected function_call event, but none found"))
		return a
	}
	fcEvent, ok := event.(*FunctionCallEvent)
	if !ok {
		a.errors = append(a.errors, fmt.Errorf("expected function_call event, got %s", event.GetType()))
		return a
	}
	if name != "" && fcEvent.Item.Name != name {
		a.errors = append(a.errors, fmt.Errorf("expected function call %q, got %q", name, fcEvent.Item.Name))
		return a
	}
	if len(arguments) > 0 {
		if err := assertFunctionCallArguments(fcEvent.Item.Arguments, arguments); err != nil {
			a.errors = append(a.errors, fmt.Errorf("expected function call %q with matching arguments: %w", name, err))
		}
	}
	return a
}

func (a *RunAssert) NextAgentHandoff(newAgent *Agent) *RunAssert {
	event, ok := a.nextEvent("agent_handoff")
	if !ok {
		a.errors = append(a.errors, errors.New("expected agent_handoff event, but none found"))
		return a
	}
	handoffEvent, ok := event.(*AgentHandoffEvent)
	if !ok {
		a.errors = append(a.errors, fmt.Errorf("expected agent_handoff event, got %s", event.GetType()))
		return a
	}
	if newAgent != nil && handoffEvent.NewAgent != newAgent {
		a.errors = append(a.errors, errors.New("expected agent handoff to target agent"))
	}
	return a
}

func (a *RunAssert) NextFunctionCallOutput(criteria RunEventCriteria) *RunAssert {
	event, ok := a.nextEvent("function_call_output")
	if !ok {
		a.errors = append(a.errors, errors.New("expected function_call_output event, but none found"))
		return a
	}
	outputEvent, ok := event.(*FunctionCallOutputEvent)
	if !ok {
		a.errors = append(a.errors, fmt.Errorf("expected function_call_output event, got %s", event.GetType()))
		return a
	}
	if !eventMatchesCriteria(outputEvent, criteria) {
		a.errors = append(a.errors, errors.New("expected function call output matching criteria"))
	}
	return a
}

func (a *RunAssert) nextEvent(expectedType string) (RunEvent, bool) {
	events := a.events()
	for a.index < len(events) {
		event := events[a.index]
		a.index++
		if expectedType == "" || event.GetType() == expectedType {
			return event, true
		}
	}
	return nil, false
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
	message := fmt.Sprintf("assertions failed:\n%s", strings.Join(msgs, "\n"))
	if events := a.events(); len(events) > 0 {
		message += "\nContext around failure:\n" + formatRunEvents(events, a.index)
	}
	return errors.New(message)
}

func formatRunEvents(events []RunEvent, selectedIndex int) string {
	lines := make([]string, 0, len(events))
	for i, event := range events {
		prefix := "   "
		if i == selectedIndex {
			prefix = ">>>"
		}
		lines = append(lines, fmt.Sprintf("%s [%d] %s%s", prefix, i, event.GetType(), formatRunEventDetails(event)))
	}
	return strings.Join(lines, "\n")
}

func formatRunEventDetails(event RunEvent) string {
	switch event := event.(type) {
	case *ChatMessageEvent:
		if event.Item == nil {
			return ""
		}
		details := []string{fmt.Sprintf("role=%s", event.Item.Role)}
		if text := event.Item.TextContent(); text != "" {
			details = append(details, fmt.Sprintf("content=%q", text))
		}
		return " " + strings.Join(details, " ")
	case *FunctionCallEvent:
		if event.Item == nil {
			return ""
		}
		details := []string{fmt.Sprintf("name=%s", event.Item.Name)}
		if event.Item.Arguments != "" {
			details = append(details, fmt.Sprintf("arguments=%s", event.Item.Arguments))
		}
		return " " + strings.Join(details, " ")
	case *FunctionCallOutputEvent:
		if event.Item == nil {
			return ""
		}
		return fmt.Sprintf(" name=%s output=%q is_error=%t", event.Item.Name, event.Item.Output, event.Item.IsError)
	case *AgentHandoffEvent:
		if event == nil {
			return ""
		}
		details := make([]string, 0, 2)
		if event.OldAgent != nil {
			details = append(details, fmt.Sprintf("old_agent=%s", event.OldAgent.Label()))
		}
		if event.NewAgent != nil {
			details = append(details, fmt.Sprintf("new_agent=%s", event.NewAgent.Label()))
		}
		if len(details) == 0 {
			return ""
		}
		return " " + strings.Join(details, " ")
	default:
		return ""
	}
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
