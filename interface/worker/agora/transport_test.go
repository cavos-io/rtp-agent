package agora

import (
	"context"
	"errors"
	"testing"

	"github.com/cavos-io/rtp-agent/interface/worker"
)

type fakeChannelClient struct {
	joinOptions worker.AgoraOptions
	joinErr     error
	leaveErr    error
	joined      bool
	left        bool
}

func (f *fakeChannelClient) Join(ctx context.Context, opts worker.AgoraOptions) error {
	f.joinOptions = opts
	if f.joinErr != nil {
		return f.joinErr
	}
	f.joined = true
	return nil
}

func (f *fakeChannelClient) Leave(ctx context.Context) error {
	if f.leaveErr != nil {
		return f.leaveErr
	}
	f.left = true
	return nil
}

func TestTransportJoinValidatesOptions(t *testing.T) {
	tr := NewTransport(worker.AgoraOptions{}, &fakeChannelClient{})

	err := tr.Join(context.Background())
	if err == nil {
		t.Fatal("Join() error = nil, want validation error")
	}
}

func TestTransportJoinPassesOptionsToClient(t *testing.T) {
	client := &fakeChannelClient{}
	opts := worker.AgoraOptions{
		AppID:   "app",
		Channel: "support",
		UID:     "agent",
		Token:   "token",
	}
	tr := NewTransport(opts, client)

	if err := tr.Join(context.Background()); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
	if !client.joined {
		t.Fatal("client joined = false, want true")
	}
	if client.joinOptions.Channel != "support" {
		t.Fatalf("client channel = %q, want support", client.joinOptions.Channel)
	}
}

func TestTransportLeaveDelegatesToClient(t *testing.T) {
	client := &fakeChannelClient{}
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, client)

	if err := tr.Leave(context.Background()); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
	if !client.left {
		t.Fatal("client left = false, want true")
	}
}

func TestTransportReturnsClientErrors(t *testing.T) {
	joinErr := errors.New("join failed")
	tr := NewTransport(worker.AgoraOptions{AppID: "app", Channel: "support"}, &fakeChannelClient{joinErr: joinErr})

	if err := tr.Join(context.Background()); !errors.Is(err, joinErr) {
		t.Fatalf("Join() error = %v, want %v", err, joinErr)
	}
}
