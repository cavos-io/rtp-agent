package tools

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/livekit/protocol/livekit"
)

func TestEndCallToolParametersUseStrictEmptyObjectSchema(t *testing.T) {
	tool := NewEndCallTool(nil, EndCallToolOptions{})

	params := tool.Parameters()

	want := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
		"required":             []string{},
	}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("Parameters() = %#v, want strict empty object schema", params)
	}
}

func TestSessionEndCallToolDeletesJobRoomFromRunContext(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	jobCtx := &fakeEndCallJobContext{
		room: &livekit.Room{Name: "room-a"},
		done: make(chan struct{}, 1),
	}
	session.SetJobContext(jobCtx)
	tool := NewSessionEndCallTool(session, EndCallToolOptions{DeleteRoom: true})
	runCtx := agent.NewRunContext(session, nil, nil)

	if _, err := tool.Execute(agent.WithRunContext(context.Background(), runCtx), `{}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	select {
	case <-jobCtx.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for DeleteRoom")
	}
	if jobCtx.roomName != "room-a" {
		t.Fatalf("DeleteRoom roomName = %q, want room-a", jobCtx.roomName)
	}
}

func TestSessionEndCallToolDeletesJobRoomByDefault(t *testing.T) {
	session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
	jobCtx := &fakeEndCallJobContext{
		room: &livekit.Room{Name: "room-a"},
		done: make(chan struct{}, 1),
	}
	session.SetJobContext(jobCtx)
	tool := NewSessionEndCallTool(session, EndCallToolOptions{})
	runCtx := agent.NewRunContext(session, nil, nil)

	if _, err := tool.Execute(agent.WithRunContext(context.Background(), runCtx), `{}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	select {
	case <-jobCtx.done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for default DeleteRoom")
	}
	if jobCtx.roomName != "room-a" {
		t.Fatalf("DeleteRoom roomName = %q, want room-a", jobCtx.roomName)
	}
}

func TestEndCallToolCanDisableRoomDeletion(t *testing.T) {
	shutter := &fakeEndCallShutter{
		deleted:  make(chan struct{}, 1),
		shutdown: make(chan string, 1),
	}
	tool := NewEndCallTool(shutter, EndCallToolOptions{DisableDeleteRoom: true})

	if _, err := tool.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	select {
	case <-shutter.shutdown:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Shutdown")
	}
	select {
	case <-shutter.deleted:
		t.Fatal("DeleteRoom was called despite DisableDeleteRoom")
	default:
	}
}

type fakeEndCallJobContext struct {
	room     *livekit.Room
	roomName string
	done     chan struct{}
}

func (f *fakeEndCallJobContext) RoomInfo() *livekit.Room {
	return f.room
}

func (f *fakeEndCallJobContext) DeleteRoom(_ context.Context, roomName string) (*livekit.DeleteRoomResponse, error) {
	f.roomName = roomName
	f.done <- struct{}{}
	return &livekit.DeleteRoomResponse{}, nil
}

type fakeEndCallShutter struct {
	deleted  chan struct{}
	shutdown chan string
}

func (f *fakeEndCallShutter) DeleteRoom(context.Context) error {
	f.deleted <- struct{}{}
	return nil
}

func (f *fakeEndCallShutter) Shutdown(reason string) {
	f.shutdown <- reason
}
