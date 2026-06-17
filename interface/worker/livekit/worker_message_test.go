package livekit_test

import (
	"reflect"
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
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
