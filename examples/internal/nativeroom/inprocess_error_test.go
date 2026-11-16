//go:build nativeinprocess

package nativeroom

import (
	"context"
	"errors"
	"fmt"
	"github.com/cavos-io/rtp-agent/core/agent"
	native "native-receive-probe/nativeinprocess"
	"testing"
)

func TestNativeOutputClosureClassification(t *testing.T) {
	failure := errors.New("capture failed")
	for _, tc := range []struct {
		name            string
		err, cause, run error
		normal          bool
	}{
		{"unsubscribe", native.ErrClosed, fmt.Errorf("track unsubscribed: %w", native.ErrSourceEnded), nil, true},
		{"explicit close", native.ErrClosed, nil, context.Canceled, true},
		{"unexpected close", native.ErrClosed, nil, nil, false},
		{"overflow", native.ErrClosed, native.ErrBackpressure, nil, false},
		{"failure while canceling", failure, nil, context.Canceled, false},
		{"joined failure", errors.Join(native.ErrClosed, failure), native.ErrSourceEnded, nil, false},
		{"deadline", native.ErrClosed, nil, context.DeadlineExceeded, false},
		{"source and failure", native.ErrClosed, errors.Join(native.ErrSourceEnded, failure), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nativeOutputError(tc.err, tc.cause, tc.run)
			if errors.Is(got, agent.ErrAudioOutputEnded) != tc.normal {
				t.Fatalf("classification: %v", got)
			}
			if !tc.normal && !errors.Is(got, tc.err) {
				t.Fatal("real failure lost")
			}
		})
	}
}
