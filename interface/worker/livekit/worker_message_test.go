package livekit_test

import (
	"errors"
	"reflect"
	"testing"

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
}

func (w *recordingWorkerMessageWriter) WriteMessage(msgType int, data []byte) error {
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
