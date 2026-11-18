//go:build nativeinprocess

package nativeroom

import (
	"context"
	"github.com/cavos-io/rtp-agent/interface/worker"
	"testing"
)

func TestProviderValidatesBeforeLoadingRuntime(t *testing.T) {
	p, err := NewProvider(Config{Library: "missing"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, opts := range []worker.StartSessionOptions{
		{ConnectOptions: []worker.ConnectOptions{{}}},
		{RoomCallback: &worker.RoomCallback{}},
		{RoomOptions: worker.RoomOptions{DisableAudioInput: true}},
	} {
		if _, err := p.NewSession(nil, nil, opts); err == nil {
			t.Fatal("unsupported option accepted")
		}
	}
	if _, err := p.NewSession(nil, nil, worker.StartSessionOptions{}); err != nil {
		t.Fatal(err)
	}
	if p.(*provider).runtime != nil {
		t.Fatal("constructed room acquired runtime")
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewSession(nil, nil, worker.StartSessionOptions{}); err == nil {
		t.Fatal("closed provider accepted session")
	}
	processOwner.Lock()
	closed := processOwner.closed
	processOwner.Unlock()
	if closed {
		t.Fatal("unused provider disposed process runtime")
	}
}
