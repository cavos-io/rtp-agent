package worker

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

func TestStuckSessionEndDoesNotStopIncomingCalls(t *testing.T) {
	server := NewAgentServer(WorkerOptions{
		SessionEndTimeoutSeconds:    2,
		SessionEndTimeoutSecondsSet: true,
	})

	ready := make(chan struct{})
	sessionEndEntered := make(chan struct{})
	server.sessionEndFnc = func(*JobContext) error {
		close(sessionEndEntered)
		<-ready
		return nil
	}

	acceptedJobs := make(chan string, 1)
	server.requestFnc = func(req *JobRequest) error {
		acceptedJobs <- req.Job.Id
		return nil
	}

	stuckJob := NewJobContext(&livekit.Job{Id: "job-stuck"}, "", "", "")
	server.mu.Lock()
	server.activeJobs[stuckJob.Job.Id] = stuckJob
	server.mu.Unlock()

	frames := [][]byte{
		encodeServerMessageForTest(t, &livekit.ServerMessage{
			Message: &livekit.ServerMessage_Termination{
				Termination: &livekit.JobTermination{JobId: stuckJob.Job.Id},
			},
		}),
		encodeServerMessageForTest(t, &livekit.ServerMessage{
			Message: &livekit.ServerMessage_Availability{
				Availability: &livekit.AvailabilityRequest{Job: &livekit.Job{Id: "job-incoming"}},
			},
		}),
	}

	ctx := t.Context()
	drained := make(chan struct{})
	next := 0
	go func() {
		_ = server.runWorkerMessageLoop(
			ctx,
			func() (int, []byte, error) {
				if next < len(frames) {
					frame := frames[next]
					next++
					return websocket.BinaryMessage, frame, nil
				}
				<-drained
				return 0, nil, context.Canceled
			},
			func() error { return nil },
		)
	}()

	select {
	case <-sessionEndEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("job teardown never started")
	}

	select {
	case id := <-acceptedJobs:
		if id != "job-incoming" {
			t.Fatalf("job id = %q, want job-incoming", id)
		}
	case <-time.After(time.Second):
		t.Fatal("incoming call was never read: the message loop is held by a stuck teardown")
	}

	select {
	case <-ready:
		t.Fatal("teardown finished early; this test no longer covers the stuck case")
	default:
	}

	close(ready)
	close(drained)
}

func encodeServerMessageForTest(t *testing.T, msg *livekit.ServerMessage) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	return data
}
