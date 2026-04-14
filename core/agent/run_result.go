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
}

type ChatMessageRunEvent struct {
	Item *llm.ChatMessage
}

func (e *ChatMessageRunEvent) RunEventType() string    { return "message" }
func (e *ChatMessageRunEvent) GetCreatedAt() time.Time { return e.Item.CreatedAt }

type FunctionCallRunEvent struct {
	Item *llm.FunctionCall
}

func (e *FunctionCallRunEvent) RunEventType() string    { return "function_call" }
func (e *FunctionCallRunEvent) GetCreatedAt() time.Time { return e.Item.CreatedAt }

type FunctionCallOutputRunEvent struct {
	Item *llm.FunctionCallOutput
}

func (e *FunctionCallOutputRunEvent) RunEventType() string    { return "function_call_output" }
func (e *FunctionCallOutputRunEvent) GetCreatedAt() time.Time { return e.Item.CreatedAt }

type AgentHandoffRunEvent struct {
	Item     *llm.AgentHandoff
	OldAgent AgentInterface
	NewAgent AgentInterface
}

func (e *AgentHandoffRunEvent) RunEventType() string    { return "agent_handoff" }
func (e *AgentHandoffRunEvent) GetCreatedAt() time.Time { return e.Item.CreatedAt }

type RunResult struct {
	ChatCtx   *llm.ChatContext
	Timeline  []TimelineEvent
	Timestamp float64
	Expect    *RunAssert

	mu          sync.Mutex
	handles     []*SpeechHandle
	waitCh      chan struct{}
	done        bool
	FinalOutput any
	finalError  error

	Events []RunEvent
}

func NewRunResult(chatCtx *llm.ChatContext) *RunResult {
	timeline := make([]TimelineEvent, 0)
	events := make([]RunEvent, 0)
	if chatCtx != nil {
		for _, item := range chatCtx.Items {
			timeline = append(timeline, TimelineEvent{
				Type:      "chat_item:" + item.GetType(),
				Timestamp: float64(item.GetCreatedAt().UnixNano()) / 1e9,
				Payload: map[string]any{
					"id": item.GetID(),
				},
			})

			// Capture initial context as events
			switch v := item.(type) {
			case *llm.ChatMessage:
				events = append(events, &ChatMessageRunEvent{Item: v})
			case *llm.FunctionCall:
				events = append(events, &FunctionCallRunEvent{Item: v})
			case *llm.FunctionCallOutput:
				events = append(events, &FunctionCallOutputRunEvent{Item: v})
			}
		}
	}

	return &RunResult{
		ChatCtx:   chatCtx,
		Timeline:  timeline,
		Timestamp: float64(time.Now().UnixNano()) / 1e9,
		Expect:    &RunAssert{ChatCtx: chatCtx},
		handles:   make([]*SpeechHandle, 0),
		waitCh:    make(chan struct{}),
		Events:    events,
	}
}

func (r *RunResult) AddEvent(ev RunEvent) {
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
func (r *RunResult) WatchHandle(handle *SpeechHandle) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done {
		return
	}
	r.handles = append(r.handles, handle)

	go func() {
		_ = handle.Wait(context.Background())
		r.checkDone()
	}()
}

func (r *RunResult) checkDone() {
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
		r.done = true

		// Grab final output from the last handle if available
		if len(r.handles) > 0 {
			lastHandle := r.handles[len(r.handles)-1]
			r.FinalOutput = lastHandle.FinalOutput
		}

		close(r.waitCh)
	}
}

func (r *RunResult) Wait(ctx context.Context) error {
	select {
	case <-r.waitCh:
		return r.finalError
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *RunResult) Done() <-chan struct{} {
	return r.waitCh
}

func (r *RunResult) AddTimelineEvent(eventType string, payload map[string]any) {
	if r == nil || eventType == "" {
		return
	}

	r.Timeline = append(r.Timeline, TimelineEvent{
		Type:      eventType,
		Timestamp: float64(time.Now().UnixNano()) / 1e9,
		Payload:   payload,
	})
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
