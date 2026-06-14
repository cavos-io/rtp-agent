package agora

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/interface/worker"
)

type ChannelClient interface {
	Join(context.Context, worker.AgoraOptions) error
	Leave(context.Context) error
}

type Transport struct {
	opts   worker.AgoraOptions
	client ChannelClient
}

func NewTransport(opts worker.AgoraOptions, client ChannelClient) *Transport {
	return &Transport{
		opts:   opts,
		client: client,
	}
}

func (t *Transport) Join(ctx context.Context) error {
	if t == nil {
		return fmt.Errorf("agora transport is nil")
	}
	if err := t.opts.Validate(); err != nil {
		return err
	}
	if t.client == nil {
		return fmt.Errorf("agora channel client is required")
	}
	return t.client.Join(ctx, t.opts)
}

func (t *Transport) Leave(ctx context.Context) error {
	if t == nil || t.client == nil {
		return nil
	}
	return t.client.Leave(ctx)
}
