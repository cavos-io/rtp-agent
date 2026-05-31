package beta

import "testing"

func TestDtmfEventToCodeRejectsMultiDigitEvents(t *testing.T) {
	_, err := DtmfEventToCode(DtmfEvent("12"))
	if err == nil {
		t.Fatal(`DtmfEventToCode("12") error = nil, want invalid DTMF event error`)
	}
}

func TestDtmfEventToCodeRejectsLowercaseLetters(t *testing.T) {
	_, err := DtmfEventToCode(DtmfEvent("a"))
	if err == nil {
		t.Fatal(`DtmfEventToCode("a") error = nil, want invalid DTMF event error`)
	}
}
