package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/conversation-worker/core/evals"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type RunEvent interface {
	RunEventType() string
	GetCreatedAt() time.Time
	GetItem() llm.ChatItem
}

type ChatMessageRunEvent struct {
	Item *llm.ChatMessage
}

func (e *ChatMessageRunEvent) RunEventType() string    { return "message" }
func (e *ChatMessageRunEvent) GetCreatedAt() time.Time { return e.Item.CreatedAt }
func (e *ChatMessageRunEvent) GetItem() llm.ChatItem   { return e.Item }

type FunctionCallRunEvent struct {
	Item *llm.FunctionCall
}

func (e *FunctionCallRunEvent) RunEventType() string    { return "function_call" }
func (e *FunctionCallRunEvent) GetCreatedAt() time.Time { return e.Item.CreatedAt }
func (e *FunctionCallRunEvent) GetItem() llm.ChatItem   { return e.Item }

type FunctionCallOutputRunEvent struct {
	Item *llm.FunctionCallOutput
}

func (e *FunctionCallOutputRunEvent) RunEventType() string    { return "function_call_output" }
func (e *FunctionCallOutputRunEvent) GetCreatedAt() time.Time { return e.Item.CreatedAt }
func (e *FunctionCallOutputRunEvent) GetItem() llm.ChatItem   { return e.Item }

type AgentHandoffRunEvent struct {
	Item     *llm.AgentHandoff
	OldAgent AgentInterface
	NewAgent AgentInterface
}

func (e *AgentHandoffRunEvent) RunEventType() string    { return "agent_handoff" }
func (e *AgentHandoffRunEvent) GetCreatedAt() time.Time { return e.Item.CreatedAt }
func (e *AgentHandoffRunEvent) GetItem() llm.ChatItem   { return e.Item }

type JobResult interface {
	Wait(ctx context.Context) error
	GetEvents() []RunEvent
}

type RunResult[T any] struct {
	ChatCtx   *llm.ChatContext
	Timestamp float64
	Expect    *RunAssert

	mu          sync.Mutex
	handles     []*SpeechHandle
	tasks       []<-chan struct{}
	waitCh      chan struct{}
	done        bool
	FinalOutput T
	finalError  error

	Events []RunEvent
}

func (r *RunResult[T]) GetEvents() []RunEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	out := make([]RunEvent, len(r.Events))
	copy(out, r.Events)
	return out
}

func NewRunResult[T any](chatCtx *llm.ChatContext) *RunResult[T] {
	return &RunResult[T]{
		ChatCtx:   chatCtx,
		Timestamp: float64(time.Now().UnixNano()) / 1e9,
		Expect:    &RunAssert{ChatCtx: chatCtx},
		handles:   make([]*SpeechHandle, 0),
		tasks:     make([]<-chan struct{}, 0),
		waitCh:    make(chan struct{}),
		Events:    make([]RunEvent, 0),
	}
}

func (r *RunResult[T]) WatchTask(done <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.tasks = append(r.tasks, done)
	
	go func() {
		<-done
		r.checkDone()
	}()
}

func (r *RunResult[T]) WaitAny(ctx context.Context) (T, error) {
	if err := r.Wait(ctx); err != nil {
		var zero T
		return zero, err
	}
	return r.FinalOutput, nil
}

func (r *RunResult[T]) AddEvent(ev RunEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}

	// Binary search for insertion point to maintain order by CreatedAt
	idx := sort.Search(len(r.Events), func(i int) bool {
		return r.Events[i].GetCreatedAt().After(ev.GetCreatedAt())
	})

	r.Events = append(r.Events, nil)
	copy(r.Events[idx+1:], r.Events[idx:])
	r.Events[idx] = ev
}

func (r *RunResult[T]) WatchHandle(ctx context.Context, handle *SpeechHandle) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.handles = append(r.handles, handle)

	handle.OnItemAdded = func(item llm.ChatItem) {
		switch v := item.(type) {
		case *llm.ChatMessage:
			r.AddEvent(&ChatMessageRunEvent{Item: v})
		case *llm.FunctionCall:
			r.AddEvent(&FunctionCallRunEvent{Item: v})
		case *llm.FunctionCallOutput:
			r.AddEvent(&FunctionCallOutputRunEvent{Item: v})
		case *llm.AgentHandoff:
			r.AddEvent(&AgentHandoffRunEvent{Item: v})
		}
	}

	go func() {
		_ = handle.Wait(ctx)
		r.checkDone()
	}()
}

func (r *RunResult[T]) checkDone() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.done {
		return
	}

	allDone := true
	for _, h := range r.handles {
		if !h.IsDone() {
			allDone = false
			break
		}
	}

	if allDone {
		for _, t := range r.tasks {
			select {
			case <-t:
			default:
				allDone = false
				break
			}
		}
	}

	if allDone {
		r.done = true

		// Grab final output and error from the last handle if available
		if len(r.handles) > 0 {
			lastHandle := r.handles[len(r.handles)-1]
			
			if lastHandle.FinalOutput != nil {
				if val, ok := lastHandle.FinalOutput.(T); ok {
					r.FinalOutput = val
				} else {
					var zero T
					r.finalError = fmt.Errorf("expected output of type %T, got %T", zero, lastHandle.FinalOutput)
				}
			}
			
			if r.finalError == nil {
				r.finalError = lastHandle.Error()
			}
		}

		close(r.waitCh)
	}
}

func (r *RunResult[T]) Wait(ctx context.Context) error {
	select {
	case <-r.waitCh:
		return r.finalError
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetOutput returns the strictly typed final output of the run, blocking until completion.
func GetOutput[T any](ctx context.Context, r *RunResult[T]) (T, error) {
	return r.WaitAny(ctx)
}

func (r *RunResult[T]) Done() <-chan struct{} {
	return r.waitCh
}

func (r *RunResult[T]) Eval(ctx context.Context, evaluator evals.Evaluator, llmInstance llm.LLM) (*evals.JudgmentResult, error) {
	if err := r.Wait(ctx); err != nil {
		return nil, err
	}
	return evaluator.Evaluate(ctx, r.ChatCtx, nil, llmInstance)
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
