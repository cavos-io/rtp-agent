package agent

import (
	"errors"
	"fmt"
	"testing"
)

func TestTTSErrorNormalOutputEnd(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"normal", ErrAudioOutputEnded, false},
		{"wrapped normal", fmt.Errorf("output: %w", ErrAudioOutputEnded), false},
		{"provider failure", errors.New("provider failed"), true},
		{"unexpected native closure", errors.New("native runtime/session closed"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
			events := session.ErrorEvents()
			pipeline := &PipelineAgent{}
			if !tc.want {
				// No logger/session is attached: expected output closure must return
				// before entering provider-warning logging.
				pipeline.logTTSError("TTS inference failed", tc.err)
			}
			pipeline.emitTTSError(session, tc.err)
			select {
			case <-events:
				if !tc.want {
					t.Fatal("normal output closure reported as TTS failure")
				}
			default:
				if tc.want {
					t.Fatal("real failure hidden")
				}
			}
		})
	}
}
