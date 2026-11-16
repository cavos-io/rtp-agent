package worker

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type testSessionTransport struct {
	starts, closes int
	fail           error
}

func (*testSessionTransport) GetCallback() *RoomCallback                 { return nil }
func (*testSessionTransport) AttachRoom(*SDKRoom)                        {}
func (*testSessionTransport) ReconcileParticipants()                     {}
func (t *testSessionTransport) Start(context.Context) error              { t.starts++; return t.fail }
func (*testSessionTransport) StartRecorder(string, int) error            { return nil }
func (*testSessionTransport) StopRecorder() error                        { return nil }
func (*testSessionTransport) PopulateSessionReport(*agent.SessionReport) {}
func (t *testSessionTransport) Close() error                             { t.closes++; return nil }
func TestStartSessionExplicitTransportNeverJoinsPion(t *testing.T) {
	old := jobContextRoomConnector
	jobContextRoomConnector = workerlivekit.RoomConnector{Join: func(context.Context, *SDKRoom, string, lksdk.ConnectInfo, ...lksdk.ConnectOption) error {
		t.Fatal("custom transport joined Pion")
		return nil
	}}
	defer func() { jobContextRoomConnector = old }()
	for _, fail := range []error{nil, errors.New("native start failed")} {
		job := NewJobContext(&livekit.Job{Id: "explicit-transport", Room: &livekit.Room{Name: "room"}}, "ws://unused", "key", "secret")
		defer os.RemoveAll(job.SessionDirectory())
		prepared := job.PrepareRoom()
		session := agent.NewAgentSession(agent.NewAgent("test"), prepared, agent.AgentSessionOptions{})
		transport := &testSessionTransport{fail: fail}
		err := job.StartSession(context.Background(), session, StartSessionOptions{Transport: transport})
		if !errors.Is(err, fail) {
			t.Fatal(err)
		}
		if job.RoomConnected() || session.Room != nil || job.PrimaryRoomIO() != transport || transport.starts != 1 {
			t.Fatal("wrong room ownership")
		}
		if err := job.ConnectPreparedRoom(context.Background(), prepared); err == nil {
			t.Fatal("custom transport allowed a later Pion join")
		}
		if fail != nil && transport.closes != 1 {
			t.Fatal("failed startup did not close transport")
		}
		job.Shutdown("test done")
	}
}
