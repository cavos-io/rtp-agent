//go:build nativeinprocess

package nativeroom

import (
	"errors"
	wl "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

// ConfigureRoomOptions is called by JobContext before custom transport startup.
// It applies only to the in-process path; helper behavior is unchanged.
func (r *Room) ConfigureRoomOptions(opts wl.RoomOptions) error {
	if !r.microphoneLifecycle {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.cancel != nil {
		return errors.New("native room options must be configured before startup")
	}
	if !opts.DisablePreConnectAudio {
		return errors.New("in-process native requires DisablePreConnectAudio=true; pre-connect audio is unsupported")
	}
	if opts.ParticipantIdentity != "" && r.Config.Participant != "" && opts.ParticipantIdentity != r.Config.Participant {
		return errors.New("RoomOptions.ParticipantIdentity conflicts with native participant selection")
	}
	if opts.ParticipantIdentity != "" {
		r.Config.Participant = opts.ParticipantIdentity
	}
	r.Config.captionOptions = wl.RoomOptions{RecordRemoteParticipants: opts.RecordRemoteParticipants, DisableAudioInput: opts.DisableAudioInput, DisableTextInput: opts.DisableTextInput, TextInputCallback: opts.TextInputCallback, DisableTranscriptionOutput: opts.DisableTranscriptionOutput, TranscriptionNextOutput: opts.TranscriptionNextOutput}
	return nil
}
