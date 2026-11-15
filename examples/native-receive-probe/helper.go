package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	model "github.com/cavos-io/rtp-agent/core/audio/model"
	pb "native-receive-probe/internal/ffipb"
)

var errPublisherDisconnected = errors.New("publisher disconnected")
var errTrackUnsubscribed = errors.New("track unsubscribed")

type nativeSink interface {
	event(any)
	frame(int64, *model.AudioFrame) error
}

type helperSink struct {
	duplex                *duplexOutput
	participant, trackSID string
	encoder               *json.Encoder
	err                   error
	subscribed            func()
}

func (s *helperSink) acceptTrack(t *pb.TrackSubscribed) bool {
	return t.GetParticipantIdentity() == s.participant && t.GetTrack().GetInfo().GetSid() == s.trackSID && t.GetTrack().GetInfo().GetKind() == pb.TrackKind_KIND_AUDIO
}
func (s *helperSink) event(value any) {
	// Control events share the same pipe as PCM; the native loop serializes writes.
	if s.err == nil {
		s.err = s.encoder.Encode(value)
	}
}
func (s *helperSink) frame(at int64, f *model.AudioFrame) error {
	if s.err != nil {
		return s.err
	}
	if f.SampleRate != 24000 || f.NumChannels != 1 || f.SamplesPerChannel != 1200 || len(f.Data) != 2400 {
		return errors.New("unexpected native PCM format")
	}
	s.err = s.encoder.Encode(struct {
		Event string `json:"event"`
		Data  []byte `json:"data"`
		At    int64  `json:"at"`
	}{"pcm", f.Data, at})
	return s.err
}

func runHelper(ctx context.Context) error {
	var config struct{ URL, Token, Participant, TrackSID string }
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 65536)).Decode(&config); err != nil {
		return errors.New("invalid helper configuration")
	}
	if config.URL == "" || config.Token == "" || config.Participant == "" || config.TrackSID == "" {
		return errors.New("incomplete helper configuration")
	}
	sink := &helperSink{participant: config.Participant, trackSID: config.TrackSID, encoder: json.NewEncoder(os.Stdout)}
	// A quiet/unpublished selected track cannot leave startup pending forever.
	startup, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := time.AfterFunc(20*time.Second, cancel)
	defer timer.Stop()
	sink.event(map[string]any{"event": "helper_protocol", "version": 1})
	sink.subscribed = func() { timer.Stop() }
	err := nativeReceive(startup, config.URL, config.Token, sink)
	return sink.finish(err)
}

// finish runs after nativeReceive has released its native resources.
func (sink *helperSink) finish(err error) error {
	if sink.err == nil {
		var reason string
		switch err {
		case errPublisherDisconnected:
			reason = "participant_disconnected"
		case errTrackUnsubscribed:
			reason = "track_unsubscribed"
		}
		if reason != "" {
			sink.event(map[string]any{"event": "ended", "reason": reason})
			return sink.err
		}
	}
	return errors.Join(err, sink.err)
}
