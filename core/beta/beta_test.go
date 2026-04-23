package beta

import (
	"testing"
)

func TestFormatDtmf(t *testing.T) {
	events := []DtmfEvent{DtmfEventOne, DtmfEventStar, DtmfEventA}
	expected := "1 * A"
	if FormatDtmf(events) != expected {
		t.Errorf("Expected %q, got %q", expected, FormatDtmf(events))
	}
}

func TestDtmfEventToCode(t *testing.T) {
	tests := []struct {
		event    DtmfEvent
		expected int
		err      bool
	}{
		{DtmfEventZero, 0, false},
		{DtmfEventNine, 9, false},
		{DtmfEventStar, 10, false},
		{DtmfEventPound, 11, false},
		{DtmfEventA, 12, false},
		{DtmfEventD, 15, false},
		{DtmfEvent("invalid"), 0, true},
	}

	for _, tt := range tests {
		code, err := DtmfEventToCode(tt.event)
		if (err != nil) != tt.err {
			t.Errorf("For %s, expected error: %v, got: %v", tt.event, tt.err, err)
		}
		if !tt.err && code != tt.expected {
			t.Errorf("For %s, expected code: %d, got: %d", tt.event, tt.expected, code)
		}
	}
}
