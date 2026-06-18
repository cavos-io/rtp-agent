package livekit_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/gorilla/websocket"
	lkprotocol "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

func TestJobStatusMessageCarriesJobStatus(t *testing.T) {
	msg := workerlivekit.JobStatusMessage("job-a", lkprotocol.JobStatus_JS_RUNNING)

	update := msg.GetUpdateJob()
	if update == nil {
		t.Fatal("UpdateJob message is nil")
	}
	if update.JobId != "job-a" {
		t.Fatalf("UpdateJob.JobId = %q, want job-a", update.JobId)
	}
	if update.Status != lkprotocol.JobStatus_JS_RUNNING {
		t.Fatalf("UpdateJob.Status = %v, want JS_RUNNING", update.Status)
	}
}

func TestWorkerMessageAliasUsesLiveKitProtocolMessage(t *testing.T) {
	msg := &workerlivekit.WorkerMessage{
		Message: &lkprotocol.WorkerMessage_UpdateJob{
			UpdateJob: &lkprotocol.UpdateJobStatus{JobId: "job-a"},
		},
	}

	if msg.GetUpdateJob().GetJobId() != "job-a" {
		t.Fatalf("JobId = %q, want job-a", msg.GetUpdateJob().GetJobId())
	}
}

func TestJobRunningMessageReportsRunningStatus(t *testing.T) {
	msg := workerlivekit.JobRunningMessage("job-a")

	update := msg.GetUpdateJob()
	if update == nil {
		t.Fatal("UpdateJob message is nil")
	}
	if update.JobId != "job-a" {
		t.Fatalf("UpdateJob.JobId = %q, want job-a", update.JobId)
	}
	if update.Status != lkprotocol.JobStatus_JS_RUNNING {
		t.Fatalf("UpdateJob.Status = %v, want JS_RUNNING", update.Status)
	}
}

func TestJobStatusForEntrypointResultReportsSuccessWhenEntrypointClean(t *testing.T) {
	got := workerlivekit.JobStatusForEntrypointResult(nil, nil)
	if got != lkprotocol.JobStatus_JS_SUCCESS {
		t.Fatalf("JobStatusForEntrypointResult(nil, nil) = %v, want JS_SUCCESS", got)
	}
}

func TestJobStatusForEntrypointResultReportsFailedOnErrorOrPanic(t *testing.T) {
	if got := workerlivekit.JobStatusForEntrypointResult(errors.New("boom"), nil); got != lkprotocol.JobStatus_JS_FAILED {
		t.Fatalf("JobStatusForEntrypointResult(error, nil) = %v, want JS_FAILED", got)
	}
	if got := workerlivekit.JobStatusForEntrypointResult(nil, "panic"); got != lkprotocol.JobStatus_JS_FAILED {
		t.Fatalf("JobStatusForEntrypointResult(nil, panic) = %v, want JS_FAILED", got)
	}
}

func TestJobStatusSucceededIdentifiesSuccessOnly(t *testing.T) {
	if !workerlivekit.JobStatusSucceeded(lkprotocol.JobStatus_JS_SUCCESS) {
		t.Fatal("JobStatusSucceeded(JS_SUCCESS) = false, want true")
	}
	if workerlivekit.JobStatusSucceeded(lkprotocol.JobStatus_JS_FAILED) {
		t.Fatal("JobStatusSucceeded(JS_FAILED) = true, want false")
	}
}

func TestJobCompletionPlanForEntrypointWaitsThenSendsSuccess(t *testing.T) {
	plan := workerlivekit.JobCompletionPlanForEntrypoint(lkprotocol.JobStatus_JS_SUCCESS, false)

	if !plan.Finish {
		t.Fatal("Finish = false, want true")
	}
	if !plan.WaitForShutdown {
		t.Fatal("WaitForShutdown = false, want true")
	}
	if !plan.SendStatus {
		t.Fatal("SendStatus = false, want true")
	}
	if !plan.SendAfterFinish {
		t.Fatal("SendAfterFinish = false, want true")
	}
}

func TestJobCompletionPlanForEntrypointSendsFailureBeforeFinish(t *testing.T) {
	plan := workerlivekit.JobCompletionPlanForEntrypoint(lkprotocol.JobStatus_JS_FAILED, false)

	if !plan.Finish {
		t.Fatal("Finish = false, want true")
	}
	if plan.WaitForShutdown {
		t.Fatal("WaitForShutdown = true, want false")
	}
	if !plan.SendStatus {
		t.Fatal("SendStatus = false, want true")
	}
	if plan.SendAfterFinish {
		t.Fatal("SendAfterFinish = true, want false")
	}
}

func TestJobCompletionPlanForEntrypointSkipsStatusWhenTerminated(t *testing.T) {
	plan := workerlivekit.JobCompletionPlanForEntrypoint(lkprotocol.JobStatus_JS_SUCCESS, true)

	if !plan.Finish {
		t.Fatal("Finish = false, want true")
	}
	if plan.WaitForShutdown {
		t.Fatal("WaitForShutdown = true, want false")
	}
	if plan.SendStatus {
		t.Fatal("SendStatus = true, want false")
	}
	if plan.SendAfterFinish {
		t.Fatal("SendAfterFinish = true, want false")
	}
}

func TestRunEntrypointReportsSuccess(t *testing.T) {
	result := workerlivekit.RunEntrypoint(func() error {
		return nil
	})

	if result.Status != lkprotocol.JobStatus_JS_SUCCESS {
		t.Fatalf("RunEntrypoint().Status = %v, want JS_SUCCESS", result.Status)
	}
	if result.Err != nil {
		t.Fatalf("RunEntrypoint().Err = %v, want nil", result.Err)
	}
	if result.Recovered != nil {
		t.Fatalf("RunEntrypoint().Recovered = %v, want nil", result.Recovered)
	}
}

func TestRunEntrypointReportsError(t *testing.T) {
	errBoom := errors.New("boom")

	result := workerlivekit.RunEntrypoint(func() error {
		return errBoom
	})

	if result.Status != lkprotocol.JobStatus_JS_FAILED {
		t.Fatalf("RunEntrypoint(error).Status = %v, want JS_FAILED", result.Status)
	}
	if result.Err != errBoom {
		t.Fatalf("RunEntrypoint(error).Err = %v, want boom", result.Err)
	}
	if result.Recovered != nil {
		t.Fatalf("RunEntrypoint(error).Recovered = %v, want nil", result.Recovered)
	}
}

func TestRunEntrypointRecoversPanic(t *testing.T) {
	result := workerlivekit.RunEntrypoint(func() error {
		panic("panic boom")
	})

	if result.Status != lkprotocol.JobStatus_JS_FAILED {
		t.Fatalf("RunEntrypoint(panic).Status = %v, want JS_FAILED", result.Status)
	}
	if result.Err != nil {
		t.Fatalf("RunEntrypoint(panic).Err = %v, want nil", result.Err)
	}
	if result.Recovered != "panic boom" {
		t.Fatalf("RunEntrypoint(panic).Recovered = %v, want panic boom", result.Recovered)
	}
}

func TestRunJobEntrypointLifecycleWaitsForShutdownThenFinishesBeforeSuccessStatus(t *testing.T) {
	var events []string
	shutdown := make(chan struct{})
	done := make(chan struct{})

	go func() {
		workerlivekit.RunJobEntrypointLifecycle(workerlivekit.JobEntrypointLifecycleOptions{
			Context: context.Background(),
			Entrypoint: func() error {
				events = append(events, "entrypoint")
				return nil
			},
			MarkDone: func() {
				events = append(events, "done")
			},
			OnResult: func(result workerlivekit.EntrypointResult) {
				events = append(events, "result:"+result.Status.String())
			},
			ShutdownDone: shutdown,
			Finish: func() bool {
				events = append(events, "finish")
				return true
			},
			SendStatus: func(status lkprotocol.JobStatus) error {
				events = append(events, "status:"+status.String())
				return nil
			},
		})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("RunJobEntrypointLifecycle returned before shutdown")
	case <-time.After(20 * time.Millisecond):
	}

	close(shutdown)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunJobEntrypointLifecycle did not return after shutdown")
	}

	want := []string{"entrypoint", "done", "result:JS_SUCCESS", "finish", "status:JS_SUCCESS"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestRunJobEntrypointLifecycleSendsFailureStatusBeforeFinish(t *testing.T) {
	errBoom := errors.New("boom")
	var events []string

	workerlivekit.RunJobEntrypointLifecycle(workerlivekit.JobEntrypointLifecycleOptions{
		Context: context.Background(),
		Entrypoint: func() error {
			return errBoom
		},
		OnResult: func(result workerlivekit.EntrypointResult) {
			if result.Err != errBoom {
				t.Fatalf("result.Err = %v, want boom", result.Err)
			}
			events = append(events, "result:"+result.Status.String())
		},
		Finish: func() bool {
			events = append(events, "finish")
			return true
		},
		SendStatus: func(status lkprotocol.JobStatus) error {
			events = append(events, "status:"+status.String())
			return nil
		},
	})

	want := []string{"result:JS_FAILED", "status:JS_FAILED", "finish"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestRunJobEntrypointLifecycleSkipsStatusWhenTerminated(t *testing.T) {
	var events []string

	workerlivekit.RunJobEntrypointLifecycle(workerlivekit.JobEntrypointLifecycleOptions{
		Context: context.Background(),
		Entrypoint: func() error {
			return nil
		},
		Terminated: func() bool {
			return true
		},
		Finish: func() bool {
			events = append(events, "finish")
			return true
		},
		SendStatus: func(status lkprotocol.JobStatus) error {
			events = append(events, "status:"+status.String())
			return nil
		},
	})

	want := []string{"finish"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestMigrateJobMessageCarriesJobIDs(t *testing.T) {
	jobIDs := []string{"job-a", "job-b"}
	msg := workerlivekit.MigrateJobMessage(jobIDs)

	migrate := msg.GetMigrateJob()
	if migrate == nil {
		t.Fatal("MigrateJob message is nil")
	}
	if !reflect.DeepEqual(migrate.JobIds, jobIDs) {
		t.Fatalf("MigrateJob.JobIds = %#v, want %#v", migrate.JobIds, jobIDs)
	}
}

func TestMigrateJobMessageSortsJobIDsWithoutMutatingInput(t *testing.T) {
	jobIDs := []string{"job-b", "job-a"}
	msg := workerlivekit.MigrateJobMessage(jobIDs)

	migrate := msg.GetMigrateJob()
	if migrate == nil {
		t.Fatal("MigrateJob message is nil")
	}
	want := []string{"job-a", "job-b"}
	if !reflect.DeepEqual(migrate.JobIds, want) {
		t.Fatalf("MigrateJob.JobIds = %#v, want %#v", migrate.JobIds, want)
	}
	if !reflect.DeepEqual(jobIDs, []string{"job-b", "job-a"}) {
		t.Fatalf("input jobIDs = %#v, want original order", jobIDs)
	}
}

func TestMigratableRunningJobIDsSkipsFakeAndEmptyJobs(t *testing.T) {
	jobs := []workerlivekit.RunningJobInfo{
		{Job: &lkprotocol.Job{Id: "job-b"}},
		{Job: &lkprotocol.Job{Id: "job-local"}, FakeJob: true},
		{},
		{Job: &lkprotocol.Job{Id: "job-a"}},
	}

	got := workerlivekit.MigratableRunningJobIDs(jobs)
	want := []string{"job-a", "job-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MigratableRunningJobIDs() = %#v, want %#v", got, want)
	}
}

func TestMigrateRunningJobsMessageUsesMigratableRunningJobs(t *testing.T) {
	msg := workerlivekit.MigrateRunningJobsMessage([]workerlivekit.RunningJobInfo{
		{Job: &lkprotocol.Job{Id: "job-b"}},
		{Job: &lkprotocol.Job{Id: "job-local"}, FakeJob: true},
		{Job: &lkprotocol.Job{Id: "job-a"}},
	})

	migrate := msg.GetMigrateJob()
	if migrate == nil {
		t.Fatal("MigrateJob message is nil")
	}
	want := []string{"job-a", "job-b"}
	if !reflect.DeepEqual(migrate.JobIds, want) {
		t.Fatalf("MigrateJob.JobIds = %#v, want %#v", migrate.JobIds, want)
	}
}

func TestWorkerStatusMessageCarriesStatusLoadAndJobCount(t *testing.T) {
	msg := workerlivekit.WorkerStatusMessage(lkprotocol.WorkerStatus_WS_AVAILABLE, 0.42, 2)

	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("UpdateWorker message is nil")
	}
	if update.GetStatus() != lkprotocol.WorkerStatus_WS_AVAILABLE {
		t.Fatalf("UpdateWorker.Status = %v, want WS_AVAILABLE", update.GetStatus())
	}
	if update.Load != 0.42 {
		t.Fatalf("UpdateWorker.Load = %v, want 0.42", update.Load)
	}
	if update.JobCount != 2 {
		t.Fatalf("UpdateWorker.JobCount = %d, want 2", update.JobCount)
	}
}

func TestWorkerMessageWebSocketFrameUsesBinaryMessage(t *testing.T) {
	msgType, data, err := workerlivekit.WorkerMessageWebSocketFrame(workerlivekit.JobRunningMessage("job-a"))
	if err != nil {
		t.Fatalf("WorkerMessageWebSocketFrame() error = %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("message type = %d, want websocket.BinaryMessage", msgType)
	}
	if len(data) == 0 {
		t.Fatal("frame data is empty")
	}
}

type recordingWorkerMessageWriter struct {
	msgType int
	data    []byte
	err     error
}

func (w *recordingWorkerMessageWriter) WriteMessage(msgType int, data []byte) error {
	if w.err != nil {
		return w.err
	}
	w.msgType = msgType
	w.data = append([]byte(nil), data...)
	return nil
}

func TestWriteWorkerMessageWebSocketWritesBinaryFrame(t *testing.T) {
	writer := &recordingWorkerMessageWriter{}

	if err := workerlivekit.WriteWorkerMessageWebSocket(writer, workerlivekit.JobRunningMessage("job-a")); err != nil {
		t.Fatalf("WriteWorkerMessageWebSocket() error = %v", err)
	}
	if writer.msgType != websocket.BinaryMessage {
		t.Fatalf("message type = %d, want websocket.BinaryMessage", writer.msgType)
	}
	var decoded lkprotocol.WorkerMessage
	if err := proto.Unmarshal(writer.data, &decoded); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}
	if decoded.GetUpdateJob().GetJobId() != "job-a" {
		t.Fatalf("decoded job id = %q, want job-a", decoded.GetUpdateJob().GetJobId())
	}
}

type initialRegisterWebSocket struct {
	recordingWorkerMessageWriter
	readType int
	readData []byte
	readErr  error
}

func (ws *initialRegisterWebSocket) ReadMessage() (int, []byte, error) {
	return ws.readType, ws.readData, ws.readErr
}

func TestExchangeInitialRegisterWebSocketWritesRegisterAndReadsResponse(t *testing.T) {
	response := &lkprotocol.ServerMessage{
		Message: &lkprotocol.ServerMessage_Register{
			Register: &lkprotocol.RegisterWorkerResponse{WorkerId: "worker-a"},
		},
	}
	responseData, err := proto.Marshal(response)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	ws := &initialRegisterWebSocket{
		readType: websocket.BinaryMessage,
		readData: responseData,
	}

	msg, err := workerlivekit.ExchangeInitialRegisterWebSocket(ws, workerlivekit.RegisterWorkerMessage(workerlivekit.WorkerRegistrationOptions{
		WorkerType: "room",
	}))
	if err != nil {
		t.Fatalf("ExchangeInitialRegisterWebSocket() error = %v", err)
	}
	if ws.msgType != websocket.BinaryMessage {
		t.Fatalf("written message type = %d, want websocket.BinaryMessage", ws.msgType)
	}
	if msg.GetRegister().GetWorkerId() != "worker-a" {
		t.Fatalf("register worker id = %q, want worker-a", msg.GetRegister().GetWorkerId())
	}
}

type workerMessageLoopReader struct {
	frames []workerMessageLoopFrame
	closed chan struct{}
}

type workerMessageLoopFrame struct {
	msgType int
	data    []byte
	err     error
}

func (r *workerMessageLoopReader) ReadMessage() (int, []byte, error) {
	if len(r.frames) == 0 {
		<-r.closed
		return 0, nil, io.EOF
	}
	frame := r.frames[0]
	r.frames = r.frames[1:]
	return frame.msgType, frame.data, frame.err
}

func encodeServerMessage(t *testing.T, msg *lkprotocol.ServerMessage) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	return data
}

func TestRunWorkerMessageLoopDispatchesDecodedServerMessages(t *testing.T) {
	reader := &workerMessageLoopReader{
		frames: []workerMessageLoopFrame{
			{
				msgType: websocket.BinaryMessage,
				data: encodeServerMessage(t, &lkprotocol.ServerMessage{
					Message: &lkprotocol.ServerMessage_Register{
						Register: &lkprotocol.RegisterWorkerResponse{WorkerId: "worker-a"},
					},
				}),
			},
			{err: io.EOF},
		},
		closed: make(chan struct{}),
	}
	var got []*workerlivekit.ServerMessage

	err := workerlivekit.RunWorkerMessageLoop(context.Background(), workerlivekit.WorkerMessageLoopOptions{
		Reader: reader,
		Handle: func(msg *workerlivekit.ServerMessage) {
			got = append(got, msg)
		},
	})

	if !errors.Is(err, io.EOF) {
		t.Fatalf("RunWorkerMessageLoop() error = %v, want EOF", err)
	}
	if len(got) != 1 {
		t.Fatalf("handled messages = %d, want 1", len(got))
	}
	if got[0].GetRegister().GetWorkerId() != "worker-a" {
		t.Fatalf("worker id = %q, want worker-a", got[0].GetRegister().GetWorkerId())
	}
}

func TestRunWorkerMessageLoopSkipsDecodeErrorsAndContinues(t *testing.T) {
	reader := &workerMessageLoopReader{
		frames: []workerMessageLoopFrame{
			{msgType: websocket.BinaryMessage, data: []byte("not-protobuf")},
			{
				msgType: websocket.BinaryMessage,
				data: encodeServerMessage(t, &lkprotocol.ServerMessage{
					Message: &lkprotocol.ServerMessage_Availability{
						Availability: &lkprotocol.AvailabilityRequest{Job: &lkprotocol.Job{Id: "job-a"}},
					},
				}),
			},
			{err: io.EOF},
		},
		closed: make(chan struct{}),
	}
	decodeErrors := 0
	handled := 0

	err := workerlivekit.RunWorkerMessageLoop(context.Background(), workerlivekit.WorkerMessageLoopOptions{
		Reader: reader,
		Handle: func(msg *workerlivekit.ServerMessage) {
			handled++
		},
		OnDecodeError: func(error) {
			decodeErrors++
		},
	})

	if !errors.Is(err, io.EOF) {
		t.Fatalf("RunWorkerMessageLoop() error = %v, want EOF", err)
	}
	if decodeErrors != 1 {
		t.Fatalf("decode errors = %d, want 1", decodeErrors)
	}
	if handled != 1 {
		t.Fatalf("handled messages = %d, want 1", handled)
	}
}

func TestRunWorkerMessageLoopClosesReaderOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	closed := make(chan struct{})
	reader := &workerMessageLoopReader{closed: closed}

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- workerlivekit.RunWorkerMessageLoop(ctx, workerlivekit.WorkerMessageLoopOptions{
			Reader: reader,
			Close: func() error {
				close(closed)
				return nil
			},
		})
	}()

	cancel()

	select {
	case err := <-doneCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunWorkerMessageLoop() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunWorkerMessageLoop() did not return after context cancellation")
	}
}

func TestAvailableWorkerStatusMessageReportsFullWhenWorkerCannotAcceptJob(t *testing.T) {
	msg := workerlivekit.AvailableWorkerStatusMessage(0.91, 4, false)

	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("UpdateWorker message is nil")
	}
	if update.GetStatus() != lkprotocol.WorkerStatus_WS_FULL {
		t.Fatalf("UpdateWorker.Status = %v, want WS_FULL", update.GetStatus())
	}
	if update.Load != 0.91 {
		t.Fatalf("UpdateWorker.Load = %v, want 0.91", update.Load)
	}
	if update.JobCount != 4 {
		t.Fatalf("UpdateWorker.JobCount = %d, want 4", update.JobCount)
	}
}

func TestAvailableWorkerStatusMessageReportsAvailableWhenWorkerCanAcceptJob(t *testing.T) {
	msg := workerlivekit.AvailableWorkerStatusMessage(0.21, 1, true)

	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("UpdateWorker message is nil")
	}
	if update.GetStatus() != lkprotocol.WorkerStatus_WS_AVAILABLE {
		t.Fatalf("UpdateWorker.Status = %v, want WS_AVAILABLE", update.GetStatus())
	}
	if update.Load != 0.21 {
		t.Fatalf("UpdateWorker.Load = %v, want 0.21", update.Load)
	}
	if update.JobCount != 1 {
		t.Fatalf("UpdateWorker.JobCount = %d, want 1", update.JobCount)
	}
}

func TestDrainingWorkerStatusMessageReportsFullWithoutLoad(t *testing.T) {
	msg := workerlivekit.DrainingWorkerStatusMessage(3)

	update := msg.GetUpdateWorker()
	if update == nil {
		t.Fatal("UpdateWorker message is nil")
	}
	if update.GetStatus() != lkprotocol.WorkerStatus_WS_FULL {
		t.Fatalf("UpdateWorker.Status = %v, want WS_FULL", update.GetStatus())
	}
	if update.Load != 0 {
		t.Fatalf("UpdateWorker.Load = %v, want 0", update.Load)
	}
	if update.JobCount != 3 {
		t.Fatalf("UpdateWorker.JobCount = %d, want 3", update.JobCount)
	}
}
