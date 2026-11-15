package main

import "testing"

func TestDuplexProtocolValidation(t *testing.T) {
	for _, c := range []duplexCommand{
		{Event: "capture", ID: 1000, Data: make([]byte, 2400)},
		{Event: "clear", ID: 1001}, {Event: "stop", ID: 1002},
	} {
		if err := validateDuplexCommand(c); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []duplexCommand{
		{Event: "capture", ID: 1000, Data: make([]byte, 2399)},
		{Event: "clear", ID: 1001, Data: []byte{1}},
		{Event: "unknown", ID: 1000}, {Event: "stop", ID: 3},
	} {
		if validateDuplexCommand(c) == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}
func TestDuplexClearRejectsPendingCapture(t *testing.T) {
	d := &duplexOutput{published: true, pending: map[uint64]bool{1000: true}}
	if _, err := d.command(duplexCommand{Event: "clear", ID: 1001}); err == nil {
		t.Fatal("clear raced pending capture")
	}
}
