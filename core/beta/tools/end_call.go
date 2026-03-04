package tools

import (
	"context"
	"fmt"

	"github.com/cavos-io/conversation-worker/core/agent"
)

type Shutter interface {
	Shutdown(reason string)
	DeleteRoom(ctx context.Context) error
}

type EndCallToolOptions struct {
	ExtraDescription string
	DeleteRoom       bool
	EndInstructions  string
	OnToolCalled     func(ctx *agent.RunContext)
	OnToolCompleted  func(ctx *agent.RunContext, output string)
}

type EndCallTool struct {
	shutter Shutter
	opts    EndCallToolOptions
}

func NewEndCallTool(shutter Shutter, opts EndCallToolOptions) *EndCallTool {
	if opts.EndInstructions == "" {
		opts.EndInstructions = "say goodbye to the user"
	}
	return &EndCallTool{
		shutter: shutter,
		opts:    opts,
	}
}

func (t *EndCallTool) ID() string {
	return "end_call"
}

func (t *EndCallTool) Name() string {
	return "end_call"
}

const endCallDescription = `Ends the current call and disconnects immediately.

Call when:
- The user clearly indicates they are done (e.g., “that’s all, bye”).
- The agent determines the conversation is complete and should end.

Do not call when:
- The user asks to pause, hold, or transfer.
- Intent is unclear.

This is the final action the agent can take.
Once called, no further interaction is possible with the user.
Don't generate any other text or response when the tool is called.`

func (t *EndCallTool) Description() string {
	desc := endCallDescription
	if t.opts.ExtraDescription != "" {
		desc += "\n" + t.opts.ExtraDescription
	}
	return desc
}

func (t *EndCallTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *EndCallTool) Execute(ctx context.Context, args string) (string, error) {
	if t.shutter == nil {
		return "", fmt.Errorf("shutter not available")
	}

	rc := agent.GetRunContext(ctx)

	if t.opts.OnToolCalled != nil {
		t.opts.OnToolCalled(rc)
	}

	// Python implementation handles delayed shutdown for RealtimeModel
	// In Go, we'll trigger shutdown and optionally delete room
	go func() {
		// Wait for playout if possible
		if rc != nil {
			_ = rc.WaitForPlayout(context.Background())
		}

		if t.opts.DeleteRoom {
			_ = t.shutter.DeleteRoom(context.Background())
		}

		t.shutter.Shutdown("user_initiated")
	}()

	if t.opts.OnToolCompleted != nil {
		t.opts.OnToolCompleted(rc, t.opts.EndInstructions)
	}

	return t.opts.EndInstructions, nil
}
