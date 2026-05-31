package math

import (
	"testing"
	"time"
)

func TestTimeMSRoundsLikeReference(t *testing.T) {
	previous := timeNow
	timeNow = func() time.Time {
		return time.Unix(1, 999_500_000)
	}
	defer func() {
		timeNow = previous
	}()

	if got := TimeMS(); got != 2000 {
		t.Fatalf("TimeMS() = %d, want rounded millisecond 2000", got)
	}
}

func TestShortUUIDUsesReferenceLength(t *testing.T) {
	got := ShortUUID("prefix-")

	if len(got) != len("prefix-")+12 {
		t.Fatalf("ShortUUID() = %q length %d, want prefix plus 12 hex chars", got, len(got))
	}
}
