package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cavos-io/conversation-worker/core/evals"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type RunResult struct {
	ChatCtx *llm.ChatContext
	Expect  *RunAssert
}

func NewRunResult(chatCtx *llm.ChatContext) *RunResult {
	return &RunResult{
		ChatCtx: chatCtx,
		Expect:  &RunAssert{ChatCtx: chatCtx},
	}
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
