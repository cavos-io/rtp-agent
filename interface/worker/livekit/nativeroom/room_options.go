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
	if opts.AudioTrackName != "" {
		return errors.New("in-process native does not support RoomOptions.AudioTrackName")
	}
	if opts.AudioInputSampleRate != 0 {
		return errors.New("in-process native does not support RoomOptions.AudioInputSampleRate")
	}
	if opts.AudioInputFrameSizeMS != 0 {
		return errors.New("in-process native does not support RoomOptions.AudioInputFrameSizeMS")
	}
	if opts.AudioInputProcessor != nil {
		return errors.New("in-process native does not support RoomOptions.AudioInputProcessor")
	}
	if opts.AudioOutputSampleRate != 0 {
		return errors.New("in-process native does not support RoomOptions.AudioOutputSampleRate")
	}
	if opts.AudioSubscriptionTimeout != 0 {
		return errors.New("in-process native does not support RoomOptions.AudioSubscriptionTimeout")
	}
	if opts.PreConnectAudioTimeout < 0 {
		return errors.New("RoomOptions.PreConnectAudioTimeout must not be negative")
	}
	if opts.DisableAudioInput {
		return errors.New("in-process native does not support RoomOptions.DisableAudioInput")
	}
	if opts.DisableAudioOutput {
		return errors.New("in-process native does not support RoomOptions.DisableAudioOutput")
	}
	if opts.TranscriptionJSONFormat {
		return errors.New("in-process native does not support RoomOptions.TranscriptionJSONFormat")
	}
	if opts.DisableCloseOnDisconnect {
		return errors.New("in-process native does not support RoomOptions.DisableCloseOnDisconnect")
	}
	if opts.DeleteRoomOnClose {
		return errors.New("in-process native does not support RoomOptions.DeleteRoomOnClose")
	}
	if opts.DeleteRoom != nil {
		return errors.New("in-process native does not support RoomOptions.DeleteRoom")
	}
	if len(opts.ParticipantKinds) != 0 {
		return errors.New("in-process native does not support RoomOptions.ParticipantKinds")
	}
	if opts.ParticipantIdentity != "" && r.Config.Participant != "" && opts.ParticipantIdentity != r.Config.Participant {
		return errors.New("RoomOptions.ParticipantIdentity conflicts with native participant selection")
	}
	if opts.ParticipantIdentity != "" {
		r.Config.Participant = opts.ParticipantIdentity
	}
	r.Config.captionOptions = wl.RoomOptions{DisablePreConnectAudio: opts.DisablePreConnectAudio, PreConnectAudioTimeout: opts.PreConnectAudioTimeout, RecordRemoteParticipants: opts.RecordRemoteParticipants, DisableTextInput: opts.DisableTextInput, TextInputCallback: opts.TextInputCallback, DisableTranscriptionOutput: opts.DisableTranscriptionOutput, TranscriptionNextOutput: opts.TranscriptionNextOutput}
	return nil
}
