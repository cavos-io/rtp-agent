package tts

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestSynthesisTimingStartsAtSubmissionAndSurvivesRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, timing := WithSynthesisTiming(context.Background())
		if !timing.StartedAt().IsZero() {
			t.Fatal("unsubmitted request has a start time")
		}
		time.Sleep(200 * time.Millisecond)
		want := time.Now()
		MarkSynthesisStarted(ctx)
		time.Sleep(300 * time.Millisecond)
		MarkSynthesisStarted(ctx)
		if !timing.StartedAt().Equal(want) {
			t.Fatal("retry reset the first submission time")
		}
		_, other := WithSynthesisTiming(context.Background())
		if !other.StartedAt().IsZero() {
			t.Fatal("request timing leaked into another request")
		}
		MarkSynthesisStarted(context.Background())
	})
}
