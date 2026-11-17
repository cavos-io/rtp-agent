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

type configurableSessionTransport struct {
	testSessionTransport
	options    RoomOptions
	configured bool
	configErr  error
}

func (t *configurableSessionTransport) ConfigureRoomOptions(o RoomOptions) error {
	if t.starts != 0 {
		return errors.New("configured after startup")
	}
	t.configured = true
	t.options = o
	return t.configErr
}
func TestStartSessionConfiguresCustomTransport(t *testing.T) {
	for _, configErr := range []error{nil, errors.New("unsupported option")} {
		job := NewJobContext(&livekit.Job{Id: "configured-transport", Room: &livekit.Room{Name: "room"}}, "ws://unused", "key", "secret")
		defer os.RemoveAll(job.SessionDirectory())
		session := agent.NewAgentSession(agent.NewAgent("test"), nil, agent.AgentSessionOptions{})
		transport := &configurableSessionTransport{configErr: configErr}
		next := &transportTestTextOutput{}
		opts := RoomOptions{DisablePreConnectAudio: true, DisableTranscriptionOutput: true, ParticipantIdentity: "caller", TranscriptionNextOutput: next}
		err := job.StartSession(context.Background(), session, StartSessionOptions{Transport: transport, RoomOptions: opts})
		if !errors.Is(err, configErr) {
			t.Fatal(err)
		}
		if !transport.configured || transport.options.TranscriptionNextOutput != next || transport.options.ParticipantIdentity != "caller" || !transport.options.DisableTranscriptionOutput || !transport.options.DisablePreConnectAudio {
			t.Fatal("RoomOptions not forwarded")
		}
		if configErr != nil && (transport.starts != 0 || transport.closes != 0) {
			t.Fatal("started with invalid options")
		}
		if configErr == nil && transport.starts != 1 {
			t.Fatal("valid transport not started")
		}
		job.Shutdown("test done")
	}
}

type transportTestTextOutput struct{}

func (*transportTestTextOutput) CaptureText(context.Context, string) error { return nil }
func (*transportTestTextOutput) Flush()                                    {}
