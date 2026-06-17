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
