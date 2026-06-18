package livekit_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestLocalJobOptionsOwnsLiveKitRoomAndRecordingFields(t *testing.T) {
	room := &lkprotocol.Room{Name: "room-a"}
	opts := workerlivekit.LocalJobOptions{
		FakeJob:           false,
		RoomInfo:          room,
		Token:             "room-token",
		RecordingOptions:  agent.RecordingOptions{Audio: true},
		SessionReportPath: "reports/session.json",
		SessionDirectory:  "sessions/job-a",
	}

	if opts.RoomInfo != room {
		t.Fatalf("RoomInfo = %p, want %p", opts.RoomInfo, room)
	}
	if !opts.RecordingOptions.Audio {
		t.Fatal("RecordingOptions.Audio = false, want true")
	}
	if opts.Token != "room-token" {
		t.Fatalf("Token = %q, want room-token", opts.Token)
	}
}

func TestDefaultFakeLocalJobOptionsUsesReferenceFakeJobMode(t *testing.T) {
	opts := workerlivekit.DefaultFakeLocalJobOptions()

	if !opts.FakeJob {
		t.Fatal("DefaultFakeLocalJobOptions().FakeJob = false, want true")
	}
	if opts.RoomInfo != nil {
		t.Fatalf("DefaultFakeLocalJobOptions().RoomInfo = %#v, want nil", opts.RoomInfo)
	}
	if opts.Token != "" {
		t.Fatalf("DefaultFakeLocalJobOptions().Token = %q, want empty", opts.Token)
	}
}

func TestLocalJobContextValuesBuildsLiveKitJobIdentityAndToken(t *testing.T) {
	values := workerlivekit.LocalJobContextValues(workerlivekit.LocalJobContextValueOptions{
		RoomName:            "room-a",
		ParticipantIdentity: "",
		APIKey:              "api-key",
		APISecret:           "api-secret",
		TTL:                 time.Hour,
		Options: workerlivekit.LocalJobOptions{
			FakeJob: true,
		},
		NewIdentity: func(prefix string) string {
			if prefix != "fake-agent-" {
				t.Fatalf("NewIdentity prefix = %q, want fake-agent-", prefix)
			}
			return "fake-agent-id"
		},
	})

	if values.Job.GetRoom().GetName() != "room-a" {
		t.Fatalf("Job.Room.Name = %q, want room-a", values.Job.GetRoom().GetName())
	}
	if values.ParticipantIdentity != "fake-agent-id" {
		t.Fatalf("ParticipantIdentity = %q, want fake-agent-id", values.ParticipantIdentity)
	}
	if values.Token == "" {
		t.Fatal("Token = empty, want generated participant token")
	}
}

func TestValidateLocalJobRunOptionsChecksIdentityBeforeRoomInfo(t *testing.T) {
	err := workerlivekit.ValidateLocalJobRunOptions("", workerlivekit.LocalJobOptions{FakeJob: false})
	if err == nil {
		t.Fatal("ValidateLocalJobRunOptions() error = nil, want missing identity")
	}
	if got, want := err.Error(), "agent_identity is None but fake_job is False"; got != want {
		t.Fatalf("ValidateLocalJobRunOptions() error = %q, want %q", got, want)
	}
}

func TestPrepareLocalJobRunOptionsUsesExplicitIdentity(t *testing.T) {
	identity, err := workerlivekit.PrepareLocalJobRunOptions("agent-local", workerlivekit.DefaultFakeLocalJobOptions())
	if err != nil {
		t.Fatalf("PrepareLocalJobRunOptions() error = %v", err)
	}
	if identity != "agent-local" {
		t.Fatalf("identity = %q, want agent-local", identity)
	}
}

func TestPrepareLocalJobRunOptionsRejectsInvalidTokenBeforeValidation(t *testing.T) {
	_, err := workerlivekit.PrepareLocalJobRunOptions("agent-local", workerlivekit.LocalJobOptions{
		FakeJob: false,
		Token:   "not-a-jwt",
	})
	if err == nil {
		t.Fatal("PrepareLocalJobRunOptions() error = nil, want invalid token")
	}
	if got, want := err.Error(), "invalid local job token: token is malformed: token contains an invalid number of segments"; got != want {
		t.Fatalf("PrepareLocalJobRunOptions() error = %q, want %q", got, want)
	}
}

func TestPrepareLocalJobRunOptionsChecksReferenceValidationAfterIdentity(t *testing.T) {
	_, err := workerlivekit.PrepareLocalJobRunOptions("", workerlivekit.LocalJobOptions{FakeJob: false})
	if err == nil {
		t.Fatal("PrepareLocalJobRunOptions() error = nil, want missing identity")
	}
	if got, want := err.Error(), "agent_identity is None but fake_job is False"; got != want {
		t.Fatalf("PrepareLocalJobRunOptions() error = %q, want %q", got, want)
	}
}

func TestLocalJobSessionReportPathPrefersExplicitPath(t *testing.T) {
	got := workerlivekit.LocalJobSessionReportPath(workerlivekit.LocalJobOptions{
		SessionReportPath: "reports/explicit.json",
	}, "sessions/job-a")

	if got != "reports/explicit.json" {
		t.Fatalf("LocalJobSessionReportPath() = %q, want explicit path", got)
	}
}

func TestLocalJobSessionReportPathUsesSessionDirectory(t *testing.T) {
	got := workerlivekit.LocalJobSessionReportPath(workerlivekit.LocalJobOptions{}, "sessions/job-a")
	want := filepath.Join("sessions/job-a", "session_report.json")

	if got != want {
		t.Fatalf("LocalJobSessionReportPath() = %q, want %q", got, want)
	}
}

func TestLocalJobSessionReportPathEmptyWithoutOutput(t *testing.T) {
	got := workerlivekit.LocalJobSessionReportPath(workerlivekit.LocalJobOptions{}, "")

	if got != "" {
		t.Fatalf("LocalJobSessionReportPath() = %q, want empty", got)
	}
}
