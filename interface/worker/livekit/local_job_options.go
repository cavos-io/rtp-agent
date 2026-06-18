package livekit

import (
	"fmt"
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

func ValidateLocalJobRunOptions(participantIdentity string, opts LocalJobOptions) error {
	if !opts.FakeJob && participantIdentity == "" && opts.Token == "" {
		return fmt.Errorf("agent_identity is None but fake_job is False")
	}
	if !opts.FakeJob && opts.RoomInfo == nil {
		return fmt.Errorf("room_info is None but fake_job is False")
	}
	return nil
}
