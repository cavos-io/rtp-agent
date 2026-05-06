package tools

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/library/logger"
)

type Shutter interface {
	Shutdown(reason string)
	DeleteRoom(ctx context.Context) error
}

type EndCallToolOptions struct {
	ExtraDescription string
	// DeleteRoom controls whether the room is deleted when the call ends.
	// Defaults to true when nil, matching Python's delete_room=True default.
	// Set to pointer-to-false to disable room deletion.
	DeleteRoom       *bool
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
	// Default DeleteRoom to true (matches Python's delete_room=True default)
	if opts.DeleteRoom == nil {
		t := true
		opts.DeleteRoom = &t
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

func (t *EndCallTool) Execute(ctx context.Context, args any) (any, error) {
	logger.Logger.Debugw("end_call tool invoked",
		"delete_room", t.opts.DeleteRoom != nil && *t.opts.DeleteRoom,
		"end_instructions", t.opts.EndInstructions,
	)

	if t.shutter == nil {
		logger.Logger.Errorw("end_call: shutter not available", fmt.Errorf("shutter not available"))
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
			logger.Logger.Debugw("end_call: waiting for playout to finish")
			_ = rc.WaitForPlayout(context.Background())
			logger.Logger.Debugw("end_call: playout finished")
		}

		if t.opts.DeleteRoom != nil && *t.opts.DeleteRoom {
			logger.Logger.Infow("end_call: deleting room")
			if err := t.shutter.DeleteRoom(context.Background()); err != nil {
				logger.Logger.Errorw("end_call: failed to delete room", err)
			} else {
				logger.Logger.Infow("end_call: room deleted successfully")
			}
		}

		logger.Logger.Infow("end_call: shutting down session", "reason", "user_initiated")
		t.shutter.Shutdown("user_initiated")
	}()

	if t.opts.OnToolCompleted != nil {
		t.opts.OnToolCompleted(rc, t.opts.EndInstructions)
	}

	logger.Logger.Debugw("end_call: returning end instructions to LLM", "output", t.opts.EndInstructions)

	// Return EndInstructions so the LLM receives it as the tool result and generates
	// the final reply accordingly (e.g. "say goodbye to the user").
	return t.opts.EndInstructions, nil
}

