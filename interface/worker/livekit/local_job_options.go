package livekit

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	lkprotocol "github.com/livekit/protocol/livekit"
)

type LocalJobOptions struct {
	FakeJob           bool
	RoomInfo          *lkprotocol.Room
	Token             string
	RecordingOptions  agent.RecordingOptions
	SessionReportPath string
	SessionDirectory  string
}

func DefaultFakeLocalJobOptions() LocalJobOptions {
	return LocalJobOptions{FakeJob: true}
}

func DefaultServerFakeLocalJobOptions() LocalJobOptions {
	return DefaultFakeLocalJobOptions()
}

type LocalJobContextValueOptions struct {
	RoomName            string
	ParticipantIdentity string
	APIKey              string
	APISecret           string
	TTL                 time.Duration
	Options             LocalJobOptions
	NewIdentity         func(string) string
}

type LocalJobContextValuesResult struct {
	Job                 *lkprotocol.Job
	ParticipantIdentity string
	Token               string
}

type LocalJobContextSetupPlanOptions = LocalJobContextValueOptions

type LocalJobContextSetupPlanResult struct {
	Job              *lkprotocol.Job
	AcceptIdentity   string
	Token            string
	FakeJob          bool
	InitRecording    bool
	RecordingOptions agent.RecordingOptions
	SessionDirectory string
}

func PrepareLocalJobRunOptions(participantIdentity string, opts LocalJobOptions) (string, error) {
	identity, err := LocalJobParticipantIdentityForRun(opts.Token, participantIdentity)
	if err != nil {
		return "", err
	}
	if err := ValidateLocalJobRunOptions(identity, opts); err != nil {
		return "", err
	}
	return identity, nil
}

func PrepareServerLocalJobRunOptions(participantIdentity string, opts LocalJobOptions) (string, error) {
	return PrepareLocalJobRunOptions(participantIdentity, opts)
}

func LocalJobSessionReportPath(opts LocalJobOptions, sessionDirectory string) string {
	if opts.SessionReportPath != "" {
		return opts.SessionReportPath
	}
	if sessionDirectory == "" {
		return ""
	}
	return filepath.Join(sessionDirectory, "session_report.json")
}

func LocalJobContextValues(opts LocalJobContextValueOptions) LocalJobContextValuesResult {
	localOptions := opts.Options
	token := localOptions.Token
	participantIdentity := LocalJobIdentity(token, opts.ParticipantIdentity, opts.NewIdentity)
	job := LocalRoomJob(LocalRoomJobOptions{
		RoomName: opts.RoomName,
		RoomInfo: localOptions.RoomInfo,
		FakeJob:  localOptions.FakeJob,
	})
	generatedToken, err := LocalJobToken(token, opts.APIKey, opts.APISecret, participantIdentity, opts.RoomName, opts.TTL)
	if err == nil {
		token = generatedToken
	}
	return LocalJobContextValuesResult{
		Job:                 job,
		ParticipantIdentity: participantIdentity,
		Token:               token,
	}
}

func LocalJobContextSetupPlan(opts LocalJobContextSetupPlanOptions) LocalJobContextSetupPlanResult {
	values := LocalJobContextValues(LocalJobContextValueOptions(opts))
	localOptions := opts.Options
	return LocalJobContextSetupPlanResult{
		Job:              values.Job,
		AcceptIdentity:   values.ParticipantIdentity,
		Token:            values.Token,
		FakeJob:          localOptions.FakeJob,
		InitRecording:    HasSessionRecordingOption(localOptions.RecordingOptions),
		RecordingOptions: localOptions.RecordingOptions,
		SessionDirectory: localOptions.SessionDirectory,
	}
}

func ValidateLocalJobRunOptions(participantIdentity string, opts LocalJobOptions) error {
	if !opts.FakeJob && participantIdentity == "" && opts.Token == "" {
		return fmt.Errorf("agent_identity is None but fake_job is False")
	}
	if !opts.FakeJob && opts.RoomInfo == nil {
		return fmt.Errorf("room_info is None but fake_job is False")
	}
	return nil
}
