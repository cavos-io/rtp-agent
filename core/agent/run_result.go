package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cavos-io/conversation-worker/core/evals"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type RunResult struct {
	ChatCtx   *llm.ChatContext
	Timeline  []TimelineEvent
	Timestamp float64
	Expect    *RunAssert
}

func NewRunResult(chatCtx *llm.ChatContext) *RunResult {
	timeline := make([]TimelineEvent, 0)
	if chatCtx != nil {
		for _, item := range chatCtx.Items {
			timeline = append(timeline, TimelineEvent{
				Type:      "chat_item:" + item.GetType(),
				Timestamp: float64(item.GetCreatedAt().UnixNano()) / 1e9,
				Payload: map[string]any{
					"id": item.GetID(),
				},
			})
		}
	}

	return &RunResult{
		ChatCtx:   chatCtx,
		Timeline:  timeline,
		Timestamp: float64(time.Now().UnixNano()) / 1e9,
		Expect:    &RunAssert{ChatCtx: chatCtx},
	}
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
